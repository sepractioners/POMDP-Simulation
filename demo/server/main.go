// Command server hosts a small dashboard for watching the architecture's
// Orchestrator drive the gridworld demo in real time. It is a plain Go
// net/http server with an embedded HTML/JS page (no Node, no build step)
// streaming live events over SSE.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"architecture/demo/gridworld"
	"architecture/sdk/component"
	"architecture/sdk/component/memory"
	"architecture/sdk/event"
	"architecture/sdk/orchestrator"
)

//go:embed static/index.html
var staticFiles embed.FS

const runReset event.Type = "demo.run_reset"

// server holds the single long-lived event bus and a pointer to whichever
// run is currently active, so /events can stay connected across restarts
// triggered from the dashboard.
type server struct {
	bus event.Bus

	mu     sync.RWMutex
	env    *gridworld.Environment
	cancel context.CancelFunc
}

func newServer() *server {
	return &server{bus: event.NewChannelBus()}
}

type startRequest struct {
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Seed        int64   `json:"seed"`
	DelayMS     int     `json:"delayMs"`
	Pattern     string  `json:"pattern"`
	WallDensity float64 `json:"wallDensity"`
}

func (s *server) handleStart(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // empty/invalid body -> zero-value defaults below
	if req.Width <= 0 {
		req.Width = 12
	}
	if req.Height <= 0 {
		req.Height = 8
	}
	if req.Seed == 0 {
		req.Seed = rand.Int63()
	}
	if req.DelayMS <= 0 {
		req.DelayMS = 250
	}
	pattern := orchestrator.Pattern(req.Pattern)
	switch pattern {
	case orchestrator.PatternLinear, orchestrator.PatternTree, orchestrator.PatternGraph:
	default:
		pattern = orchestrator.PatternTree
	}
	if req.WallDensity < 0 || req.WallDensity > 0.6 {
		req.WallDensity = 0.22
	}

	rng := rand.New(rand.NewSource(req.Seed))
	start := gridworld.Position{X: rng.Intn(req.Width), Y: rng.Intn(req.Height)}
	goal := gridworld.Position{X: rng.Intn(req.Width), Y: rng.Intn(req.Height)}
	walls := gridworld.GenerateMaze(req.Width, req.Height, start, goal, req.WallDensity, req.Seed)

	env := gridworld.NewEnvironment(req.Width, req.Height, start, goal, req.Seed)
	env.Noise = 1
	env.Walls = walls

	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.env = env
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	s.bus.Publish(event.Event{
		Type:   runReset,
		Source: "server",
		Payload: map[string]any{
			"width": req.Width, "height": req.Height,
			"start": start, "goal": goal, "walls": wallList(walls), "pattern": pattern,
		},
	})

	go s.runOnce(ctx, env, goal, pattern, time.Duration(req.DelayMS)*time.Millisecond)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true, "width": req.Width, "height": req.Height, "start": start, "goal": goal, "pattern": pattern,
	})
}

func wallList(walls map[gridworld.Position]bool) []gridworld.Position {
	out := make([]gridworld.Position, 0, len(walls))
	for p := range walls {
		out = append(out, p)
	}
	return out
}

func (s *server) runOnce(ctx context.Context, env *gridworld.Environment, goal gridworld.Position, pattern orchestrator.Pattern, delay time.Duration) {
	actions := memory.NewActionRegistry()
	for _, m := range gridworld.Moves {
		actions.Register(m.Name, m.Delta)
	}

	gw := &gridworld.Gateway{Env: env, Delay: delay}
	obsLedger := memory.NewObservationLedger()
	belief := &gridworld.Belief{Alpha: 0.6}
	policy := &gridworld.Policy{Goal: goal, Registry: actions}
	transition := &gridworld.Transition{Width: env.Width, Height: env.Height, Walls: env.Walls}
	stateLedger := memory.NewStateLedger(component.State{Data: env.TruePosition()})
	actuator := &gridworld.Actuator{Env: env}
	reward := &gridworld.Reward{Goal: goal}

	orch := orchestrator.New(orchestrator.Config{
		Bus:      s.bus,
		Goal:     component.Goal{ID: "reach-goal", Data: goal},
		Pattern:  pattern,
		MaxSteps: 500,

		Gateway:    gw,
		ObsLedger:  obsLedger,
		Belief:     belief,
		Policy:     policy,
		Actions:    actions,
		Transition: transition,
		StateL:     stateLedger,
		Actuator:   actuator,
		Reward:     reward,

		IsGoalReached: func(st component.State) bool {
			p, ok := st.Data.(gridworld.Position)
			return ok && p.Equal(goal)
		},
		MaxStepRetries: 1,
		MaxPlanNodes:   20000,
	})

	if err := orch.Run(ctx); err != nil && ctx.Err() == nil {
		log.Printf("run finished with error: %v", err)
	}
}

// handleEvents streams every event published on the bus to the browser
// over Server-Sent Events, enriched with the current run's ground-truth
// position so the dashboard can show belief-vs-truth convergence.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsubscribe := s.bus.Subscribe()
	defer unsubscribe()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			s.mu.RLock()
			env := s.env
			s.mu.RUnlock()

			wire := map[string]any{
				"seq": evt.Seq, "type": evt.Type, "source": evt.Source,
				"time": evt.Time, "payload": evt.Payload,
			}
			if env != nil {
				wire["true"] = env.TruePosition()
				wire["goal"] = env.Goal
			}
			b, err := json.Marshal(wire)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	s := newServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/start", s.handleStart)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		b, err := staticFiles.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(b)
	})

	log.Printf("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
