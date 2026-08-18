// Package memory provides the zero-dependency, in-process default
// implementations of the storage-shaped components: Observation Ledger,
// State Ledger, and Action Registry. They hold everything in memory and
// are protected by a mutex; nothing here talks to an external process.
package memory

import (
	"context"
	"sync"

	"architecture/sdk/component"
)

// ObservationLedger is an append-only, in-memory log. It is the reference
// implementation of component.ObservationLedger: the source of truth a
// State Ledger projection can be replayed from.
type ObservationLedger struct {
	mu  sync.RWMutex
	log []component.Observation
}

// NewObservationLedger constructs an empty in-memory ledger.
func NewObservationLedger() *ObservationLedger {
	return &ObservationLedger{}
}

func (l *ObservationLedger) Append(_ context.Context, obs component.Observation) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.log = append(l.log, obs)
	return nil
}

func (l *ObservationLedger) All(_ context.Context) ([]component.Observation, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]component.Observation, len(l.log))
	copy(out, l.log)
	return out, nil
}

func (l *ObservationLedger) Since(_ context.Context, seq uint64) ([]component.Observation, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []component.Observation
	for _, o := range l.log {
		if o.Seq > seq {
			out = append(out, o)
		}
	}
	return out, nil
}
