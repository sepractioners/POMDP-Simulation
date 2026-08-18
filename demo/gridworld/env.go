// Package gridworld is a small simulated environment used to exercise the
// architecture end to end: an agent senses a noisy version of its true
// position and must navigate to a goal cell it cannot directly observe.
package gridworld

import (
	"math/rand"
	"sync"
)

// Position is a cell on the grid.
type Position struct {
	X, Y int
}

func (p Position) Equal(o Position) bool { return p.X == o.X && p.Y == o.Y }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Environment holds the hidden ground-truth state of the simulated world.
// Only the Actuator (via Move) is allowed to change TruePosition; the
// Observation Gateway may only read a noisy view of it.
type Environment struct {
	mu     sync.Mutex
	Width  int
	Height int
	Goal   Position
	Walls  map[Position]bool
	true_  Position
	rng    *rand.Rand
	Noise  int
}

// NewEnvironment constructs a grid of the given size with a starting and
// goal position.
func NewEnvironment(width, height int, start, goal Position, seed int64) *Environment {
	return &Environment{
		Width:  width,
		Height: height,
		Goal:   goal,
		Walls:  map[Position]bool{},
		true_:  start,
		rng:    rand.New(rand.NewSource(seed)),
		Noise:  1,
	}
}

// TruePosition returns the hidden ground-truth position.
func (e *Environment) TruePosition() Position {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.true_
}

// NoisyPosition returns the true position perturbed by uniform noise in
// [-Noise, Noise] on each axis, clamped to the grid bounds. This is what
// the Observation Gateway exposes to the rest of the system.
func (e *Environment) NoisyPosition() Position {
	e.mu.Lock()
	defer e.mu.Unlock()
	dx, dy := 0, 0
	if e.Noise > 0 {
		dx = e.rng.Intn(2*e.Noise+1) - e.Noise
		dy = e.rng.Intn(2*e.Noise+1) - e.Noise
	}
	return Position{
		X: clamp(e.true_.X+dx, 0, e.Width-1),
		Y: clamp(e.true_.Y+dy, 0, e.Height-1),
	}
}

// Move applies delta to the true position, clamped to the grid bounds,
// and returns the resulting true position. This is the only way ground
// truth changes, and it is invoked by the Actuator Gateway.
func (e *Environment) Move(delta Position) Position {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.true_ = Position{
		X: clamp(e.true_.X+delta.X, 0, e.Width-1),
		Y: clamp(e.true_.Y+delta.Y, 0, e.Height-1),
	}
	return e.true_
}
