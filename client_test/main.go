package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"relay/helper" 

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

type Envelope struct {
	Type    string `json:"type"`
	From    string `json:"from"`
	To      string `json:"to"`
	Payload string `json:"payload"`
}

func generatePeerID() (peer.ID, string) {
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		log.Fatalf("Failed to generate peer ID: %v", err)
	}
	id, err := peer.IDFromPrivateKey(priv)
	if err != nil {
		log.Fatalf("Failed to get peer ID: %v", err)
	}
	return id, id.String()
}

func main() {
	relayURL := "ws://0.0.0.0:8080/ws"

	_, peerAID := generatePeerID()
	_, peerBID := generatePeerID()

	handlerA := func(from peer.ID, payload []byte) ([]byte, error) {
		log.Printf("[PeerA] Received from %s: %s", from, string(payload))
		reply := Envelope{
			Type:    "message",
			From:    peerAID,
			To:      from.String(),
			Payload: fmt.Sprintf("ACK from PeerA to %s", from),
		}
		return json.Marshal(reply)
	}

	handlerB := func(from peer.ID, payload []byte) ([]byte, error) {
		log.Printf("[PeerB] Received from %s: %s", from, string(payload))
		reply := Envelope{
			Type:    "message",
			From:    peerBID,
			To:      from.String(),
			Payload: fmt.Sprintf("ACK from PeerB to %s", from),
		}
		return json.Marshal(reply)
	}

	clientA := helper.NewRelayClient(relayURL, peerAID, handlerA)
	clientB := helper.NewRelayClient(relayURL, peerBID, handlerB)

	time.Sleep(2 * time.Second)

	msg := Envelope{
		Type:    "message",
		From:    peerAID,
		To:      peerBID,
		Payload: "Hello from A",
	}
	msgBytes, _ := json.Marshal(msg)
	if err := clientA.Send(peerBID, msgBytes); err != nil {
		log.Printf("Send from A to B failed: %v", err)
	}

	msg2 := Envelope{
		Type:    "message",
		From:    peerBID,
		To:      peerAID,
		Payload: "Hello from B",
	}
	msgBytes2, _ := json.Marshal(msg2)
	if err := clientB.Send(peerAID, msgBytes2); err != nil {
		log.Printf("Send from B to A failed: %v", err)
	}

	time.Sleep(3 * time.Second)

	log.Println("Closing clients...")
	clientA.Close()
	clientB.Close()

	time.Sleep(1 * time.Second)
}
