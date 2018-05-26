package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
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
	emoji rune
	seeker bool
	col, row int
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
		var code, name string
		var emoji rune

		go func () { // *** Receive messages from client (external)
			for {
				_, rawMsg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				log.Printf("\nmessage received:\n%s\n", string(rawMsg))
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


				case "move to": // col // row

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

				case "ready":
				case "ready for next round":
				case "start":
					mutex.Lock()

					if len(games[code].players) < 2 {
						sendMsg(conn, code, name, "too few hiders")
						mutex.Unlock()
						break
					}

					games[code].started = true

					games[code].wood = growForest(games[code].players)

					populateForest(games[code]) // everyone's given a random col and row

					/* DEBUG
					for _, s := range games[code].wood {
						fmt.Println(string(s))
					}
					for n, p := range games[code].players {
						fmt.Printf("%s (%d, %d)", n, p.x, p.y)
					}
					*/

					reply := "setup"
					for n := range games[code].players {
						reply += fmt.Sprintf("\n%s %s %s", games[code].players[n].emoji, games[code].players[n].col, games[code].players[n].row)
					}

					reply += fmt.Sprintf("\nforest\n%d\n", len(games[code].wood[0]))
					for treeLine := range games[code].wood {
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
			msg := strings.Split(string(rawMsg), "\n")

			switch msg[0] { // most of these simply relay the msg to the client
			case "joined", "game has started":
				sendMsg(conn, code, name, rawMsg)
			}
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

