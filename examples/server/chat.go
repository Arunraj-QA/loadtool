package main

import (
	"context"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// chatBuffer is how many messages a chat client may fall behind before
// it loses messages.
const chatBuffer = 64

// chat is a WebSocket chat: every text message a client sends goes to
// every client in the same room (?room=, default "lobby"), the sender
// included, at once (no -delay). A client that does not keep up loses
// messages rather than slowing the room down.
type chat struct {
	mu    sync.Mutex
	rooms map[string]map[*chatClient]struct{}
}

type chatClient struct{ out chan []byte }

func newChat() *chat { return &chat{rooms: map[string]map[*chatClient]struct{}{}} }

func (h *chat) serve(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "lobby"
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has answered the request
	}
	defer c.CloseNow()
	cl := &chatClient{out: make(chan []byte, chatBuffer)}
	h.join(room, cl)
	defer h.leave(room, cl)

	// A hijacked connection's request context is not cancelled when the
	// client goes away; a failed read or write ends the connection.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The writer sends the room's messages to this client; it ends with
	// the connection, before serve returns.
	written := make(chan struct{})
	go func() {
		defer close(written)
		for {
			select {
			case m := <-cl.out:
				if c.Write(ctx, websocket.MessageText, m) != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			break
		}
		if typ == websocket.MessageText {
			h.broadcast(room, data)
		}
	}
	cancel()
	<-written
}

func (h *chat) join(room string, cl *chatClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[room] == nil {
		h.rooms[room] = map[*chatClient]struct{}{}
	}
	h.rooms[room][cl] = struct{}{}
}

func (h *chat) leave(room string, cl *chatClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.rooms[room], cl)
	if len(h.rooms[room]) == 0 {
		delete(h.rooms, room)
	}
}

// broadcast queues m for every client in room. Each read returns a new
// slice, so the clients may share it.
func (h *chat) broadcast(room string, m []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for cl := range h.rooms[room] {
		select {
		case cl.out <- m:
		default: // this client is behind: it loses m
		}
	}
}
