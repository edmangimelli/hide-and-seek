package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var random *rand.Rand
func init() {
   source := rand.NewSource(time.Now().UnixNano())
   random = rand.New(source)
   log.Println("There are", maxCodes, "possible game codes.")
}

type player struct {
	emoji string
	seeker bool
	found bool
	ready bool
	row, col int
	waitingToJoin bool
	connChan chan string
	numberOfTimesHasBeenSeeker int
	numberOfTimesHasBeenHider int
	numberOfTreesChopped int
	numberOfHidingSpotChanges int
}

type game struct {
	wood forest
	players map[string]*player
	started bool // false = seeker hasn't started the game
	round int
	usedEmojis [][]bool
	santaInUse bool
}

var games = make(map[string]*game, 0)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

var mutex = sync.Mutex{}

func main() {
	http.HandleFunc("/socket", func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)

		// conn.SetCloseHandler(func())
		// delete(games[code].players, player)

		connChan := make(chan string)
		code, emoji, name := "", "", conn.RemoteAddr().String()

		go func () { // *** Receive messages from client (external)
			for {
				_, rawMsg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				log.Printf("\nmessage received from %s/%s:\n%s\n", code, name, string(rawMsg))
				msg := strings.Split(string(rawMsg), "\n")

				switch msg[0] { // 6 message types can be received:

				case "join": // code // name
					mutex.Lock()

					if _, exists := games[msg[1]]; !exists {
						sendMsg(conn, fmt.Sprintf("(!provided:) %s", msg[1]), fmt.Sprintf("(!provided:) %s", msg[2]), "no such game")
						mutex.Unlock()
						break
					}

					if _, exists := games[msg[1]].players[msg[2]]; exists {
						sendMsg(conn, fmt.Sprintf("(!provided:) %s", msg[1]), fmt.Sprintf("(!provided:) %s", msg[2]), "name is taken")
						mutex.Unlock()
						break
					}

					code = msg[1]
					name = msg[2]
					emoji = randomEmoji(games[code], name)
					games[code].players[name] = &player{
						emoji: emoji,
						waitingToJoin: games[code].started,
						connChan: connChan,
					}
					log.Printf("\nplayer has joined: %s/%s\n", code, name)

					for n, v := range games[code].players { // tell other players
						if n != name { // don't need to send the message to yourself
							v.connChan <- fmt.Sprintf("joined\n%s\n%s", emoji, name)
						}
					}

					var reply string
					if games[code].started {
						reply = "wait for next round"
					} else {
						reply = "wait for start"
					}
					reply += fmt.Sprintf("\n%s\n%s\n%s", code, emoji, name)
					for n := range games[code].players {
						if n != name {
							reply += fmt.Sprintf("\n%s\n%s", games[code].players[n].emoji, n)
						}
					}

					mutex.Unlock()
					sendMsg(conn, code, name, reply)


				case "move to": // row // col
					row, _ := strconv.Atoi(msg[1])
					col, _ := strconv.Atoi(msg[2])

					mutex.Lock()
					if games[code].wood[row][col] == ' ' { // can't move to non-tree
						log.Printf("\ncan't move there. no tree.\n")
						mutex.Unlock()
						break
					}

					occ := occupant(row, col, games[code])

					if games[code].players[name].seeker { // seeker
						for _, v := range games[code].players { // tell other players
							v.connChan <- fmt.Sprintf("moved\n%s\nfrom\n%d\n%d\nto\n%d\n%d", emoji, games[code].players[name].row, games[code].players[name].col, row, col)
						}

						games[code].players[name].row = row
						games[code].players[name].col = col

						if occ != "" {
							games[code].players[occ].found = true
							for _, v := range games[code].players { // tell other players
								v.connChan <- fmt.Sprintf("found\n%s\n%s", games[code].players[occ].emoji, occ)
							}
						}

						last := onlyOneHiderLeft(games[code])
						if last != "" {
							for _, v := range games[code].players { // tell other players
								v.connChan <- fmt.Sprintf("winner\n%s\n%s", games[code].players[last].emoji, last)
							}
						}

					} else { // hider
						if occ != "" {
							mutex.Unlock()
							break
						}

						for _, v := range games[code].players { // tell other players
							v.connChan <- fmt.Sprintf("moved\n%s\nfrom\n%d\n%d\nto\n%d\n%d", emoji, games[code].players[name].row, games[code].players[name].col, row, col)
						}

						games[code].players[name].row = row
						games[code].players[name].col = col
					}

					mutex.Unlock()

				case "new game": // name
					name = msg[1]
					log.Printf("\n?/%s is trying to initialize new game.\n", name)

					mutex.Lock()
					var err error
					code, err = newGameCode() // make new game
					if err != nil {
						sendMsg(conn, "?", name, "too many games in session")
						mutex.Unlock()
						break
					}

					games[code] = &game{
						players: make(map[string]*player),
						usedEmojis: make([][]bool, len(emojis)),
					}
					for i := range games[code].usedEmojis {
						games[code].usedEmojis[i] = make([]bool, len(emojis[i]))
					}
					log.Printf("\nnew game created: %s\n", code)

					emoji = randomEmoji(games[code], name)
					games[code].players[name] = &player{
						emoji: emoji,
						seeker: true,
						connChan: connChan,
					}
					mutex.Unlock()
					log.Printf("\nplayer has joined: %s/%s\n", code, name)

					sendMsg(conn, code, name, fmt.Sprintf("game initialized\n%s\n%s\n%s", code, emoji, name))

				case "ready to go":
					mutex.Lock()
					games[code].players[name].ready = true
					if everyonesReady(games[code]) {
						for _, v := range games[code].players { // tell other players
							v.connChan <- "go!"
						}
					}
					mutex.Unlock()

				case "ready for next setup":
				case "start":
					mutex.Lock()

					if len(games[code].players) < 2 {
						sendMsg(conn, code, name, "too few hiders")
						mutex.Unlock()
						break
					}

					games[code].started = true

					games[code].wood = growForest(games[code].players)

					populateForest(games[code]) // everyone's given a random row and col

					/* DEBUG
					for _, s := range games[code].wood {
						fmt.Println(string(s))
					}
					for n, p := range games[code].players {
						fmt.Printf("%s (%d, %d)", n, p.x, p.y)
					}
					*/

					reply := fmt.Sprintf("setup\nseeker %s", seekerEmoji(games[code]))
					for n := range games[code].players {
						reply += fmt.Sprintf("\n%s %d %d", games[code].players[n].emoji, games[code].players[n].row, games[code].players[n].col)
					}

					reply += fmt.Sprintf("\nforest\n%d\n", len(games[code].wood[0]))
					for _, treeLine := range games[code].wood {
						reply += string(treeLine)
					}

					for _, v := range games[code].players { // tell other players
						v.connChan <- reply
					}
					mutex.Unlock()

				} // switch end
			}
		}()

		// *** Receive messages from other players (internal)
		for {
			rawMsg := <-connChan
			//msg := strings.Split(string(rawMsg), "\n")

			sendMsg(conn, code, name, rawMsg)

			//switch msg[0] { // most of these simply relay the msg to the client
			//case "joined", "game has started", "setup":
			//}
		}
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "client.html")
	})

	http.ListenAndServe(":8080", nil)
}

func sendMsg(conn *websocket.Conn, code string, name string, msg string) error {
	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		log.Printf("\nconn.WriteMessage failed:\nconnection:\n%s\nto: %s/%s\nmsg:\n%s\n", conn.RemoteAddr().String(), code, name, msg)
		return err
	}
	log.Printf("\nmessage sent to %s/%s:\n%s\n", code, name, msg)
	return nil
}

func seekerEmoji(g *game) string {
	for n := range g.players {
		if g.players[n].seeker {
			return g.players[n].emoji
		}
	}
	return ""
}

func occupant(row int, col int, g *game) string {
	for n := range g.players {
		if !g.players[n].found && !g.players[n].waitingToJoin && g.players[n].row == row && g.players[n].col == col {
				return n
		}
	}
	return ""
}

func onlyOneHiderLeft(g *game) string {
	notFound := 0
	last := ""

	for n := range g.players {
		if !g.players[n].found && !g.players[n].waitingToJoin {
			notFound++
			last = n
		}
	}

	if notFound == 1 {
		return last
	} else {
		return ""
	}
}

func everyonesReady(g *game) bool {
	for n := range g.players {
		if !g.players[n].ready {
			return false
		}
	}
	return true
}
