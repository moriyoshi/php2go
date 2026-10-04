package lower

// CFG is the control-flow graph of a lowered function, with predecessors, a
// dominator tree and the loops those imply.
//
// Predecessors are derived here rather than stored on Block, because Succs is
// the single authority and a second copy would be free to drift from it -- the
// same failure the terminator text and Succs are guarded against by a test.
//
// # Exception edges are included, deliberately
//
// IR-DESIGN.md models exceptions as regions rather than as an edge out of every
// call, which is right for representation and wrong for dominance: a handler
// can be entered from anywhere in the protected range, so ignoring those edges
// would let this claim that a block dominates one it does not. Every block in a
// region therefore gets an edge to that region's handlers and, following the
// parent chain, to the handlers of every region enclosing it. Extra edges can
// only weaken dominance, which is the safe direction.
type CFG struct {
	fn    *Function
	succs [][]int
	preds [][]int
	// rpoAt is a block's position in reverse postorder, or -1 when the block is
	// not reachable from the entry.
	rpoAt []int
	rpo   []int
	idom  []int
}

// Loop is a natural loop: a back edge's target and everything that can reach
// the edge's source without leaving through the header.
type Loop struct {
	Header  int
	Latches []int
	Blocks  []int
}

// Analyze builds the graph. Dead blocks -- finally bodies, which exist only as
// inlined copies -- take no part in it.
func Analyze(fn *Function) *CFG {
	n := len(fn.Blocks)
	c := &CFG{
		fn:    fn,
		succs: make([][]int, n),
		preds: make([][]int, n),
		rpoAt: make([]int, n),
		idom:  make([]int, n),
	}
	for i := range c.rpoAt {
		c.rpoAt[i] = -1
		c.idom[i] = -1
	}
	for i, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, s := range b.Succs {
			if s >= 0 && s < n && !fn.Blocks[s].Dead {
				c.succs[i] = append(c.succs[i], s)
			}
		}
		// Exception edges, up the whole enclosing chain. A finally body is a
		// Dead block -- it is inlined at each *normal* exit -- so the unwinding
		// path through it is not modelled here; see the note in IR-DESIGN.md.
		for r := b.Region; r >= 0 && r < len(fn.Regions); r = fn.Regions[r].Parent {
			for _, h := range []int{fn.Regions[r].Catch, fn.Regions[r].Finally} {
				if h >= 0 && h < n && !fn.Blocks[h].Dead && !contains(c.succs[i], h) {
					c.succs[i] = append(c.succs[i], h)
				}
			}
		}
	}
	for i := range c.succs {
		for _, s := range c.succs[i] {
			c.preds[s] = append(c.preds[s], i)
		}
	}
	c.buildRPO()
	c.buildDom()
	return c
}

func contains(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Preds returns the blocks that can reach this one in one step.
func (c *CFG) Preds(b int) []int { return c.preds[b] }

// Succs returns the graph's successors, which include the exception edges that
// Block.Succs does not.
func (c *CFG) Succs(b int) []int { return c.succs[b] }

// Reachable reports whether control can arrive at this block from the entry.
func (c *CFG) Reachable(b int) bool { return b >= 0 && b < len(c.rpoAt) && c.rpoAt[b] >= 0 }

// Idom is the immediate dominator, or -1 for the entry and for anything
// unreachable.
func (c *CFG) Idom(b int) int { return c.idom[b] }

// Dominates reports whether every path from the entry to b passes through a.
// A block dominates itself. An unreachable block is dominated by nothing, which
// keeps callers from concluding anything about code that cannot run.
func (c *CFG) Dominates(a, b int) bool {
	if !c.Reachable(a) || !c.Reachable(b) {
		return false
	}
	for at := b; at != -1; at = c.idom[at] {
		if at == a {
			return true
		}
		if at == c.idom[at] {
			break // the entry dominates itself; stop
		}
	}
	return false
}

func (c *CFG) buildRPO() {
	n := len(c.fn.Blocks)
	entry := c.fn.Entry
	if n == 0 || entry < 0 || entry >= n || c.fn.Blocks[entry].Dead {
		return
	}
	seen := make([]bool, n)
	var post []int
	// Iterative depth-first search: a deeply nested body would otherwise be a
	// recursion depth proportional to the function's block count.
	type frame struct{ at, next int }
	stack := []frame{{entry, 0}}
	seen[entry] = true
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.next < len(c.succs[top.at]) {
			s := c.succs[top.at][top.next]
			top.next++
			if !seen[s] {
				seen[s] = true
				stack = append(stack, frame{s, 0})
			}
			continue
		}
		post = append(post, top.at)
		stack = stack[:len(stack)-1]
	}
	for i := len(post) - 1; i >= 0; i-- {
		c.rpoAt[post[i]] = len(c.rpo)
		c.rpo = append(c.rpo, post[i])
	}
}

// buildDom is Cooper, Harvey and Kennedy's iterative dominator algorithm: walk
// reverse postorder to a fixpoint, intersecting predecessors by climbing the
// partially-built tree. Simpler than Lengauer-Tarjan and faster on the block
// counts a PHP function produces.
func (c *CFG) buildDom() {
	if len(c.rpo) == 0 {
		return
	}
	entry := c.rpo[0]
	c.idom[entry] = entry
	for changed := true; changed; {
		changed = false
		for _, b := range c.rpo[1:] {
			newIdom := -1
			for _, p := range c.preds[b] {
				if c.idom[p] == -1 {
					continue // not processed yet
				}
				if newIdom == -1 {
					newIdom = p
					continue
				}
				newIdom = c.intersect(p, newIdom)
			}
			if newIdom != -1 && c.idom[b] != newIdom {
				c.idom[b] = newIdom
				changed = true
			}
		}
	}
	c.idom[entry] = -1 // the entry has no dominator but itself
}

func (c *CFG) intersect(a, b int) int {
	for a != b {
		for c.rpoAt[a] > c.rpoAt[b] {
			a = c.idom[a]
		}
		for c.rpoAt[b] > c.rpoAt[a] {
			b = c.idom[b]
		}
	}
	return a
}

// BackEdges returns the edges whose target dominates their source. Each one is
// a loop.
func (c *CFG) BackEdges() [][2]int {
	var out [][2]int
	for _, b := range c.rpo {
		for _, s := range c.succs[b] {
			if c.Dominates(s, b) {
				out = append(out, [2]int{b, s})
			}
		}
	}
	return out
}

// Loops returns the natural loop of each header, merging the back edges that
// share one.
func (c *CFG) Loops() []Loop {
	byHeader := map[int]*Loop{}
	var order []int
	for _, e := range c.BackEdges() {
		latch, header := e[0], e[1]
		l, seen := byHeader[header]
		if !seen {
			l = &Loop{Header: header, Blocks: []int{header}}
			byHeader[header] = l
			order = append(order, header)
		}
		l.Latches = append(l.Latches, latch)
		// Everything that reaches the latch without passing back through the
		// header is in the loop.
		inLoop := map[int]bool{header: true}
		for _, b := range l.Blocks {
			inLoop[b] = true
		}
		stack := []int{latch}
		for len(stack) > 0 {
			at := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if inLoop[at] {
				continue
			}
			inLoop[at] = true
			l.Blocks = append(l.Blocks, at)
			stack = append(stack, c.preds[at]...)
		}
	}
	out := make([]Loop, 0, len(order))
	for _, h := range order {
		out = append(out, *byHeader[h])
	}
	return out
}

// InCycle reports, per block, whether control can return to it.
//
// This is deliberately *not* natural-loop membership. It over-approximates:
// an irreducible loop has no back edge and so no natural loop, but control
// still returns to its blocks. The loop-builder analysis uses membership to
// *refuse* promotion when a read happens inside a loop, so missing a loop there
// would allow an unsound promotion rather than merely a missed one.
func (c *CFG) InCycle() []bool {
	n := len(c.fn.Blocks)
	out := make([]bool, n)
	for b := 0; b < n; b++ {
		if c.fn.Blocks[b].Dead {
			continue
		}
		seen := make([]bool, n)
		stack := append([]int(nil), c.succs[b]...)
		for len(stack) > 0 {
			at := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[at] {
				continue
			}
			seen[at] = true
			if at == b {
				out[b] = true
				break
			}
			stack = append(stack, c.succs[at]...)
		}
	}
	return out
}
