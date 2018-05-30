package main

import (
	"errors"
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
	log.Printf("             maximum number of games: %d\n", maxCodes)
}

type player struct {
	// round variables
	seeker bool
	found bool
	ready bool
	row, col int
	movesThisRound int

	// game variables
	connChan chan string
	emoji string
	waitingToJoin bool
	score int
	totalMoves int
	numberOfTimesHasBeenSeeker int
	numberOfTimesHasBeenHider int
	numberOfTimesHasEarnedSeeker int
}

type game struct {
	wood forest
	players map[string]*player
	started bool // false = seeker hasn't started the game
	round int
	usedEmojis [][]bool
	santaInUse bool
	multiHiderRound bool
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

		connChan := make(chan string)
		code, emoji, name := "", "", conn.RemoteAddr().String()

		conn.SetCloseHandler(func(codeNumber int, text string) error {
			if name == "" || code == "" { return errors.New("left before joing game.") }
			mutex.Lock()
			switch len(games[code].players) {
			case 1: // they were the only person left. delete the game. nobody needs to be notified.
				delete(games, code)
			case 2: // this needs to be fixed in the event that the only player left is waitingToJoin
				delete(games[code].players, name)
				for _, p := range games[code].players { // tell only player
					p.connChan <- "too few hiders"
				}
			default: // send left message and maybe send winner message
				for n, p := range games[code].players { // tell other players
					if n != name {
						p.connChan <- fmt.Sprintf("left\n%s\n%s\n%d\n%d", emoji, name, games[code].players[name].row, games[code].players[name].col)
					}
				}
				if (games[code].players[name].seeker) {
					for _, p := range games[code].players { // tell other players. who aren't waiting to join
						if p.waitingToJoin { continue }
						p.connChan <- "round over\nseeker left"
					}
				} else {
					reportWinnerIfThereIsOne(games[code])
				}
				delete(games[code].players, name)
			}
			mutex.Unlock()
			return errors.New("left.")
		})


		go func () { // *** Receive messages from client (external)
			for {
				_, rawMsg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				log.Printf("\n✉ message received from %s/%s:\n%s\n", code, name, string(rawMsg))
				msg := strings.Split(string(rawMsg), "\n")

				switch msg[0] { // 6 message types can be received:

				case "join": // code // name
					mutex.Lock()

					if _, exists := games[msg[1]]; !exists {
						sendMsg(conn, code, name, fmt.Sprintf("no such game\n%s", msg[1]))
						mutex.Unlock()
						break
					}

					if _, exists := games[msg[1]].players[msg[2]]; exists {
						sendMsg(conn, code, name, fmt.Sprintf("name is taken\n%s", msg[2]))
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
						row: -1,
						col: -1,
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

					if games[code].players[name].seeker {

						if occ != "" {
							games[code].players[occ].found = true
							if !reportWinnerIfThereIsOne(games[code]) {
								for _, v := range games[code].players { // tell other players
									v.connChan <- fmt.Sprintf("found\n%s\n%s\n%d\n%d", games[code].players[occ].emoji, occ, row, col)
								}
							}
						}

						for _, v := range games[code].players { // tell other players
							v.connChan <- fmt.Sprintf("moved\n%s\nfrom\n%d\n%d\nto\n%d\n%d", emoji, games[code].players[name].row, games[code].players[name].col, row, col)
						}

						games[code].players[name].row = row
						games[code].players[name].col = col

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
						row: -1,
						col: -1,
					}
					mutex.Unlock()
					log.Printf("\nplayer has joined: %s/%s\n", code, name)

					sendMsg(conn, code, name, fmt.Sprintf("game initialized\n%s\n%s\n%s", code, emoji, name))

				case "ready to go":
					mutex.Lock()
					games[code].players[name].ready = true
					if everyonesReady(games[code]) {
						for _, p := range games[code].players { // tell non-waiting players 
							if p.waitingToJoin { continue }
							p.ready = false
							p.connChan <- "go!"
						}
					}
					mutex.Unlock()

				case "ready for next setup":
					mutex.Lock()
					games[code].players[name].ready = true
					if everyonesReady(games[code]) {
						newSetup(games[code])
					}
					mutex.Unlock()
				case "start":
					mutex.Lock()
					games[code].started = true
					newSetup(games[code])
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
	log.Printf("\n📝 message sent to %s/%s:\n%s\n", code, name, msg)
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

	for n, p := range g.players {
		if !p.seeker && !p.found && !p.waitingToJoin {
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
	for _, p := range g.players {
		if p.waitingToJoin { continue }
		if !p.ready {
			return false
		}
	}
	return true
}

func everyonesFound(g *game) bool {
	for _, p := range g.players {
		if p.seeker || p.waitingToJoin { continue }
		if !p.found {
			return false
		}
	}
	return true
}


func newSetup(g *game) {

	if len(g.players) < 2 {
		for _, v := range g.players { // tell only player
			v.connChan <- "too few hiders"
		}
		return
	}

	g.multiHiderRound = len(g.players) > 2

	//if there's no seeker (seeker left)
	if noSeeker(g) { randomlyAppointSeeker(g) }

	g.wood = growForest(g.players)

	populateForest(g) // everyone's given a random row and col

	/* DEBUG
	for _, s := range g.wood {
		fmt.Println(string(s))
	}
	for n, p := range g.players {
		fmt.Printf("%s (%d, %d)", n, p.x, p.y)
	}
	*/

	reply := fmt.Sprintf("setup\nseeker %s", seekerEmoji(g))

	reply += fmt.Sprintf("\nforest\n%d\n", len(g.wood[0]))
	for _, treeLine := range g.wood {
		reply += string(treeLine)
	}

	for n := range g.players {
		g.players[n].found = false;
		g.players[n].ready = false;
		g.players[n].waitingToJoin = false;
		reply += fmt.Sprintf("\n%s\n%s\n%d\n%d\n%d", g.players[n].emoji, n, g.players[n].row, g.players[n].col, g.players[n].score)
	}

	for _, v := range g.players { // tell everyone
		v.connChan <- reply
	}

	return
}

func noSeeker(g *game) bool {
	s, seeker, seekers := 0, "", ""

	for n, p := range g.players {
		if p.seeker {
			s++
			seeker = n
			seekers += fmt.Sprintf("%s\n", n)
		}
	}

	switch {
	case s > 1:
		log.Fatalf("CRASH: too many seekers!\n%s", seekers)
	case s == 0:
		return true
	}

	if g.players[seeker].waitingToJoin {
		log.Fatalf("CRASH: seeker is waiting to join!\n%s", seeker)
	}
	return false
}

func randomlyAppointSeeker(g *game) {
	log.Println("Randomly appointing seeker!")
	for {
		r := random.Intn(len(g.players))
		for _, p := range g.players {
			if r == 0 {
				if p.waitingToJoin {
					break
				} else {
					p.seeker = true
					return
				}
			}
			r--
		}
	}
}

func reportWinnerIfThereIsOne(g *game) bool {

	if g.multiHiderRound {
		last := onlyOneHiderLeft(g)
		if last != "" {
			for _, p := range g.players { // tell other players. who aren't waiting to join
				if p.waitingToJoin { continue }
				if p.seeker { p.seeker = false }
				p.connChan <- fmt.Sprintf("winner\n%s\n%s", g.players[last].emoji, last)
			}
			g.players[last].seeker = true
			return true
		}
	} else {
		if everyonesFound(g) {
			for _, p := range g.players { // tell other players. who aren't waiting to join
				if p.waitingToJoin { continue }
				if p.seeker { p.seeker = false } else { p.seeker = true } // preparing for another 2 player game but the next game could be multiHider
				p.connChan <- "round over\n2 player game"
			}
			return true
		}
	}
	return false
}
