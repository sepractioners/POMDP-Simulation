package gridworld

import "math/rand"

// GenerateMaze scatters walls at the given density (0..1) across a
// width x height grid, leaving start and goal clear, and retries with a
// fresh layout until the result has a path between them (checked by
// plain BFS ignoring the true environment noise/actions — walls are the
// only obstacle). Returns an empty wall set if no solvable layout was
// found within the attempt budget, which for reasonable densities on
// reasonably sized grids essentially never happens.
func GenerateMaze(width, height int, start, goal Position, density float64, seed int64) map[Position]bool {
	rng := rand.New(rand.NewSource(seed))
	for attempt := 0; attempt < 200; attempt++ {
		walls := make(map[Position]bool)
		for x := 0; x < width; x++ {
			for y := 0; y < height; y++ {
				p := Position{X: x, Y: y}
				if p.Equal(start) || p.Equal(goal) {
					continue
				}
				if rng.Float64() < density {
					walls[p] = true
				}
			}
		}
		if reachable(width, height, start, goal, walls) {
			return walls
		}
	}
	return map[Position]bool{}
}

// reachable does a BFS over the four cardinal moves to check whether goal
// can be reached from start without crossing a wall.
func reachable(width, height int, start, goal Position, walls map[Position]bool) bool {
	if start.Equal(goal) {
		return true
	}
	seen := map[Position]bool{start: true}
	queue := []Position{start}
	deltas := []Position{{X: 0, Y: -1}, {X: 0, Y: 1}, {X: -1, Y: 0}, {X: 1, Y: 0}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range deltas {
			next := Position{X: cur.X + d.X, Y: cur.Y + d.Y}
			if next.X < 0 || next.X >= width || next.Y < 0 || next.Y >= height {
				continue
			}
			if walls[next] || seen[next] {
				continue
			}
			if next.Equal(goal) {
				return true
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return false
}
