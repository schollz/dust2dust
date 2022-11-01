package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hypebeast/go-osc/osc"
	log "github.com/schollz/logger"
)

var addr = flag.String("addr", "localhost:8097", "http service address")
var name = flag.String("name", "", "your name")
var room = flag.String("room", "dust", "room")
var oscrecv = flag.String("osc-recv", "", "address to receive osc")
var oscsend = flag.String("osc-send", "", "address to send osc (local norns)")
var runserver = flag.Bool("server", false, "run server")
var debugMode = flag.Bool("debug", false, "debug mode")

func main() {
	flag.Parse()
	if *debugMode {
		log.SetLevel("debug")
	} else {
		log.SetLevel("error")
	}

	var err error
	if *runserver {
		err = runServer()
	} else {
		err = runClient()
	}
	if err != nil {
		log.Error(err)
	}
}

// norns client code

func runClient() (err error) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// connect to websocket server
	scheme := "ws"
	connectURL := *addr
	if strings.Contains(connectURL, "https") {
		scheme = "wss"
	}
	connectURL = strings.TrimPrefix(connectURL, "https://")
	connectURL = strings.TrimPrefix(connectURL, "http://")
	u := url.URL{Scheme: scheme, Host: connectURL, Path: "/" + *room + "/" + *name}
	log.Debugf("connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Error("dial:", err)
		os.Exit(1)
	}
	defer c.Close()

	// startup osc client
	var oscClient *osc.Client
	if *oscsend != "" {
		foo := strings.Split(*oscsend, ":")
		oscPort, _ := strconv.Atoi(foo[1])
		oscClient = osc.NewClient(foo[0], oscPort)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, message, errmsg := c.ReadMessage()
			if errmsg != nil {
				log.Debug("read:", errmsg)
				return
			}
			log.Debugf("recv: %s", message)
			// send message via osc
			if *oscsend != "" {
				msg := osc.NewMessage("/dust2dust")
				msg.Append(string(message))
				errSend := oscClient.Send(msg)
				if errSend != nil {
					log.Error(errSend)
				}
			}
		}
	}()

	// startup osc server
	if *oscrecv != "" {
		log.Debugf("setting up osc server at %s", *oscrecv)
		go func() {
			d := osc.NewStandardDispatcher()
			d.AddMsgHandler("/dust2dust", func(msg *osc.Message) {
				log.Debugf("local norns: %s", msg)
				msgString := msg.String()
				log.Debug(msgString)
				data := strings.Split(msgString, "/dust2dust ,s ")
				if len(data) == 2 {
					log.Debug(data[1])
					errWrite := c.WriteMessage(websocket.TextMessage, []byte(data[1]))
					if errWrite != nil {
						panic(errWrite)
					}
				}
			})

			server := &osc.Server{
				Addr:       *oscrecv,
				Dispatcher: d,
			}
			server.ListenAndServe()
		}()
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-interrupt:
			log.Debug("interrupt")

			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Debug("write close:", err)
				return
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}

	return
}

// norns server code

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 65534
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool { return true},
}

// connection is an middleman between the websocket connection and the hub.
type connection struct {
	// The websocket connection.
	ws   *websocket.Conn
	name string
	// Buffered channel of outbound messages.
	send chan []byte
}

// readPump pumps messages from the websocket connection to the hub.
func (s subscription) readPump() {
	c := s.conn
	defer func() {
		log.Debugf("unregistering %+v", s)
		h.unregister <- s
		c.ws.Close()
	}()
	c.ws.SetReadLimit(maxMessageSize)
	c.ws.SetReadDeadline(time.Now().Add(pongWait))
	c.ws.SetPongHandler(func(string) error { c.ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, msg, err := c.ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				log.Errorf("error: %v", err)
			}
			break
		}
		m := message{msg, s.room, s.name}
		log.Debug(m)
		h.broadcast <- m
	}
}

type message struct {
	data   []byte
	room   string
	origin string
}

type subscription struct {
	conn *connection
	room string
	name string
}

// hub maintains the set of active connections and broadcasts messages to the
// connections.
type hub struct {
	// Registered connections.
	rooms map[string]map[*connection]bool

	// Inbound messages from the connections.
	broadcast chan message

	// Register requests from the connections.
	register chan subscription

	// Unregister requests from connections.
	unregister chan subscription
}

var h = hub{
	broadcast:  make(chan message),
	register:   make(chan subscription),
	unregister: make(chan subscription),
	rooms:      make(map[string]map[*connection]bool),
}

func (h *hub) run() {
	for {
		select {
		case s := <-h.register:
			connections := h.rooms[s.room]
			if connections == nil {
				connections = make(map[*connection]bool)
				h.rooms[s.room] = connections
			}
			h.rooms[s.room][s.conn] = true
		case s := <-h.unregister:
			connections := h.rooms[s.room]
			if connections != nil {
				if _, ok := connections[s.conn]; ok {
					delete(connections, s.conn)
					close(s.conn.send)
					if len(connections) == 0 {
						delete(h.rooms, s.room)
					}
				}
			}
		case m := <-h.broadcast:
			connections := h.rooms[m.room]
			for c := range connections {
				if m.origin == c.name {
					continue
				}
				select {
				case c.send <- m.data:
				default:
					close(c.send)
					delete(connections, c)
					if len(connections) == 0 {
						delete(h.rooms, m.room)
					}
				}
			}
		}
	}
}

// write writes a message with the given message type and payload.
func (c *connection) write(mt int, payload []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(writeWait))
	return c.ws.WriteMessage(mt, payload)
}

// writePump pumps messages from the hub to the websocket connection.
func (s *subscription) writePump() {
	c := s.conn
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.ws.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.write(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.write(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.write(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

// serveWs handles websocket requests from the peer.
func serveWs(w http.ResponseWriter, r *http.Request) (err error) {
	ws, err := upgrader.Upgrade(w, r, nil)
	roomName := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(roomName) < 2 {
		err = fmt.Errorf("must return /room/name")
		return
	}
	if err != nil {
		log.Error(err)
		return
	}
	room := roomName[0]
	name := roomName[1]
	c := &connection{send: make(chan []byte, 256), ws: ws, name: name}
	s := subscription{c, room, name}
	log.Debugf("registering %+v", s)
	h.register <- s
	log.Debug("done registering")
	go s.writePump()
	s.readPump()
	return
}

func runServer() (err error) {
	go h.run()
	port := strings.Split(*addr, ":")
	log.Infof("listening on %s", port[1])
	http.HandleFunc("/", handler)
	return http.ListenAndServe(fmt.Sprintf(":%s", port[1]), nil)
}

func handler(w http.ResponseWriter, r *http.Request) {
	t := time.Now().UTC()
	err := handle(w, r)
	if err != nil {
		log.Error(err)
		w.Write([]byte(err.Error()))
	}
	log.Infof("%v %v %v %s\n", r.RemoteAddr, r.Method, r.URL.Path, time.Since(t))
}

func handle(w http.ResponseWriter, r *http.Request) (err error) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

	// very special paths
	if r.URL.Path == "/" {
		w.Write([]byte("ashes to ashes, dust to dust."))
	} else {
		err = serveWs(w, r)
		if err != nil {
			log.Debugf("ws: %w", err)
			err = nil
		}
	}

	return
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
const (
	letterIdxBits = 6                    // 6 bits to represent a letter index
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
)

func RandStringBytesMask(n int) string {
	b := make([]byte, n)
	for i := 0; i < n; {
		if idx := int(rand.Int63() & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i++
		}
	}
	return string(b)
}
