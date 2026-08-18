package component

import "context"

// ObservationGateway resolves how raw signals enter the system: it is the
// single ingress point between the environment and everything downstream.
type ObservationGateway interface {
	Observe(ctx context.Context) (Observation, error)
}

// ObservationLedger resolves the record-and-replay contradiction: it is
// the append-only, durable-by-adapter source of truth for everything the
// agent has ever observed. State Ledger is rebuildable from this log.
type ObservationLedger interface {
	Append(ctx context.Context, obs Observation) error
	All(ctx context.Context) ([]Observation, error)
	Since(ctx context.Context, seq uint64) ([]Observation, error)
}

// BeliefEngine resolves the gap between a raw observation and a usable
// estimate of the true state, folding a new observation into the prior
// belief.
type BeliefEngine interface {
	Update(ctx context.Context, prior Belief, obs Observation) (Belief, error)
}

// PolicyEngine resolves what to do given the current belief: it selects
// the next action.
type PolicyEngine interface {
	Decide(ctx context.Context, goal Goal, belief Belief) (Action, error)
}

// ActionRegistry resolves what actions exist and are valid to select. It
// is consulted by the Policy Engine and the Orchestrator, not executed
// directly.
type ActionRegistry interface {
	Register(name string, meta any)
	Get(name string) (any, bool)
	List() []string
}

// TransitionEngine resolves how state changes in response to an action:
// given the current State and a selected Action, it produces the next
// State. In a simulated demo this models the environment directly; against
// a real environment it typically models the *expected* effect, with the
// Actuator Gateway carrying out the real one.
type TransitionEngine interface {
	Apply(ctx context.Context, current State, action Action) (State, error)
}

// StateLedger resolves fast access to "current" state: a materialized,
// rebuildable projection over the Observation Ledger.
type StateLedger interface {
	Current(ctx context.Context) (State, error)
	Commit(ctx context.Context, s State) error
}

// ActuatorGateway resolves the boundary between a decision and its
// real-world (or simulated) effect: it is the single egress point for
// actions leaving the system.
type ActuatorGateway interface {
	Execute(ctx context.Context, action Action) error
}

// RewardEvaluator resolves the feedback/scoring contradiction: given a
// state transition, it produces the learning/evaluation signal.
type RewardEvaluator interface {
	Evaluate(ctx context.Context, prev State, action Action, next State) (Reward, error)
}
