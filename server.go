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
}

type game struct {
	grid string
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
		conn, _ := upgrader.Upgrade(w, r, nil) // error ignored for sake of simplicity
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
						writeMsg(conn, conn.RemoteAddr().String(), "error\nno such game")
						break
					}

					if _, exists := games[msg[1]].players[msg[2]]; exists { // if name is already in use; error
						writeMsg(conn, conn.RemoteAddr().String(), "error\nname is taken")
						break
					}
					code = msg[1]
					name = msg[2]
					games[code].players[name] = &player{x: 0, y: 0, waitingToJoin: games[code].started, connChan: connChan}
					// player has joined

					for n, v := range games[code].players { // tell other players
						if n != name { // don't need to send the message to yourself
							v.connChan <- fmt.Sprintf("joined\n%s", name)
						}
					}

					if games[code].started { // if game has already started
						writeMsg(conn, name, "game is in session\nwill send message when next round starts")
						break
					}

					// game hasn't started
					writeMsg(conn, name, "game hasn't started\nwill send messages as players join and when the first round starts")

					mutex.Unlock() // UNLOCK

				case "new game":
					name = msg[1]

					log.Println("try to make new game.")
					var err error
					code, err = newGameCode() // make new game
					if err != nil {
						writeMsg(conn, conn.RemoteAddr().String(), "error\ntoo many games currently in session")
						break
					}

					mutex.Lock() // LOCK
					games[code] = &game{ // initialize
						grid: "",
						players: make(map[string]*player),
					}
					log.Printf("new game created: %s\n", code)

					games[code].players[name] = &player{ // add seeker to list of players
						seeker: true,
						x: 0, y: 0,
						connChan: connChan,
					}
					mutex.Unlock() // UNLOCK
					log.Printf("player added: %s\n", name)

					writeMsg(conn, name, fmt.Sprintf("game initialized\n%s\nwill send messages as players join\nmust receive signal to start game", code))

				case "start game":
					mutex.Lock() // LOCK
					games[code].started = true
					for n, v := range games[code].players { // tell other players
						if n != name { // don't need to send the message to yourself
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
				writeMsg(conn, name, rawMsg)
			}
		}
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "client.html")
	})

	http.ListenAndServe(":8080", nil)
}

func writeMsg(conn *websocket.Conn, name string, msg string) error {
	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		log.Println("conn.WriteMessage failed:\nconnection:\n%s\nmsg:\n%s\n", conn.RemoteAddr().String(), msg)
		return err
	}
	log.Printf("message sent to %s:\n%s", name, msg)
	return nil
}

