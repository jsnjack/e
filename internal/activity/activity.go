// Package activity provides a headless stream of background-operation events.
// Workers can publish without depending on GTK or waiting for the UI.
package activity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Kind identifies the part of E performing an operation.
type Kind string

const (
	// AI is model-backed completion and transformation work.
	AI Kind = "ai"
	// File is document loading and saving work.
	File Kind = "file"
	// Workspace is project discovery and indexing work.
	Workspace Kind = "workspace"
)

// Phase identifies an operation's lifecycle state.
type Phase uint8

const (
	Started Phase = iota
	Progressed
	Finished
	Failed
	Cancelled
)

// Event is a point-in-time update for one operation.
type Event struct {
	ID       uint64
	Kind     Kind
	Phase    Phase
	Label    string
	Current  int64
	Total    int64
	Note     string
	Model    string
	Fallback bool
	At       time.Time
}

// Hub fans activity events out to subscribers without blocking publishers.
type Hub struct {
	mu      sync.Mutex
	subs    map[uint64]chan Event
	nextID  atomic.Uint64
	nextSub uint64
}

// NewHub creates an empty activity hub.
func NewHub() *Hub { return &Hub{subs: make(map[uint64]chan Event)} }

// Begin publishes the start of an operation and returns its lifecycle handle.
func (h *Hub) Begin(kind Kind, label string) *Operation {
	if h == nil {
		return &Operation{}
	}
	op := &Operation{hub: h, id: h.nextID.Add(1), kind: kind, label: label}
	h.publish(Event{ID: op.id, Kind: kind, Phase: Started, Label: label, At: time.Now()})
	return op
}

// Report publishes a completed operation which was too quick to report at its start.
func (h *Hub) Report(kind Kind, label, note string) {
	if h == nil {
		return
	}
	h.publish(Event{
		ID: h.nextID.Add(1), Kind: kind, Phase: Finished, Label: label,
		Note: note, At: time.Now(),
	})
}

// Subscribe returns a buffered event stream and an idempotent unsubscribe function.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	if h == nil {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	h.mu.Lock()
	id := h.nextSub
	h.nextSub++
	ch := make(chan Event, 64)
	h.subs[id] = ch
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if existing, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(existing)
			}
			h.mu.Unlock()
		})
	}
}

func (h *Hub) publish(event Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- event:
		default:
			// Activity is advisory: UI backpressure must never stall a worker.
		}
	}
}

// Operation publishes progress and exactly one terminal event for work begun by Hub.Begin.
type Operation struct {
	hub   *Hub
	id    uint64
	kind  Kind
	label string
	done  atomic.Bool
}

// Progress publishes bounded progress. Invalid or late progress is ignored.
func (o *Operation) Progress(current, total int64) {
	if o == nil || o.hub == nil || o.done.Load() || total <= 0 || current < 0 {
		return
	}
	o.hub.publish(Event{
		ID: o.id, Kind: o.kind, Phase: Progressed, Label: o.label,
		Current: min(current, total), Total: total, At: time.Now(),
	})
}

// Finish publishes a successful result and the model that served it, when applicable.
func (o *Operation) Finish(note, model string, fallback bool) {
	o.finish(Finished, note, model, fallback)
}

// Fail publishes a failed result. Context cancellation is treated as cancellation.
func (o *Operation) Fail(err error) {
	if errorsIsCancellation(err) {
		o.Cancel()
		return
	}
	note := "Operation failed"
	if err != nil {
		note = err.Error()
	}
	o.finish(Failed, note, "", false)
}

// Cancel removes an operation from the running surface without presenting it as an error.
func (o *Operation) Cancel() { o.finish(Cancelled, "", "", false) }

func (o *Operation) finish(phase Phase, note, model string, fallback bool) {
	if o == nil || o.hub == nil || !o.done.CompareAndSwap(false, true) {
		return
	}
	o.hub.publish(Event{
		ID: o.id, Kind: o.kind, Phase: phase, Label: o.label, Note: note,
		Model: model, Fallback: fallback, At: time.Now(),
	})
}

func errorsIsCancellation(err error) bool {
	return errors.Is(err, context.Canceled)
}

// Plural formats an integer with its singular or plural noun.
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}
