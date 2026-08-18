package memory

import (
	"context"
	"sync"

	"architecture/sdk/component"
)

// StateLedger is the reference implementation of component.StateLedger: a
// materialized projection held in memory. It is intentionally not
// durable — it is expected to be rebuildable by replaying the
// Observation Ledger, so the core has no reason to persist it itself.
type StateLedger struct {
	mu      sync.RWMutex
	current component.State
}

// NewStateLedger constructs a State Ledger seeded with the given state.
func NewStateLedger(initial component.State) *StateLedger {
	return &StateLedger{current: initial}
}

func (s *StateLedger) Current(_ context.Context) (component.State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current, nil
}

func (s *StateLedger) Commit(_ context.Context, st component.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = st
	return nil
}
