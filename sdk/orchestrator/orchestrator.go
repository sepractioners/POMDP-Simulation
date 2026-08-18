// Package orchestrator owns the goal, the navigation pattern, and
// backtracking, and drives the nine components through the event bus. It
// is the only component that runs single-process by requirement (see
// package doc in component): distributing it would need a coordination
// layer, which this SDK does not depend on.
package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"architecture/sdk/component"
	"architecture/sdk/event"
)

// Pattern selects how the Orchestrator navigates toward the goal.
type Pattern string

const (
	// PatternLinear walks straight ahead: it asks the Policy Engine for
	// one action per step and, on failure, only retries that same step
	// (see Config.MaxStepRetries) — no re-navigation.
	PatternLinear Pattern = "linear"

	// PatternTree plans ahead of any real action: it searches the
	// Transition Engine's model with depth-first search, backtracking to
	// an earlier simulated state whenever a branch dead-ends, until it
	// finds a path to the goal. Only once a full path is found does it
	// execute those actions for real, one per step. Cycles are avoided
	// only along the current path, so the same state may be revisited by
	// a different branch.
	PatternTree Pattern = "tree"

	// PatternGraph is PatternTree with a search-wide visited set instead
	// of a per-path one: once a state has been explored by any branch it
	// is never explored again. This is a plain DFS-with-memoization, not
	// a shortest-path search (no Dijkstra/A*) — a starting point, not the
	// final word on graph navigation.
	PatternGraph Pattern = "graph"
)

// Event types published on the bus over the course of a run.
const (
	EventStepStarted    event.Type = "orchestrator.step_started"
	EventObservation    event.Type = "orchestrator.observation"
	EventBeliefUpdated  event.Type = "orchestrator.belief_updated"
	EventActionSelected event.Type = "orchestrator.action_selected"
	EventStateCommitted event.Type = "orchestrator.state_committed"
	EventReward         event.Type = "orchestrator.reward"
	EventGoalReached    event.Type = "orchestrator.goal_reached"
	EventStepError      event.Type = "orchestrator.step_error"
	EventRunFinished    event.Type = "orchestrator.run_finished"

	// Planning-phase events, published only by Tree/Graph patterns before
	// any real action is taken.
	EventPlanStarted   event.Type = "orchestrator.plan_started"
	EventPlanExpanded  event.Type = "orchestrator.plan_expanded"
	EventPlanBacktrack event.Type = "orchestrator.plan_backtrack"
	EventPlanFound     event.Type = "orchestrator.plan_found"
)

// Config wires the nine components and the bus into an Orchestrator.
type Config struct {
	Bus      event.Bus
	Goal     component.Goal
	Pattern  Pattern
	MaxSteps int

	Gateway    component.ObservationGateway
	ObsLedger  component.ObservationLedger
	Belief     component.BeliefEngine
	Policy     component.PolicyEngine
	Actions    component.ActionRegistry
	Transition component.TransitionEngine
	StateL     component.StateLedger
	Actuator   component.ActuatorGateway
	Reward     component.RewardEvaluator

	// IsGoalReached lets the demo/consumer decide when the goal has been
	// met. It is not one of the nine components: deciding when the goal
	// is satisfied is the Orchestrator's own responsibility, since the
	// Orchestrator is the layer that owns the goal.
	IsGoalReached func(component.State) bool

	// MaxStepRetries bounds PatternLinear's naive retry: on a step error,
	// retry the same step (same action) up to this many times before
	// aborting the run. It is not backtracking — it never revisits an
	// earlier state or tries a different action. Use PatternTree/Graph
	// for real backtracking.
	MaxStepRetries int

	// MaxPlanNodes bounds the search performed by PatternTree/Graph
	// before giving up (default 5000).
	MaxPlanNodes int
}

// Orchestrator drives one run of the architecture's decision loop.
type Orchestrator struct {
	cfg Config
}

// New constructs an Orchestrator from cfg, applying sane defaults for
// unset optional fields.
func New(cfg Config) *Orchestrator {
	if cfg.Pattern == "" {
		cfg.Pattern = PatternLinear
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 1000
	}
	if cfg.MaxPlanNodes <= 0 {
		cfg.MaxPlanNodes = 5000
	}
	return &Orchestrator{cfg: cfg}
}

// Run executes the configured pattern until the goal is reached, the step
// budget is exhausted, or ctx is cancelled.
func (o *Orchestrator) Run(ctx context.Context) error {
	switch o.cfg.Pattern {
	case PatternLinear:
		return o.runLinear(ctx)
	case PatternTree:
		return o.runPlanned(ctx, false)
	case PatternGraph:
		return o.runPlanned(ctx, true)
	default:
		return fmt.Errorf("orchestrator: unsupported pattern %q", o.cfg.Pattern)
	}
}

// runLinear asks the Policy Engine for one action per step and commits it
// immediately; the only recovery on failure is retrying the same step.
func (o *Orchestrator) runLinear(ctx context.Context) error {
	c := o.cfg
	defer c.Bus.Publish(event.Event{Type: EventRunFinished, Source: "orchestrator"})

	var belief component.Belief
	for step := 0; step < c.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.Bus.Publish(event.Event{Type: EventStepStarted, Source: "orchestrator", Payload: step})

		var res stepResult
		var stepErr error
		for attempt := 0; attempt <= c.MaxStepRetries; attempt++ {
			res, stepErr = o.step(ctx, belief, nil)
			if stepErr == nil {
				break
			}
		}
		if stepErr != nil {
			c.Bus.Publish(event.Event{Type: EventStepError, Source: "orchestrator", Payload: stepErr.Error()})
			return stepErr
		}
		belief = res.belief

		if c.IsGoalReached != nil && c.IsGoalReached(res.next) {
			c.Bus.Publish(event.Event{Type: EventGoalReached, Source: "orchestrator", Payload: res.next})
			return nil
		}
	}
	return errors.New("orchestrator: max steps reached without satisfying goal")
}

// runPlanned implements PatternTree (global=false) and PatternGraph
// (global=true): it plans a full path to the goal against the Transition
// Engine's model first — including any backtracking — and only then
// executes that path for real, one action at a time. Real actions are
// never undone; only simulated planning states are backtracked over.
func (o *Orchestrator) runPlanned(ctx context.Context, global bool) error {
	c := o.cfg
	defer c.Bus.Publish(event.Event{Type: EventRunFinished, Source: "orchestrator"})

	root, err := c.StateL.Current(ctx)
	if err != nil {
		return err
	}

	plan, err := o.plan(ctx, root, global)
	if err != nil {
		return err
	}

	var belief component.Belief
	for i, action := range plan {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.Bus.Publish(event.Event{Type: EventStepStarted, Source: "orchestrator", Payload: i})

		forced := action
		res, err := o.step(ctx, belief, &forced)
		if err != nil {
			c.Bus.Publish(event.Event{Type: EventStepError, Source: "orchestrator", Payload: err.Error()})
			return err
		}
		belief = res.belief

		if c.IsGoalReached != nil && c.IsGoalReached(res.next) {
			c.Bus.Publish(event.Event{Type: EventGoalReached, Source: "orchestrator", Payload: res.next})
			return nil
		}
	}

	if c.IsGoalReached != nil && c.IsGoalReached(root) {
		c.Bus.Publish(event.Event{Type: EventGoalReached, Source: "orchestrator", Payload: root})
		return nil
	}
	return errors.New("orchestrator: planned path did not reach goal")
}

// planNode is one simulated state in the search tree/graph.
type planNode struct {
	state component.State
	tried map[string]bool
}

// stateKey gives simulated states a comparable identity for cycle
// detection. component.State.Data is intentionally `any` (consumer-owned
// domain data), so this is a generic, dependency-free fallback rather
// than a typed comparison the core cannot know about.
func stateKey(s component.State) string {
	return fmt.Sprintf("%v", s.Data)
}

// plan performs depth-first search over the Transition Engine's model,
// starting at root, backtracking whenever a node runs out of untried,
// non-cycling, valid actions. It never calls the Observation Gateway,
// Belief Engine, or Actuator Gateway — those model *sensing* and
// *effecting* the real world, neither of which a hypothetical lookahead
// should touch.
func (o *Orchestrator) plan(ctx context.Context, root component.State, global bool) ([]component.Action, error) {
	c := o.cfg
	if c.IsGoalReached != nil && c.IsGoalReached(root) {
		return nil, nil
	}

	c.Bus.Publish(event.Event{Type: EventPlanStarted, Source: "orchestrator", Payload: root})

	stack := []*planNode{{state: root, tried: map[string]bool{}}}
	path := make([]component.Action, 0)
	visited := map[string]bool{stateKey(root): true}

	for expansions := 0; expansions < c.MaxPlanNodes; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]

		action, next, ok := o.nextExpansion(ctx, top, stack, global, visited)
		if !ok {
			stack = stack[:len(stack)-1]
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
			c.Bus.Publish(event.Event{Type: EventPlanBacktrack, Source: "orchestrator", Payload: top.state})
			if len(stack) == 0 {
				return nil, errors.New("orchestrator: no path to goal found")
			}
			continue
		}

		expansions++
		top.tried[action.Name] = true
		path = append(path, action)
		stack = append(stack, &planNode{state: next, tried: map[string]bool{}})
		if global {
			visited[stateKey(next)] = true
		}
		c.Bus.Publish(event.Event{Type: EventPlanExpanded, Source: "orchestrator", Payload: map[string]any{"action": action, "state": next}})

		if c.IsGoalReached != nil && c.IsGoalReached(next) {
			found := make([]component.Action, len(path))
			copy(found, path)
			c.Bus.Publish(event.Event{Type: EventPlanFound, Source: "orchestrator", Payload: found})
			return found, nil
		}
	}
	return nil, fmt.Errorf("orchestrator: plan search exceeded %d node budget", c.MaxPlanNodes)
}

// nextExpansion returns the next untried action from node whose predicted
// result (per the Transition Engine) is both valid and not a cycle: for
// PatternTree that means not equal to any ancestor currently on the
// stack, for PatternGraph not equal to any state visited by any branch.
func (o *Orchestrator) nextExpansion(ctx context.Context, node *planNode, stack []*planNode, global bool, visited map[string]bool) (component.Action, component.State, bool) {
	c := o.cfg
	for _, name := range c.Actions.List() {
		if node.tried[name] {
			continue
		}
		meta, _ := c.Actions.Get(name)
		action := component.Action{Name: name, Params: meta}

		next, err := c.Transition.Apply(ctx, node.state, action)
		if err != nil {
			node.tried[name] = true
			continue
		}
		key := stateKey(next)
		if global {
			if visited[key] {
				node.tried[name] = true
				continue
			}
		} else {
			cyclic := false
			for _, anc := range stack {
				if stateKey(anc.state) == key {
					cyclic = true
					break
				}
			}
			if cyclic {
				node.tried[name] = true
				continue
			}
		}
		return action, next, true
	}
	return component.Action{}, component.State{}, false
}

// stepResult carries everything one turn of the loop produced, so callers
// (linear vs. planned patterns) can extract what they need.
type stepResult struct {
	belief component.Belief
	action component.Action
	prev   component.State
	next   component.State
	reward component.Reward
}

// step runs one full turn of Observe -> Ledger -> Belief -> [Policy or a
// forced action] -> Transition -> Actuate -> Commit -> Reward, publishing
// an event after each stage. When forced is non-nil, its action is used
// instead of consulting the Policy Engine — this is how a planned pattern
// replays a path it already found.
func (o *Orchestrator) step(ctx context.Context, belief component.Belief, forced *component.Action) (stepResult, error) {
	c := o.cfg
	var res stepResult

	obs, err := c.Gateway.Observe(ctx)
	if err != nil {
		return res, fmt.Errorf("observation gateway: %w", err)
	}
	c.Bus.Publish(event.Event{Type: EventObservation, Source: "observation_gateway", Payload: obs})

	if err := c.ObsLedger.Append(ctx, obs); err != nil {
		return res, fmt.Errorf("observation ledger: %w", err)
	}

	newBelief, err := c.Belief.Update(ctx, belief, obs)
	if err != nil {
		return res, fmt.Errorf("belief engine: %w", err)
	}
	res.belief = newBelief
	c.Bus.Publish(event.Event{Type: EventBeliefUpdated, Source: "belief_engine", Payload: newBelief})

	var action component.Action
	if forced != nil {
		action = *forced
	} else {
		action, err = c.Policy.Decide(ctx, c.Goal, newBelief)
		if err != nil {
			return res, fmt.Errorf("policy engine: %w", err)
		}
	}
	res.action = action
	c.Bus.Publish(event.Event{Type: EventActionSelected, Source: "policy_engine", Payload: action})

	current, err := c.StateL.Current(ctx)
	if err != nil {
		return res, fmt.Errorf("state ledger (read): %w", err)
	}
	res.prev = current

	next, err := c.Transition.Apply(ctx, current, action)
	if err != nil {
		return res, fmt.Errorf("transition engine: %w", err)
	}

	if err := c.Actuator.Execute(ctx, action); err != nil {
		return res, fmt.Errorf("actuator gateway: %w", err)
	}

	if err := c.StateL.Commit(ctx, next); err != nil {
		return res, fmt.Errorf("state ledger (commit): %w", err)
	}
	res.next = next
	c.Bus.Publish(event.Event{Type: EventStateCommitted, Source: "state_ledger", Payload: next})

	reward, err := c.Reward.Evaluate(ctx, current, action, next)
	if err != nil {
		return res, fmt.Errorf("reward evaluator: %w", err)
	}
	res.reward = reward
	c.Bus.Publish(event.Event{Type: EventReward, Source: "reward_evaluator", Payload: reward})

	return res, nil
}
