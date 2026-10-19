package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

//go:embed viewer.html
var viewerHTML []byte

type webMessage struct {
	binary bool
	data   []byte
}

type webClient struct {
	send               chan webMessage
	waitingForKeyframe bool
}

type webServer struct {
	mu       sync.Mutex
	clients  map[*webClient]struct{}
	upgrader websocket.Upgrader

	onMessage func(map[string]any)
	greeting  func() []byte
}

func newWebServer() *webServer {
	return &webServer{
		clients: map[*webClient]struct{}{},
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (s *webServer) listen(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(viewerHTML)
	})
	mux.HandleFunc("/ws", s.serveWebSocket)
	mux.Handle("/frames/", http.StripPrefix("/frames/", http.FileServer(http.Dir(framesDir()))))
	return http.ListenAndServe(addr, mux)
}

func (s *webServer) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &webClient{send: make(chan webMessage, 64), waitingForKeyframe: true}

	s.mu.Lock()
	s.clients[client] = struct{}{}
	s.mu.Unlock()

	if s.greeting != nil {
		client.send <- webMessage{data: s.greeting()}
	}

	go func() {
		for message := range client.send {
			kind := websocket.TextMessage
			if message.binary {
				kind = websocket.BinaryMessage
			}
			if conn.WriteMessage(kind, message.data) != nil {
				break
			}
		}
		_ = conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var message map[string]any
		if json.Unmarshal(data, &message) == nil && s.onMessage != nil {
			s.onMessage(message)
		}
	}

	s.mu.Lock()
	delete(s.clients, client)
	s.mu.Unlock()
	close(client.send)
}

func (s *webServer) sendVideo(accessUnit []byte, flags byte) {
	keyframe := flags&1 == 1

	s.mu.Lock()
	defer s.mu.Unlock()
	for client := range s.clients {
		message := make([]byte, 1+len(accessUnit))
		message[0] = flags
		copy(message[1:], accessUnit)

		if client.waitingForKeyframe {
			if !keyframe {
				continue
			}
			client.waitingForKeyframe = false
			message[0] |= 2
		}
		select {
		case client.send <- webMessage{binary: true, data: message}:
		default:
			client.waitingForKeyframe = true
		}
	}
}

func (s *webServer) sendText(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for client := range s.clients {
		select {
		case client.send <- webMessage{data: data}:
		default:
		}
	}
}
