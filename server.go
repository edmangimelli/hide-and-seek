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
	seekerIsSeeking bool // false = players are hiding
	round int
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
		var code string // game that this connection is associated with
		var name string // name that this connection is associated with

		go func () { // *** Receive messages from client (external)
			for {
				_, rawMsg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				log.Printf("message received:\n%s", string(rawMsg))
				msg := strings.Split(string(rawMsg), "\n")

				switch msg[0] {

				case "join": // [1] code, [2] name
					mutex.Lock() // LOCK
					if _, exists := games[msg[1]]; !exists { // if game doesn't exist; error
						sendMsg(conn, conn.RemoteAddr().String(), "error\nno such game")
						mutex.Unlock()
						break
					}

					if _, exists := games[msg[1]].players[msg[2]]; exists { // if name is already in use; error
						sendMsg(conn, conn.RemoteAddr().String(), "error\nname is taken")
						mutex.Unlock()
						break
					}
					code = msg[1]
					name = msg[2]
					games[code].players[name] = &player{waitingToJoin: games[code].started, connChan: connChan}
					// player has joined

					for n, v := range games[code].players { // tell other players
						if n != name { // don't need to send the message to yourself
							v.connChan <- fmt.Sprintf("joined\n%s", name)
						}
					}

					// NEED TO SEND CURRENT PLAYERS LIST TO CLIENT
					if games[code].started { // if game has already started
						sendMsg(conn, name, "game is in session\nwill send message when next round starts")
						mutex.Unlock()
						break
					}

					// game hasn't started
					sendMsg(conn, name, "game hasn't started\nwill send messages as players join and when the first round starts")

					mutex.Unlock() // UNLOCK

				case "new game":
					name = msg[1]

					log.Println("try to make new game.")
					var err error
					code, err = newGameCode() // make new game
					if err != nil {
						sendMsg(conn, conn.RemoteAddr().String(), "error\ntoo many games currently in session")
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
					mutex.Unlock() // UNLOCK
					log.Printf("player added: %s\n", name)

					sendMsg(conn, name, fmt.Sprintf("game initialized\n%s\nwill send messages as players join\nmust receive signal to start game", code))

				case "start":
					/*
can we start? at least 2 players
started = true
create a forest
give a UNIQUE random location to every player
send to every player
game has started
hiders hiding
player loc
player loc
player loc
.
html table
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

					for n, v := range games[code].players { // tell other players
						if n != name { // NOT TRUEdon't need to send the message to yourself
							v.connChan <- "game has started"
						}
					}
					mutex.Unlock() // UNLOCK
				}
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

