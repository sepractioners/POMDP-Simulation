// Package component defines the contracts for the nine components of the
// architecture. Each interface is deliberately small and resolves exactly
// one contradiction in the POMDP-style model. The core module ships
// zero-dependency, in-process default implementations (see the memory
// subpackage and the orchestrator's wiring); consumers may implement any
// interface against an external backend without the core ever depending
// on one.
package component

import "time"

// Observation is a single raw signal ingested from the environment.
type Observation struct {
	Seq  uint64
	Time time.Time
	Data any
}

// Belief is the agent's current estimate of the true state, derived from
// the observation history. It is distinct from State: Belief is what the
// agent thinks is true, State is what the architecture has committed to
// as the current materialized value driving the next decision.
type Belief struct {
	Time time.Time
	Data any
}

// Action is a candidate or selected action, identified by Name and
// carrying handler-specific parameters in Params.
type Action struct {
	Name   string
	Params any
}

// State is the materialized projection maintained by the State Ledger. It
// is a derived/rebuildable view over the Observation Ledger, not itself
// the source of truth.
type State struct {
	Seq  uint64
	Time time.Time
	Data any
}

// Reward is the feedback signal produced after a transition.
type Reward struct {
	Time  time.Time
	Value float64
	Info  any
}

// Goal describes what the Orchestrator is trying to achieve.
type Goal struct {
	ID   string
	Data any
}
