// Package stream is an in-process pub/sub used for live deployment logs and
// UI events (delivered to browsers via Server-Sent Events).
package stream

import (
	"sync"
)

type Message struct {
	Topic string `json:"topic"`
	Type  string `json:"type"` // log | status | event
	Data  any    `json:"data"`
}

type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan Message]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[string]map[chan Message]struct{}{}} }

// Subscribe returns a channel receiving messages for topic, and an unsubscribe func.
func (h *Hub) Subscribe(topic string) (<-chan Message, func()) {
	ch := make(chan Message, 512)
	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[chan Message]struct{}{}
	}
	h.subs[topic][ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs[topic], ch)
			if len(h.subs[topic]) == 0 {
				delete(h.subs, topic)
			}
			h.mu.Unlock()
			close(ch)
		})
	}
}

// Publish delivers msg to all subscribers; slow subscribers drop messages.
func (h *Hub) Publish(topic, typ string, data any) {
	msg := Message{Topic: topic, Type: typ, Data: data}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[topic] {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Event broadcasts a UI invalidation event (resource changed).
func (h *Hub) Event(kind string, id int64, status string) {
	h.Publish("events", "event", map[string]any{"kind": kind, "id": id, "status": status})
}
