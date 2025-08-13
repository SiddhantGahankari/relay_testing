// ws_relay_server.go
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// Use the same envelope structure as relay_client.go
type RelayEnvelope struct {
	Type    string          `json:"type"`           // "register"|"relay"|"response"|"error"
	From    string          `json:"from,omitempty"` // sender peer id string
	To      string          `json:"to,omitempty"`   // receiver peer id string
	Payload json.RawMessage `json:"payload,omitempty"`
	Msg     string          `json:"message,omitempty"`
}

type PeerConn struct {
	id   string
	conn *websocket.Conn
	mu   sync.Mutex
}

type Server struct {
	mu    sync.RWMutex
	peers map[string]*PeerConn
}

func NewServer() *Server {
	return &Server{peers: make(map[string]*PeerConn)}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil) // upgrades http to ws
	if err != nil {
		log.Printf("upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Expect a register envelope first
	var env RelayEnvelope
	if err := conn.ReadJSON(&env); err != nil || env.Type != "register" || env.From == "" {
		conn.WriteJSON(RelayEnvelope{Type: "error", Msg: "must register with type=register and From field"})
		return
	}

	pc := &PeerConn{id: env.From, conn: conn}
	s.mu.Lock()
	s.peers[env.From] = pc
	s.mu.Unlock()
	log.Printf("registered peer %s", env.From)

	// Send registration confirmation
	pc.mu.Lock()
	_ = conn.WriteJSON(RelayEnvelope{Type: "response", Msg: "registered successfully"})
	pc.mu.Unlock()

	// Read loop
	for {
		var e RelayEnvelope
		if err := conn.ReadJSON(&e); err != nil {
			log.Printf("read error from %s: %v", pc.id, err)
			break
		}

		// Set From field if not present
		if e.From == "" {
			e.From = pc.id
		}

		switch e.Type {
		case "relay":
			if e.To == "" {
				pc.mu.Lock()
				_ = conn.WriteJSON(RelayEnvelope{Type: "error", Msg: "relay requires To field"})
				pc.mu.Unlock()
				continue
			}

			// Find destination peer
			s.mu.RLock()
			dst, ok := s.peers[e.To]
			s.mu.RUnlock()

			if !ok {
				// Notify sender that destination is not online
				pc.mu.Lock()
				_ = conn.WriteJSON(RelayEnvelope{
					Type: "error",
					From: "relay-server",
					To:   pc.id,
					Msg:  "target peer not online: " + e.To,
				})
				pc.mu.Unlock()
				continue
			}

			// Forward to destination
			dst.mu.Lock()
			err := dst.conn.WriteJSON(e)
			dst.mu.Unlock()

			if err != nil {
				log.Printf("failed to forward message to %s: %v", e.To, err)
				// Remove dead connection
				s.mu.Lock()
				delete(s.peers, dst.id)
				s.mu.Unlock()
				dst.conn.Close()
			} else {
				log.Printf("relayed message from %s to %s", e.From, e.To)
			}

		case "error":
			log.Printf("relay error from %s: %s", e.From, e.Msg)

		case "ping":
			// Respond to ping
			pc.mu.Lock()
			_ = conn.WriteJSON(RelayEnvelope{Type: "pong", From: "relay-server", To: pc.id})
			pc.mu.Unlock()

		default:
			log.Printf("unknown envelope type from %s: %s", pc.id, e.Type)
			pc.mu.Lock()
			_ = conn.WriteJSON(RelayEnvelope{Type: "error", Msg: "unknown envelope type: " + e.Type})
			pc.mu.Unlock()
		}
	}

	// Cleanup on disconnect
	s.mu.Lock()
	delete(s.peers, pc.id)
	s.mu.Unlock()
	log.Printf("unregistered peer %s", pc.id)
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	count := len(s.peers)
	peerList := make([]string, 0, count)
	for id := range s.peers {
		peerList = append(peerList, id)
	}
	s.mu.RUnlock()

	stats := map[string]interface{}{
		"connected_peers": count,
		"peer_ids":        peerList,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func main() {
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080" 
    }

    server := NewServer()
    http.HandleFunc("/ws", server.handleWS)
    http.HandleFunc("/stats", server.getStats)

    log.Printf("relay server listening on 0.0.0.0:%s", port)
    log.Printf("WebSocket endpoint: wss://<your-render-app>.onrender.com/ws")
    log.Printf("Stats endpoint: https://<your-render-app>.onrender.com/stats")

    log.Fatal(http.ListenAndServe(":"+port, nil))
}
