// relay_client.go
package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Relay envelope used on the websocket relay
type RelayEnvelope struct {
	Type    string          `json:"type"`           // "register"|"relay"|"response"|"error"
	From    string          `json:"from,omitempty"` // sender peer id string
	To      string          `json:"to,omitempty"`   // receiver peer id string
	Payload json.RawMessage `json:"payload,omitempty"`
	Msg     string          `json:"message,omitempty"`
}

// RelayClient holds the WS connection to the relay server
type RelayClient struct {
	url         string
	peerID      string // host.ID().String()
	conn        *websocket.Conn
	mu          sync.RWMutex
	recvHandler func(from peer.ID, payload []byte) ([]byte, error) // process incoming requests and optionally return response bytes
	closeC      chan struct{}
}

// NewRelayClient creates and starts the background receiver
func NewRelayClient(url string, myPeerID string, handler func(from peer.ID, payload []byte) ([]byte, error)) *RelayClient {
	rc := &RelayClient{
		url:         url,
		peerID:      myPeerID,
		recvHandler: handler,
		closeC:      make(chan struct{}),
	}
	go rc.backgroundConnect()
	return rc
}

// backgroundConnect: connects, registers, reads messages and reconnects if needed
func (r *RelayClient) backgroundConnect() {
	for {
		select {
		case <-r.closeC:
			return
		default:
		}

		conn, _, err := websocket.DefaultDialer.Dial(r.url, nil)
		if err != nil {
			log.Printf("Relay: failed to dial %s: %v. Retrying in 3s...", r.url, err)
			time.Sleep(3 * time.Second)
			continue
		}

		r.mu.Lock()
		r.conn = conn
		r.mu.Unlock()

		// Register
		reg := RelayEnvelope{Type: "register", From: r.peerID}
		if err := conn.WriteJSON(reg); err != nil {
			log.Printf("Relay: register failed: %v", err)
			conn.Close()
			time.Sleep(2 * time.Second)
			continue
		}

		log.Printf("Relay: connected & registered as %s", r.peerID)

		// Read loop
		readErr := r.readLoop(conn)

		log.Printf("Relay: connection closed (%v). reconnecting...", readErr)
		r.mu.Lock()
		if r.conn == conn {
			r.conn = nil
		}
		r.mu.Unlock()
		conn.Close()
		time.Sleep(2 * time.Second)
	}
}

func (r *RelayClient) readLoop(conn *websocket.Conn) error {
	for {
		var env RelayEnvelope
		if err := conn.ReadJSON(&env); err != nil {
			return err
		}
		switch env.Type {
		case "relay":
			// payload contains the original application JSON payload
			go r.handleInboundRelay(env)
		case "error":
			log.Printf("Relay server error: %s", env.Msg)
		default:
			log.Printf("Relay: unknown envelope type: %s", env.Type)
		}
	}
}

func (r *RelayClient) handleInboundRelay(env RelayEnvelope) {
	if r.recvHandler == nil {
		log.Println("Relay: no recvHandler, ignoring inbound relay")
		return
	}
	fromPeer, err := peer.Decode(env.From)
	if err != nil {
		log.Printf("Relay: invalid from peer id: %s", env.From)
		return
	}

	// Call the application handler which returns optional response bytes
	resp, err := r.recvHandler(fromPeer, env.Payload)
	if err != nil {
		// send an error envelope back
		r.SendError(env.From, err.Error())
		return
	}
	if resp != nil {
		// send response envelope back to origin
		_ = r.Send(env.From, resp)
	}
}

// Send a payload to a specific peer via relay. payload should be the raw JSON bytes you would have written to a stream.
func (r *RelayClient) Send(to string, payload []byte) error {
	r.mu.RLock()
	conn := r.conn
	r.mu.RUnlock()

	if conn == nil {
		return errors.New("not connected to relay")
	}

	env := RelayEnvelope{
		Type:    "relay",
		From:    r.peerID,
		To:      to,
		Payload: json.RawMessage(payload),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		r.mu.Lock() // full write lock
		defer r.mu.Unlock()

		if r.conn == nil {
			done <- errors.New("no conn")
			return
		}
		done <- r.conn.WriteJSON(env)
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("timeout writing to relay")
	case err := <-done:
		return err
	}
}


func (r *RelayClient) SendError(to string, msg string) {
	r.mu.RLock()
	conn := r.conn
	r.mu.RUnlock()
	if conn == nil {
		return
	}
	env := RelayEnvelope{
		Type: "error",
		From: r.peerID,
		To:   to,
		Msg:  msg,
	}
	r.mu.Lock()
	_ = conn.WriteJSON(env)
	r.mu.Unlock()
}

func (r *RelayClient) Close() {
	close(r.closeC)
	r.mu.Lock()
	if r.conn != nil {
		r.conn.Close()
		r.conn = nil
	}
	r.mu.Unlock()
}
