package lower

import (
	"sort"
	"strconv"
	"strings"
)

// CallGraph is a whole-corpus call graph over lowered functions.
//
// It exists to answer a question that has been argued twice without evidence:
// if a function suspends, how far does "might be on the stack when it does"
// spread? Selective CPS is only worth anything if that set stays small, and
// nothing measured it.
//
// # What it can and cannot resolve
//
// A call site resolves to a set of possible callees. `call.fn "f"` names one (or
// two, for a namespaced call with a global fallback); `call.static "C", "m"`
// names one. `call.method $x, "m"` names only the *method*, so without the
// receiver's class every method of that name is a candidate — the standard
// class-hierarchy approximation, and exactly the imprecision type inference
// would remove. `call.dynamic` names nothing at all.
//
// Unresolved sites are the interesting ones: a sound analysis must treat them as
// reaching anything, so one `call_user_func` in a function taints every caller
// above it. That is what makes the closure large or small, and it is measured
// rather than assumed.
type CallGraph struct {
	// World, when set, lets a `recv="C"` annotation narrow a method call to C
	// and its descendants instead of every method of that name. Without the
	// hierarchy the narrowing would be unsound, since a subclass may override.
	World *World
	nodes map[string]*cgNode
	// byMethod indexes every method by its bare name, which is all a
	// `call.method` site knows without type inference.
	byMethod map[string][]*cgNode
}

type cgNode struct {
	key string
	// suspends is set when the function itself contains a suspension point.
	suspends bool
	// opaque is set when the function contains a call this graph cannot
	// resolve, so a sound analysis must assume it reaches anything.
	opaque bool
	// direct are targets named by the call site itself; viaMethod are the ones
	// only a method *name* selects, which is where the receiver's class would
	// have narrowed things. Kept apart so the cost of each approximation can be
	// measured separately rather than argued about.
	direct    map[*cgNode]bool
	viaMethod map[*cgNode]bool
	sites     []callSite
	// fn is kept so a propagated marker can be written back where an emitter
	// will read it.
	fn *Function
	// delegates are the callees named by this body's `yield from` sites. A
	// suspension travels up a delegation chain, so the marker has to travel
	// with it.
	delegates []string
	// fiberSuspends is `suspends` restricted to suspensions that reach this
	// function's callers. See isFiberSuspension.
	fiberSuspends bool
	// generator marks a body whose *call* makes a Generator object rather than
	// running it. Such a call cannot suspend its caller whatever the body does,
	// so nothing propagates through an edge into one.
	generator bool
}

type callSite struct {
	kind   string // "fn" | "static" | "method" | "opaque"
	name   string // lowercased callee, or method name
	backup string // the global fallback of a namespaced call
	recv   string // the receiver's class, when inference supplied one
}

func NewCallGraph() *CallGraph {
	return &CallGraph{nodes: map[string]*cgNode{}, byMethod: map[string][]*cgNode{}}
}

// Add records a function under a key: `f` for a plain function, `c::m` for a
// method. Keys are lowercased, since PHP resolves both case-insensitively.
func (g *CallGraph) Add(key string, fn *Function) {
	key = strings.ToLower(key)
	n := &cgNode{key: key, fn: fn, direct: map[*cgNode]bool{}, viaMethod: map[*cgNode]bool{}}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if isSuspension(b.Term) {
			n.suspends = true
		}
		// **A generator's suspension is mediated; a fiber's is not.** Calling a
		// generator function returns a Generator object and suspends nobody,
		// so `yield` says nothing about this function's callers.
		// `Fiber::suspend()` stops the whole stack it runs on, so it does.
		// Seeding the two from one flag put every generator in the reach set
		// and split at every call to one.
		if isFiberSuspension(b.Term) {
			n.fiberSuspends = true
		}
		if isGeneratorTerm(b.Term) {
			n.generator = true
		}
		if d := delegateTarget(b, fn); d != "" {
			n.delegates = append(n.delegates, d)
		}
		for _, in := range b.Insts {
			if s, ok := siteOf(in); ok {
				if s.kind == "opaque" {
					n.opaque = true
				} else {
					n.sites = append(n.sites, s)
				}
			}
		}
	}
	g.nodes[key] = n
	if i := strings.Index(key, "::"); i >= 0 {
		m := key[i+2:]
		g.byMethod[m] = append(g.byMethod[m], n)
	}
}

// isGeneratorTerm marks the terminators that make a body a generator.
func isGeneratorTerm(term string) bool {
	return strings.HasPrefix(term, "yield") || strings.HasPrefix(term, "ret.yield") ||
		strings.HasPrefix(term, "generator_create") || strings.HasPrefix(term, "ret.generator")
}

// isFiberSuspension is the half of isSuspension that propagates to callers.
func isFiberSuspension(term string) bool {
	return strings.HasPrefix(term, "fiber.suspend") || strings.HasPrefix(term, "ret.suspend")
}

func isSuspension(term string) bool {
	return strings.HasPrefix(term, "yield") || strings.HasPrefix(term, "fiber.suspend") ||
		strings.HasPrefix(term, "generator_create") || strings.HasPrefix(term, "ret.yield") ||
		strings.HasPrefix(term, "ret.suspend") || strings.HasPrefix(term, "ret.generator")
}

func siteOf(in Inst) (callSite, bool) {
	// The IR renders literals with %q, so a namespaced name arrives with its
	// backslashes doubled -- `Cake\\Foo` for `Cake\Foo`. Stripping the quotes
	// without undoing that makes every namespaced callee fail to match its own
	// definition, which in a fully namespaced corpus is *all* of them.
	unquote := func(s string) (string, bool) {
		v, err := strconv.Unquote(s)
		if err != nil {
			return "", false
		}
		return strings.ToLower(v), true
	}
	switch in.Opcode {
	case "call.fn":
		if len(in.Operands) == 0 {
			return callSite{kind: "opaque"}, true
		}
		name, ok := unquote(in.Operands[0])
		if !ok {
			return callSite{kind: "opaque"}, true
		}
		s := callSite{kind: "fn", name: name}
		for _, a := range in.Attrs {
			if strings.HasPrefix(a, "fallback=") {
				if b, ok := unquote(strings.TrimPrefix(a, "fallback=")); ok {
					s.backup = b
				}
			}
		}
		return s, true
	case "call.static":
		if len(in.Operands) < 2 {
			return callSite{kind: "opaque"}, true
		}
		cls, ok1 := unquote(in.Operands[0])
		m, ok2 := unquote(in.Operands[1])
		if !ok1 || !ok2 {
			return callSite{kind: "opaque"}, true
		}
		return callSite{kind: "static", name: cls + "::" + m}, true
	case "call.method":
		if len(in.Operands) < 2 {
			return callSite{kind: "opaque"}, true
		}
		m, ok := unquote(in.Operands[1])
		if !ok {
			return callSite{kind: "opaque"}, true
		}
		// Without a receiver class every method of this name is a candidate.
		// `recv=` is what inference supplies to narrow it.
		site := callSite{kind: "method", name: m}
		for _, a := range in.Attrs {
			if strings.HasPrefix(a, "recv=") {
				if c, ok := unquote(strings.TrimPrefix(a, "recv=")); ok {
					site.recv = c
				}
			}
		}
		return site, true
	case "call.dynamic", "closure.from_call":
		return callSite{kind: "opaque"}, true
	}
	return callSite{}, false
}

// delegateTarget names the callee a `yield from` delegates to, when the source
// is a named call. The definition is always in the same block -- no SSA value
// crosses one -- so this is a scan rather than an analysis.
//
// Only the named-call form is resolvable without dataflow. `yield from $var`
// needs to know which generator the value holds, which is a value question and
// not a type one; `yield from new C()` delegates to an object and needs a
// further hop through its iterator.
func delegateTarget(b *Block, fn *Function) string {
	if !strings.HasPrefix(b.Term, "ret.yield_from ") && !strings.HasPrefix(b.Term, "yield_from ") {
		return ""
	}
	op := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(b.Term, "ret."), "yield_from "))
	if i := strings.Index(op, " ->"); i >= 0 {
		op = op[:i]
	}
	op = strings.TrimSuffix(op, ",")
	if !strings.HasPrefix(op, "%") {
		return ""
	}
	for _, in := range b.Insts {
		if in.Result == nil || in.Result.String() != op {
			continue
		}
		lit := func(s string) string {
			v, err := strconv.Unquote(s)
			if err != nil {
				return ""
			}
			return strings.ToLower(v)
		}
		switch in.Opcode {
		case "call.fn":
			if len(in.Operands) > 0 {
				return lit(in.Operands[0])
			}
		case "call.static":
			if len(in.Operands) > 1 {
				return lit(in.Operands[0]) + "::" + lit(in.Operands[1])
			}
		}
		return ""
	}
	return ""
}

// PropagateMarks carries `SuspendsToCaller` up every `yield from` chain it can
// resolve, and reports how many functions it newly marked.
//
// A generator delegating to one that suspends suspends itself, but nothing in
// its own body says so -- it contains no `Fiber::suspend` to match on. Without
// this the marker is absent exactly where the escaping call site is, which is
// the shape of `Zend/tests/gh15108-004`.
func (g *CallGraph) PropagateMarks() int {
	added := 0
	for changed := true; changed; {
		changed = false
		for _, n := range g.nodes {
			if n.fn == nil || n.fn.SuspendsToCaller {
				continue
			}
			for _, d := range n.delegates {
				to, ok := g.nodes[d]
				if ok && to.fn != nil && to.fn.SuspendsToCaller {
					n.fn.SuspendsToCaller = true
					added++
					changed = true
					break
				}
			}
		}
	}
	return added
}

// Link resolves every call site against the definitions collected so far.
func (g *CallGraph) Link() {
	for _, n := range g.nodes {
		for _, s := range n.sites {
			switch s.kind {
			case "fn", "static":
				if to, ok := g.nodes[s.name]; ok {
					n.direct[to] = true
				} else if s.backup != "" {
					if to, ok := g.nodes[s.backup]; ok {
						n.direct[to] = true
					}
				}
			case "method":
				if s.recv != "" && g.World != nil {
					// Narrowed: the receiver's class and everything extending
					// it, since a subclass may override the method.
					for _, c := range g.World.Descendants(s.recv) {
						if to, ok := g.nodes[c+"::"+s.name]; ok {
							n.viaMethod[to] = true
						}
					}
					continue
				}
				for _, to := range g.byMethod[s.name] {
					n.viaMethod[to] = true
				}
			}
		}
	}
}

// Reach reports how far "might be on the stack when a suspension happens"
// spreads. A function is in the set if it suspends, calls something in the set,
// or -- when sound is true -- contains a call this graph cannot resolve.
//
// The two answers differ by exactly the cost of not having type inference and a
// value analysis, which is the number the fiber decision should rest on.
func (g *CallGraph) Reach(methods, sound bool) map[string]bool {
	return g.reachFrom(methods, sound, func(n *cgNode) bool { return n.suspends })
}

// ReachFiber is Reach seeded only by suspensions that propagate to a caller.
// It is the set the CPS rewrite must be judged against: a function in it has to
// become a state machine, because control can leave it for a fiber's driver.
//
// Reach's own seed counts `yield` too, which is right for the question it
// answers -- how far a suspension of any kind can be from the top of the stack
// -- and wrong for this one.
func (g *CallGraph) ReachFiber(methods, sound bool) map[string]bool {
	// A generator is **not** a seed even when its body suspends: the suspension
	// is mediated by its driver, and calling it only makes a Generator object.
	// Seeding one marked its callers resumable and then asked for a frame
	// constructor the generator entry point does not emit.
	return g.reachFrom(methods, sound, func(n *cgNode) bool {
		return n.fiberSuspends && !n.generator
	})
}

func (g *CallGraph) reachFrom(methods, sound bool, seed func(*cgNode) bool) map[string]bool {
	in := map[*cgNode]bool{}
	var seedOpaque bool
	for _, n := range g.nodes {
		if seed(n) {
			in[n] = true
		}
		if n.opaque {
			seedOpaque = true
		}
	}
	// A sound analysis must assume an unresolvable call reaches anything, so if
	// the program suspends anywhere, every function containing one is tainted.
	if sound && seedOpaque && len(in) > 0 {
		for _, n := range g.nodes {
			if n.opaque {
				in[n] = true
			}
		}
	}
	// **A call into a generator does not propagate.** Calling a generator
	// function makes a Generator object and runs nothing, so the caller cannot
	// suspend however the body behaves -- and treating it as if it could marked
	// callers resumable that have no reason to be, then asked for a frame
	// constructor the generator entry point never emits.
	reaches := func(to *cgNode) bool { return in[to] && !to.generator }
	for changed := true; changed; {
		changed = false
		for _, n := range g.nodes {
			if in[n] {
				continue
			}
			for to := range n.direct {
				if reaches(to) {
					in[n] = true
					changed = true
				}
			}
			if !in[n] && methods {
				for to := range n.viaMethod {
					if reaches(to) {
						in[n] = true
						changed = true
						break
					}
				}
			}
		}
	}
	out := make(map[string]bool, len(in))
	for n := range in {
		out[n.key] = true
	}
	return out
}

// Stats describes the graph itself, before any propagation.
type Stats struct {
	Functions   int
	Suspending  int
	Opaque      int
	Edges       int
	MethodEdges int
	// Unresolved counts call sites naming a callee this graph has no definition
	// for -- an engine builtin, or a function in a file the corpus did not
	// include. Distinct from Opaque, which is a site naming nothing at all.
	Unresolved int
}

func (g *CallGraph) Stats() Stats {
	var s Stats
	s.Functions = len(g.nodes)
	for _, n := range g.nodes {
		if n.suspends {
			s.Suspending++
		}
		if n.opaque {
			s.Opaque++
		}
		s.Edges += len(n.direct) + len(n.viaMethod)
		s.MethodEdges += len(n.viaMethod)
		for _, site := range n.sites {
			switch site.kind {
			case "fn", "static":
				if _, ok := g.nodes[site.name]; !ok {
					if site.backup == "" {
						s.Unresolved++
					} else if _, ok := g.nodes[site.backup]; !ok {
						s.Unresolved++
					}
				}
			case "method":
				if len(g.byMethod[site.name]) == 0 {
					s.Unresolved++
				}
			}
		}
	}
	return s
}

// FanIn returns, for each function, how many functions can transitively reach
// it. That is the blast radius of a hypothetical suspension in it, and it makes
// the propagation question answerable on a corpus that contains no fibers.
func (g *CallGraph) FanIn() []int {
	callers := map[*cgNode][]*cgNode{}
	for _, n := range g.nodes {
		for to := range n.direct {
			callers[to] = append(callers[to], n)
		}
		for to := range n.viaMethod {
			callers[to] = append(callers[to], n)
		}
	}
	out := make([]int, 0, len(g.nodes))
	for _, n := range g.nodes {
		seen := map[*cgNode]bool{n: true}
		stack := []*cgNode{n}
		for len(stack) > 0 {
			at := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, c := range callers[at] {
				if !seen[c] {
					seen[c] = true
					stack = append(stack, c)
				}
			}
		}
		out = append(out, len(seen)-1)
	}
	sort.Ints(out)
	return out
}
