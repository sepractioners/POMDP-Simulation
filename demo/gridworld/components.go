package gridworld

import (
	"context"
	"fmt"
	"math"
	"time"

	"architecture/sdk/component"
)

// --- Observation Gateway -----------------------------------------------

// Gateway reads a noisy view of the Environment's true position. Delay
// paces each observation so a live viewer can follow the run; it is a
// demo-only concern and has no equivalent in the core SDK.
type Gateway struct {
	Env   *Environment
	Delay time.Duration
}

func (g *Gateway) Observe(ctx context.Context) (component.Observation, error) {
	if g.Delay > 0 {
		select {
		case <-time.After(g.Delay):
		case <-ctx.Done():
			return component.Observation{}, ctx.Err()
		}
	}
	return component.Observation{Data: g.Env.NoisyPosition()}, nil
}

// --- Belief Engine -------------------------------------------------------

// Belief exponentially smooths noisy observations into a position
// estimate, rounding to the nearest cell.
type Belief struct{ Alpha float64 }

func (b *Belief) Update(_ context.Context, prior component.Belief, obs component.Observation) (component.Belief, error) {
	o := obs.Data.(Position)
	if prior.Data == nil {
		return component.Belief{Data: o}, nil
	}
	p := prior.Data.(Position)
	alpha := b.Alpha
	if alpha <= 0 {
		alpha = 0.6
	}
	next := Position{
		X: int(math.Round(alpha*float64(o.X) + (1-alpha)*float64(p.X))),
		Y: int(math.Round(alpha*float64(o.Y) + (1-alpha)*float64(p.Y))),
	}
	return component.Belief{Data: next}, nil
}

// --- Action Registry seeding ---------------------------------------------

// MoveDef is one entry in the gridworld's fixed action set.
type MoveDef struct {
	Name  string
	Delta Position
}

// Moves is the fixed action set for the gridworld, in priority order:
// register these with a component.ActionRegistry before running the
// orchestrator. Order matters for PatternTree/Graph, whose search tries
// actions in ActionRegistry.List() order — putting "stay" last keeps the
// planner from preferring a no-op over real progress.
var Moves = []MoveDef{
	{"up", Position{X: 0, Y: -1}},
	{"down", Position{X: 0, Y: 1}},
	{"left", Position{X: -1, Y: 0}},
	{"right", Position{X: 1, Y: 0}},
	{"stay", Position{X: 0, Y: 0}},
}

// --- Policy Engine ---------------------------------------------------------

// Policy greedily moves one cell per step along whichever axis is
// furthest from the goal, based on the current belief (never the hidden
// true position).
type Policy struct {
	Goal     Position
	Registry component.ActionRegistry
}

func (p *Policy) Decide(_ context.Context, _ component.Goal, belief component.Belief) (component.Action, error) {
	b, _ := belief.Data.(Position)
	dx := p.Goal.X - b.X
	dy := p.Goal.Y - b.Y

	name := "stay"
	switch {
	case dx == 0 && dy == 0:
		name = "stay"
	case abs(dx) >= abs(dy) && dx != 0:
		if dx > 0 {
			name = "right"
		} else {
			name = "left"
		}
	case dy != 0:
		if dy > 0 {
			name = "down"
		} else {
			name = "up"
		}
	}

	meta, ok := p.Registry.Get(name)
	if !ok {
		return component.Action{}, fmt.Errorf("gridworld: action %q not registered", name)
	}
	return component.Action{Name: name, Params: meta}, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// --- Transition Engine -----------------------------------------------------

// Transition is a pure prediction of the effect of an action on the
// committed State; it does not touch the Environment directly. It is the
// sole gatekeeper for walls: it rejects a move into a wall cell so
// PatternTree/Graph's planner (and PatternLinear's step, on the way to
// the Actuator) never treats a blocked move as valid.
type Transition struct {
	Width, Height int
	Walls         map[Position]bool
}

func (t *Transition) Apply(_ context.Context, current component.State, action component.Action) (component.State, error) {
	cur, _ := current.Data.(Position)
	delta, _ := action.Params.(Position)
	next := Position{
		X: clamp(cur.X+delta.X, 0, t.Width-1),
		Y: clamp(cur.Y+delta.Y, 0, t.Height-1),
	}
	if t.Walls[next] {
		return component.State{}, fmt.Errorf("gridworld: %v is a wall", next)
	}
	return component.State{Data: next}, nil
}

// --- Actuator Gateway --------------------------------------------------------

// Actuator is the only thing allowed to mutate the Environment's hidden
// ground truth; it carries out the action the Policy Engine selected.
type Actuator struct{ Env *Environment }

func (a *Actuator) Execute(_ context.Context, action component.Action) error {
	delta, _ := action.Params.(Position)
	a.Env.Move(delta)
	return nil
}

// --- Reward Evaluator ---------------------------------------------------------

type Reward struct{ Goal Position }

func (r *Reward) Evaluate(_ context.Context, _ component.State, _ component.Action, next component.State) (component.Reward, error) {
	n, _ := next.Data.(Position)
	dist := abs(r.Goal.X-n.X) + abs(r.Goal.Y-n.Y)
	value := -1.0
	if dist == 0 {
		value = 100.0
	}
	return component.Reward{Value: value, Info: dist}, nil
}
