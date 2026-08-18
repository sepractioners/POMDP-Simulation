package memory

import "sync"

// ActionRegistry is the reference implementation of
// component.ActionRegistry: an in-memory map of action name to
// handler-specific metadata, preserving registration order so that
// List() is deterministic — consumers doing ordered search (e.g. the
// Orchestrator's Tree/Graph patterns) depend on that.
type ActionRegistry struct {
	mu    sync.RWMutex
	items map[string]any
	order []string
}

// NewActionRegistry constructs an empty in-memory registry.
func NewActionRegistry() *ActionRegistry {
	return &ActionRegistry{items: make(map[string]any)}
}

func (r *ActionRegistry) Register(name string, meta any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[name]; !exists {
		r.order = append(r.order, name)
	}
	r.items[name] = meta
}

func (r *ActionRegistry) Get(name string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.items[name]
	return v, ok
}

func (r *ActionRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}
