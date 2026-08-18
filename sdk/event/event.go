// Package event provides the in-process event bus that decouples every
// component of the architecture. It has no external dependencies: the bus
// is backed entirely by Go channels.
package event

import (
	"sync"
	"sync/atomic"
	"time"
)

// Type identifies the kind of event flowing across the bus.
type Type string

// Event is the unit of communication between components. Payload carries
// type-specific data (observations, beliefs, actions, states, rewards, ...).
type Event struct {
	Seq     uint64
	Type    Type
	Source  string
	Time    time.Time
	Payload any
}

// Bus is the contract every component depends on to publish and observe
// events. It is intentionally minimal so alternative implementations
// (e.g. an adapter backed by an external broker) can satisfy it without the
// core ever depending on them.
type Bus interface {
	// Publish sends an event to every current subscriber. It never blocks
	// the publisher on a slow subscriber.
	Publish(evt Event)
	// Subscribe returns a channel of events matching any of the given
	// types (all events if no types are given), and an unsubscribe func.
	Subscribe(types ...Type) (<-chan Event, func())
}

// ChannelBus is the zero-dependency default Bus implementation: in-process
// fan-out over buffered Go channels.
type ChannelBus struct {
	mu     sync.RWMutex
	subs   map[int]*subscription
	nextID int
	seq    atomic.Uint64
}

type subscription struct {
	types map[Type]bool // nil/empty means "all types"
	ch    chan Event
}

// NewChannelBus constructs a ready-to-use in-process event bus.
func NewChannelBus() *ChannelBus {
	return &ChannelBus{subs: make(map[int]*subscription)}
}

// Publish implements Bus. Delivery is best-effort and non-blocking: a
// subscriber whose buffer is full drops the event rather than stalling the
// publisher, since the Observation Ledger (not the bus) is the durable
// record of what happened.
func (b *ChannelBus) Publish(evt Event) {
	if evt.Time.IsZero() {
		evt.Time = time.Now()
	}
	evt.Seq = b.seq.Add(1)

	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs {
		if len(sub.types) > 0 && !sub.types[evt.Type] {
			continue
		}
		select {
		case sub.ch <- evt:
		default:
		}
	}
}

// Subscribe implements Bus.
func (b *ChannelBus) Subscribe(types ...Type) (<-chan Event, func()) {
	filter := make(map[Type]bool, len(types))
	for _, t := range types {
		filter[t] = true
	}

	b.mu.Lock()
	id := b.nextID
	b.nextID++
	sub := &subscription{types: filter, ch: make(chan Event, 256)}
	b.subs[id] = sub
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(s.ch)
		}
	}
	return sub.ch, unsubscribe
}
