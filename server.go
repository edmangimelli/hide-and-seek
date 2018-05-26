package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

type player struct {
	seeker bool
   x, y int
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

		connChan := make(chan string)
		var code string // game that this connection is associated with
		var name string // name that this connection is associated with
		var emoji rune

		go func () { // *** Receive messages from client (external)
			for {
				_, rawMsg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				log.Printf("message received:\n%s", string(rawMsg))
				msg := strings.Split(string(rawMsg), "\n")

				switch msg[0] { // 6 message types can be received:

				case "join": // code // name
					mutex.Lock() // LOCK
					if _, exists := games[msg[1]]; !exists { // if game doesn't exist; error
						sendMsg(conn, conn.RemoteAddr().String(), "no such game")
						mutex.Unlock()
						break
					}

					if _, exists := games[msg[1]].players[msg[2]]; exists { // if name is already in use; error
						sendMsg(conn, conn.RemoteAddr().String(), "name is taken")
						mutex.Unlock()
						break
					}
					code = msg[1]
					name = msg[2]
					games[code].players[name] = &player{waitingToJoin: games[code].started, connChan: connChan}
					emoji = randomEmoji(games[code], name)
					games[code].players[name].emoji = emoji
					// player has joined

					for n, v := range games[code].players { // tell other players
						if n != name { // don't need to send the message to yourself
							v.connChan <- fmt.Sprintf("joined\n%s\n%s", emoji, name)
						}
					}

					var msg string
					if games[code].started { // if game has already started
						msg = "wait for next round"
					} else {
						msg = "wait for start"
					}
					msg += fmt.Sprintf("\n%s\n%s\n%s", code, emoji, name)
					for p := range games[code].players {
						if p.name != name {
							msg += fmt.Sprinf("\n%s\n%s", p.emoji, p.name)
						}
					}

					sendMsg(conn, name, msg)

					mutex.Unlock() // UNLOCK

				case "move to": // col // row
				case "new game": // name
					name = msg[1]

					log.Println("try to make new game.")
					var err error
					code, err = newGameCode() // make new game
					if err != nil {
						sendMsg(conn, conn.RemoteAddr().String(), "too many games in session")
						break
					}

					mutex.Lock() // LOCK
					games[code] = &game{ // initialize
						players: make(map[string]*player),
					}
					log.Printf("new game created: %s\n", code)

					games[code].players[name] = &player{ // add seeker to list of players
						seeker: true,
						connChan: connChan,
					}

					games[code].usedEmojis :=  make([][]bool, len(emojis))
					for i := range games[code].usedEmojis {
						games[code].usedEmojis[i] := make([]bool, len(emojis[i])
					}
					emoji = randomEmoji(games[code], name)
					games[code].players[name].emoji = emoji

					mutex.Unlock() // UNLOCK
					log.Printf("player added: %s\n", name)

					sendMsg(conn, name, fmt.Sprintf("game initialized\n%s\n%s\n%s", code, emoji, name))

				case "ready":
				case "ready for next round":
				case "start":
					/*
can we start? at least 2 players
started = true
create a forest
give a UNIQUE random location to every player
send to every player

positions
ed
3
4
bri
4
4

forest
....
....
....
....


got forest

got positions

go!




					*/
					mutex.Lock() // LOCK
					if len(games[code].players) < 2 {
						// error
						mutex.Unlock()
						break
					}
					games[code].started = true
					games[code].wood = growForest(games[code].players)
					populateForest(games[code])
					for _, s := range games[code].wood {
						fmt.Println(string(s))
					}
					for n, p := range games[code].players {
						fmt.Printf("%s (%d, %d)", n, p.x, p.y)
					}

					for n, v := range games[code].players { // tell other players
						if n != name { // NOT TRUEdon't need to send the message to yourself
							v.connChan <- "game has started"
						}
					}
					mutex.Unlock() // UNLOCK

				} // switch end
			}
		}()

		// *** Receive messages from other players (internal)
		for {
			rawMsg := <-connChan
			msg := strings.Split(string(rawMsg), "\n")

			switch msg[0] { // most of these simply relay the msg to the client
			case "joined", "game has started":
				sendMsg(conn, name, rawMsg)
			}
		}
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "client.html")
	})

	http.ListenAndServe(":8080", nil)
}

func sendMsg(conn *websocket.Conn, name string, msg string) error {
	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		log.Println("conn.WriteMessage failed:\nconnection:\n%s\nmsg:\n%s\n", conn.RemoteAddr().String(), msg)
		return err
	}
	log.Printf("message sent to %s:\n%s", name, msg)
	return nil
}

