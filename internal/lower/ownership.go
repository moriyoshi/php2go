package lower

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// Who owns each allocation, and how ownership got there.
//
// `ConfinedObjects` asks one question of an object: does it outlive its frame.
// Measured on real library code the answer is "yes" for ~95% of `new` sites, and
// the reason is rarely the object itself -- it is the **container** it went
// into, which is then returned, passed or stored. This analysis answers the
// question that follows: which owners does an allocation have, through which
// containers, is ownership ever shared or only moved, and is it ever written in
// place. OWNERSHIP.md is the design; OWNERSHIP-PERF.md says what the answers are
// for. The two share their vocabulary with this file.
//
// It is analysis only. Nothing here decides what the emitter destroys or copies.

// EdgeClass is how an operation relates an operand to its result or to a
// container. Every intrinsic the lowering can emit declares one per operand
// position; see ownershipRules.
type EdgeClass uint8

const (
	// EdgeObserve reads the operand and retains nothing.
	EdgeObserve EdgeClass = iota
	// EdgeForward makes the result the operand's own identity.
	EdgeForward
	// EdgeAlias joins two operands into one identity: a store into a place.
	EdgeAlias
	// EdgeContain makes the operand an element of a container.
	EdgeContain
	// EdgeSplice makes every element of the operand an element of a container.
	EdgeSplice
	// EdgeExtract makes the result some element of the operand.
	EdgeExtract
	// EdgePartOf makes the result a place inside the operand. For ownership it
	// behaves as EdgeExtract; it is kept apart because it is a different claim.
	EdgePartOf
	// EdgeSink sends the operand somewhere this analysis does not follow.
	EdgeSink
)

// ownRule is one intrinsic's edges. Argument positions it does not name are
// observed.
type ownRule struct {
	// Result is how the result relates to argument From: EdgeForward,
	// EdgeExtract, EdgePartOf, or EdgeObserve for a result that holds none of
	// the arguments (a count, a bool, a string). `Fresh` and `External`
	// override it.
	Result EdgeClass
	From   int
	// Fresh marks a result that is a new container this body owns.
	Fresh bool
	// External marks a result whose identity comes from outside the frame, with
	// the reason anything stored into it escapes.
	External string
	// Alias joins these two arguments: the first is the place written.
	Alias []int
	// Contain lists arguments that become elements of argument Into, or of the
	// result when Into is -1.
	Contain []int
	Splice  []int
	Into    int
	// Sink lists arguments that go somewhere untracked, for Reason.
	Sink   []int
	Reason string
	// Writes lists arguments the operation changes **in place**: an element
	// set, an append, a place taken for writing. Replacing what a variable
	// holds is not a write to the old value, so `write_var` has none.
	//
	// The array builders (`array.append`, `array.set`) write their container
	// too, but when the container dies at the instruction the write is the
	// value being built, not a change anyone else can see -- see traceIntrinsic.
	Writes []int
	// ReEnters marks an operation that can call user code with an operand as
	// `$this` where the catalogue does not say so. The catalogue's flag was
	// set for diagnostic ordering, and it misses the ArrayAccess dispatch:
	// `$o[$k]` on an object is `offsetGet` with `$o` as `$this`.
	ReEnters bool
}

var (
	observe  = ownRule{}
	forward0 = ownRule{Result: EdgeForward, From: 0}
	extract0 = ownRule{Result: EdgeExtract, From: 0}
	partOf0  = ownRule{Result: EdgePartOf, From: 0, Writes: []int{0}}
)

// ownershipRules declares the edges of **every** intrinsic the lowering can
// emit, by the name the IR carries after IntrinsicCalls: the catalogue variant
// name, plus the variable intrinsics. TestOwnershipRulesAreComplete fails when
// a name has no entry, because an intrinsic this table does not know is a sink
// for everything it mentions -- sound, and silently imprecise, which is how the
// measurement harness this replaced went wrong three times.
var ownershipRules = map[string]ownRule{
	// The variables. A write joins the place and the value; a read is the
	// place's identity.
	"write_var":       {Alias: []int{0, 1}},
	"bind_var":        {Alias: []int{0, 1}},
	"read_var":        forward0,
	"read_var.silent": forward0,
	"copy":            forward0,
	"copy.silent":     forward0,
	// The return check **is** the value: `return new A` in a typed function
	// returns the `new`. Missing this edge is what turned Twig's containers
	// from 76% returned into 92% "stays in the frame".
	"coerce_return": forward0,

	// Containers built in place: the result is the array, and the value is
	// its element. Both edges at once.
	"array.new":          {Fresh: true},
	"array.append":       {Result: EdgeForward, From: 0, Contain: []int{1}, Into: -1, Writes: []int{0}},
	"array.append.bound": {Result: EdgeForward, From: 0, Contain: []int{1}, Into: -1, Writes: []int{0}},
	"array.set":          {Result: EdgeForward, From: 0, Contain: []int{2}, Into: -1, Writes: []int{0}},
	"array.set.key":      {Result: EdgeForward, From: 0, Contain: []int{2}, Into: -1, Writes: []int{0}},
	"array.set.bound":    {Result: EdgeForward, From: 0, Contain: []int{2}, Into: -1, Writes: []int{0}},
	"array.unpack":       {Result: EdgeForward, From: 0, Splice: []int{1}, Into: -1, Writes: []int{0}},

	// Writes into a container that already exists.
	"dim.set":    {Contain: []int{2}, Into: 0, Writes: []int{0}, ReEnters: true},
	"dim.append": {Contain: []int{1}, Into: 0, Writes: []int{0}, ReEnters: true},
	"prop.set":   {Contain: []int{2}, Into: 0, Writes: []int{0}},
	"sprop.set":  {Sink: []int{2}, Reason: "static"},

	// Reads out of a container.
	"dim.get":              {Result: EdgeExtract, From: 0, ReEnters: true},
	"dim.get.silent":       {Result: EdgeExtract, From: 0, ReEnters: true},
	"dim.get.array":        extract0,
	"dim.get.array.silent": extract0,
	// Destructuring reads: an element comes out of the container, as above.
	"dim.get.list":       {Result: EdgeExtract, From: 0, ReEnters: true},
	"dim.get.list.array": extract0,
	"prop.get":           extract0,
	"prop.get.silent":    extract0,
	// A static read is a value; the static's own storage is `sprop.ref`.
	"sprop.get":        {External: "static.read"},
	"sprop.get.silent": {External: "static.read"},

	// Places inside a container, taken to be written through.
	"prop.ref":         partOf0,
	"prop.ref.rw":      partOf0,
	"prop.ref.unset":   partOf0,
	"dim.ref":          {Result: EdgePartOf, From: 0, Writes: []int{0}, ReEnters: true},
	"dim.ref.rw":       {Result: EdgePartOf, From: 0, Writes: []int{0}, ReEnters: true},
	"dim.ref.unset":    {Result: EdgePartOf, From: 0, Writes: []int{0}, ReEnters: true},
	"dim.append.ref":   {Result: EdgePartOf, From: 0, Writes: []int{0}, ReEnters: true},
	"sprop.ref":        {External: "static"},
	"func.args":        {External: "param"},
	"func.num_args":    observe,
	"is_set":           observe,
	"array_key_exists": observe,

	// Iteration. The iterator stands for its subject, so what it yields is an
	// element of the subject. A key can be an object -- `yield $o => 1` -- so
	// it is extracted too. By reference, the loop writes its subject.
	"iter.new":           forward0,
	"iter.new.array":     forward0,
	"iter.new.ref":       {Result: EdgeForward, From: 0, Writes: []int{0}},
	"iter.current":       extract0,
	"iter.current.array": extract0,
	"iter.current.ref":   {Result: EdgeExtract, From: 0, Writes: []int{0}},
	"iter.key":           extract0,
	"iter.key.array":     extract0,
	"iter.next":          observe,
	"iter.next.array":    observe,
	"iter.valid":         observe,
	"iter.free":          observe,
	"iter.free.owned":    observe,

	// Conversions. `(array)$o` is a new array of the object's properties and
	// `(object)$a` the reverse, so the result shares the operand's elements;
	// treating it as the same identity is a superset of that.
	"to.array":  forward0,
	"to.object": forward0,
	"to_bool":   observe,
	"to.int":    observe,
	"to.float":  observe,
	"to.null":   observe,
	"to_string": observe,

	// Everything that answers a scalar. Whether one of these can call user
	// code is the catalogue's to say.
	"add": observe, "add.int": observe, "add.numeric": observe,
	"sub": observe, "sub.int": observe, "sub.numeric": observe,
	"mul": observe, "mul.int": observe, "mul.numeric": observe,
	"div": observe, "mod": observe,
	"pow": observe, "pow.int": observe, "pow.numeric": observe,
	"concat":  observe,
	"bit_and": observe, "bit_and.int": observe,
	"bit_or": observe, "bit_or.int": observe,
	"bit_xor": observe, "bit_xor.int": observe,
	"bit_not": observe, "shift_left": observe, "shift_right": observe,
	"bool_xor": observe, "not_bool": observe,
	"compare": observe, "loose_equal": observe, "not_loose_equal": observe,
	"strict_equal": observe, "not_strict_equal": observe,
	"is_smaller": observe, "is_smaller_or_equal": observe,
	"increment": observe, "increment.int": observe, "increment.numeric": observe,
	"decrement": observe, "decrement.int": observe, "decrement.numeric": observe,
	"count": observe, "count.array": observe,
	"strlen": observe, "get_type": observe, "type_check": observe,
	"echo":                     observe,
	"warn.undefined_container": observe,
	"strbuf.new":               observe,
	"strbuf.append":            {Writes: []int{0}},
	"strbuf.finish":            observe,
}

// OwnershipRuleNames are the intrinsics ownershipRules declares, for the
// completeness test.
func OwnershipRuleNames() map[string]bool {
	out := map[string]bool{}
	for n := range ownershipRules {
		out[n] = true
	}
	return out
}

// OwnershipRuleWrites reports whether a rule changes an argument in place.
func OwnershipRuleWrites(name string) bool {
	return len(ownershipRules[name].Writes) > 0
}

// catalogueReEnters is which catalogue names may call user code.
var catalogueReEnters = func() map[string]bool {
	out := map[string]bool{}
	for _, s := range php.Catalogue() {
		for _, v := range s.Variants {
			if v.Effects&php.ReEnters != 0 {
				out[v.Name] = true
			}
		}
	}
	return out
}()

// OwnershipOptions are the facts about *other* bodies the trace needs. Each is
// the same predicate ConfinedObjectsWith takes, and nil means the safe answer:
// the object is kept, the result is not fresh.
type OwnershipOptions struct {
	KeepsArg  func(callees []string, param int) bool
	LeaksThis func(method string) bool
	ArgKept   func(method string) bool
	// CtorLeaksThis answers per class, because every constructor shares one
	// name.
	CtorLeaksThis func(class string) bool
	// ReEntry reports whether user code reached implicitly from an intrinsic --
	// `__toString`, `__get`, `offsetGet`, a property hook -- can keep an
	// operand. Nil means it can.
	ReEntry func(intrinsic string) bool
	// FnReturnsFresh and MethodReturnsFresh report a callee whose result
	// nothing else holds -- OwnershipTrace.ReturnsFresh of its body. A fresh
	// result is an allocation site here; any other is borrowed.
	FnReturnsFresh     func(callees []string) bool
	MethodReturnsFresh func(method string) bool
	// ByRefArg reports whether a call takes argument i by reference. It
	// decides the `preferref` sends -- every method call's, since the callee
	// is not known when the call is compiled -- which otherwise count as
	// writes. Nil means they do. An explicit `byref` send always writes.
	ByRefArg func(in Inst, arg int) bool
	// FileBody marks the file's top-level code, whose locals are globals.
	FileBody bool
}

// Ownership is what the trace found for one allocation.
type Ownership struct {
	// Site is the allocating value, `%3`, or a synthetic name for an array
	// literal written inline as an operand.
	Site string
	// Kind is "new", "clone", "factory", "array" (`array.new`), "literal" (an
	// inline array literal) or "call" (the result of a callee known to return
	// something fresh).
	Kind  string
	Class string
	Line  string
	// Escapes are the reasons the allocation outlives the frame, sorted; none
	// means it is confined.
	Escapes []string
	// Via is the containment chain to the first owner that escapes, innermost
	// first: `[%3, $list]` reads "contained in %3, which is contained in
	// $list". Empty when the allocation escapes on its own.
	Via []string
	// Owners counts what may hold the allocation inside the frame: locals,
	// containers, and untracked destinations other than a terminator.
	Owners int
	// Shared is Owners > 1. An allocation that is not shared is only ever
	// moved, so its refcount never exceeds one.
	Shared bool
	// Written reports that the allocation may be changed in place after it is
	// built -- by this body, through anything it shares an identity with, or
	// by code the trace cannot see. An array that is never written can be
	// shared instead of copied.
	Written bool
}

// Confined reports an allocation that provably dies with its frame.
func (o Ownership) Confined() bool { return len(o.Escapes) == 0 }

// Move is a store whose value nothing else holds and nothing reads again, so
// the destination can take it without a copy.
type Move struct {
	Block, Index int
	Value, Place string
}

// InstPos names one instruction by block and index; Index == len(Insts) is
// the terminator.
type InstPos struct{ Block, Index int }

// Key is how Function.SharedStores names it.
func (p InstPos) Key() string { return fmt.Sprintf("%d.%d", p.Block, p.Index) }

// ReleasePoint is a value whose share is handed back after an instruction.
type ReleasePoint struct {
	Block, Index int
	Value        string
}

// LiteralUse is one inline array literal: the instruction it is an operand of
// (Index == len(Insts) for the terminator) and its text.
type LiteralUse struct {
	Block, Index int
	Text         string
}

// Append is one concatenation that could extend its left operand in place.
type Append struct {
	Block, Index int
	Left, Result string
}

// OwnershipTrace is everything the trace found in one body.
type OwnershipTrace struct {
	Sites []Ownership
	// Moves are the `write_var` stores that need no copy: the value is fresh,
	// dies at the store, and has no owner but the destination.
	Moves []Move
	// Appends are the concatenations whose left operand dies at the concat,
	// is this body's own, is held by no local, and has no other name still
	// live -- so the concatenation could extend it in place.
	Appends []Append
	// ReturnsFresh reports a body whose every return hands back something
	// nothing else holds: an allocation it made, or a scalar.
	ReturnsFresh bool
	// ParamWritten reports, per `recv` position, whether the body may change
	// the parameter's value in place.
	ParamWritten []bool
	// ParamReleasable reports, per `recv` position, whether the holder the
	// parameter arrived as never leaves the frame as itself -- not returned,
	// yielded, captured by reference or handed to an operation nothing here
	// understands -- so the callee may hand its share on the caller's array
	// back at exit (php.ReleaseShared). Passing it on, storing it, assigning
	// it all share into holders of their own, which is why those are allowed.
	ParamReleasable []bool
	// ReleaseAfter are the promoted copies (ShareAttr reads) whose share can
	// be handed back once their last use has run: the value is read,
	// passed on or stored -- each of which shares into a holder of its own --
	// and never kept as itself. Keyed by the instruction after which it dies.
	ReleaseAfter []ReleasePoint
	// SharedLiterals are the inline array literals that are never written and
	// never leave the frame, so one prebuilt array can stand for every
	// evaluation of them (OWNERSHIP-PERF.md P3a).
	SharedLiterals []LiteralUse
	// SharedCopies are the `copy` results whose operand is an allocation this
	// body made, never written and never leaving the frame, so the copy
	// changes nothing anyone can see.
	SharedCopies []string
	// SharedStores are the `write_var` stores whose value's identity --
	// destination included -- is private in the same sense, so the store
	// need not copy.
	SharedStores []InstPos

	graph *ownGraph
}

// sameIdentity reports two tokens the trace joined by forward or alias edges.
func (t *OwnershipTrace) sameIdentity(a, b string) bool {
	na, oka := t.graph.node[a]
	nb, okb := t.graph.node[b]
	return oka && okb && t.graph.findIdent(na) == t.graph.findIdent(nb)
}

// identityMembers are the SSA values in a token's identity.
func (t *OwnershipTrace) identityMembers(tok string) []string {
	n, ok := t.graph.node[tok]
	if !ok {
		return nil
	}
	id := t.graph.findIdent(n)
	var out []string
	for v, m := range t.graph.node {
		if strings.HasPrefix(v, "%") && t.graph.findIdent(m) == id {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// identityHasPlace reports whether any local holds the token's identity.
func (t *OwnershipTrace) identityHasPlace(tok string) bool {
	n, ok := t.graph.node[tok]
	return ok && len(t.graph.places[t.graph.findIdent(n)]) > 0
}

// identityBorrowed reports whether the token's identity may hold something
// this body did not make.
func (t *OwnershipTrace) identityBorrowed(tok string) bool {
	n, ok := t.graph.node[tok]
	return !ok || t.graph.borrowed[t.graph.findIdent(n)]
}

// ownGraph is two union-finds over the same nodes.
//
// The **escape** partition is Steensgaard's analysis specialised to ownership:
// forward edges join identities, and each container has one contents node that
// every element joins. It is what fates and writes are computed on, and joining
// all of a container's elements into one node is imprecise and sound -- no
// element can be lost.
//
// The **identity** partition follows forward and alias edges only. Owners are
// counted on it, because the escape partition would count every element of a
// container as an owner of every other: `[new A, new B]` is two objects with one
// owner each, not one object with two.
type ownGraph struct {
	parent   []int
	contents []int
	reasons  []map[string]bool
	// extracted marks a contents node something was read out of, so any
	// element may have an owner the identity partition cannot see.
	extracted []bool
	ident     []int
	places    []map[string]bool
	holds     []int
	contained []bool
	// site and borrowed are on the identity partition: whether it holds an
	// allocation this body made, and whether it may hold something it did not.
	site     []bool
	borrowed []bool
	// written is on the identity partition too: whether the identity may be
	// changed in place. **Not** on the escape partition, because storing into
	// a container copies -- `$this->nodes = $nodes` gives the property its own
	// array -- so a write through the container is not a write to what was
	// stored. On the escape partition every parameter a constructor put into
	// `$this` was written the moment any property was: half of Twig's.
	written  []bool
	name     []string
	node     map[string]int
	classOf  map[string]string
	fileBody bool
}

func newOwnGraph(fileBody bool) *ownGraph {
	return &ownGraph{node: map[string]int{}, classOf: map[string]string{}, fileBody: fileBody}
}

func (g *ownGraph) fresh(name string) int {
	n := len(g.parent)
	g.parent = append(g.parent, n)
	g.contents = append(g.contents, -1)
	g.reasons = append(g.reasons, nil)
	g.extracted = append(g.extracted, false)
	g.ident = append(g.ident, n)
	g.places = append(g.places, nil)
	g.holds = append(g.holds, 0)
	g.contained = append(g.contained, false)
	g.site = append(g.site, false)
	g.borrowed = append(g.borrowed, false)
	g.written = append(g.written, false)
	g.name = append(g.name, name)
	return n
}

// of is the node for a value or place token.
func (g *ownGraph) of(tok string) int {
	if n, ok := g.node[tok]; ok {
		return n
	}
	n := g.fresh(tok)
	g.node[tok] = n
	if strings.HasPrefix(tok, "$") {
		if tok == "$this" {
			g.addReason(n, "this")
			g.borrowed[n] = true
		} else {
			g.places[n] = map[string]bool{tok: true}
			if g.fileBody {
				g.addReason(n, "global")
				g.written[n] = true
			}
		}
	}
	return n
}

func (g *ownGraph) find(x int) int {
	for g.parent[x] != x {
		g.parent[x] = g.parent[g.parent[x]]
		x = g.parent[x]
	}
	return x
}

func (g *ownGraph) findIdent(x int) int {
	for g.ident[x] != x {
		g.ident[x] = g.ident[g.ident[x]]
		x = g.ident[x]
	}
	return x
}

// same joins two nodes into one identity: a forward or an alias edge.
func (g *ownGraph) same(a, b int) {
	ia, ib := g.findIdent(a), g.findIdent(b)
	if ia != ib {
		g.ident[ib] = ia
		for p := range g.places[ib] {
			if g.places[ia] == nil {
				g.places[ia] = map[string]bool{}
			}
			g.places[ia][p] = true
		}
		g.holds[ia] += g.holds[ib]
		g.contained[ia] = g.contained[ia] || g.contained[ib]
		g.site[ia] = g.site[ia] || g.site[ib]
		g.borrowed[ia] = g.borrowed[ia] || g.borrowed[ib]
		g.written[ia] = g.written[ia] || g.written[ib]
	}
	g.union(a, b)
}

// union joins two nodes in the escape partition only.
func (g *ownGraph) union(a, b int) int {
	ra, rb := g.find(a), g.find(b)
	if ra == rb {
		return ra
	}
	g.parent[rb] = ra
	for r := range g.reasons[rb] {
		g.addReason(ra, r)
	}
	g.extracted[ra] = g.extracted[ra] || g.extracted[rb]
	ca, cb := g.contents[ra], g.contents[rb]
	switch {
	case ca < 0:
		g.contents[ra] = cb
	case cb >= 0:
		// Merging two containers merges what they hold, which may merge ra
		// itself again when containment is cyclic.
		g.union(ca, cb)
	}
	return g.find(ra)
}

// elements is the contents node of a container, made on first use.
func (g *ownGraph) elements(x int) int {
	r := g.find(x)
	if g.contents[r] < 0 {
		g.contents[r] = g.fresh("elements of " + g.name[r])
	}
	return g.find(g.contents[r])
}

func (g *ownGraph) addReason(x int, reason string) {
	r := g.find(x)
	if g.reasons[r] == nil {
		g.reasons[r] = map[string]bool{}
	}
	g.reasons[r][reason] = true
}

func (g *ownGraph) write(x int)          { g.written[g.findIdent(x)] = true }
func (g *ownGraph) borrow(x int)         { g.borrowed[g.findIdent(x)] = true }
func (g *ownGraph) mkSite(x int)         { g.site[g.findIdent(x)] = true }
func (g *ownGraph) isWritten(x int) bool { return g.written[g.findIdent(x)] }

// external marks a result that came from outside this body.
//
// **Borrowed, but not written.** A parameter, a call's result, a static read
// are handed over by value: whoever else holds the original changes *their*
// array, not this one. Marking them written made every variable that is ever
// rebound from one -- `$name = strtolower($name)` -- look written, which on
// Twig was half of all parameters. Only a result nothing here understands (an
// unknown operation) may be an alias of storage someone else writes.
func (g *ownGraph) external(x int, reason string) {
	g.addReason(x, reason)
	g.borrow(x)
	if sinkWrites(reason) {
		g.write(x)
	}
}

// sinkWrites is which untracked destinations may change a value in place.
// Handing an array to a callee by value, returning it or yielding it gives the
// other side a copy, so those do not; a static, a global, a by-reference
// capture or an operation nothing here knows might.
func sinkWrites(reason string) bool {
	switch reason {
	case "static", "global", "closure", "ref":
		return true
	}
	for _, p := range []string{"op:", "intrinsic:", "term:"} {
		if strings.HasPrefix(reason, p) {
			return true
		}
	}
	return false
}

// sink records that a value goes somewhere untracked. A terminal sink moves
// the value out of a frame that dies with it; any other one is another owner.
func (g *ownGraph) sink(x int, reason string, terminal bool) {
	g.addReason(x, reason)
	if !terminal {
		g.holds[g.findIdent(x)]++
	}
	if sinkWrites(reason) {
		g.write(x)
	}
}

// contain makes a value an element of a container, which is one more owner.
func (g *ownGraph) contain(value, container int) {
	i := g.findIdent(value)
	g.holds[i]++
	g.contained[i] = true
	g.union(value, g.elements(container))
}

// extract makes a result some element of a container. The element is still
// the container's, so the result is borrowed.
func (g *ownGraph) extract(result, container int) {
	e := g.elements(container)
	g.extracted[e] = true
	g.union(result, e)
	g.extracted[g.find(e)] = true
	g.borrow(result)
}

// ownWalk is the per-body state of one trace.
type ownWalk struct {
	g      *ownGraph
	opts   OwnershipOptions
	sites  []ownSite
	deaths map[ownPos]map[string]bool
	after  map[ownPos]map[string]bool
	pos    ownPos
	line   string
	// Candidates, decided once the whole graph exists.
	stores  []Move
	concats []ownConcat
	copies  [][2]string
	// literals maps each literal site's token to where it was written.
	literals map[string]LiteralUse
	returns  []string
	params   map[int]string
}

type ownConcat struct {
	pos          ownPos
	left, result string
}

// dies reports that a value's last use is the current instruction.
func (w *ownWalk) dies(tok string) bool { return w.deaths[w.pos][tok] }

// argTokens is ownTokens for one argument, plus a synthetic token -- and an
// allocation site -- for an array literal written inline. `write_var($a, [])`
// allocates as surely as `array.new` does, and without a node the literal had
// no identity for a write or a copy to be counted against.
func (w *ownWalk) argTokens(arg string, slot int) []string {
	toks := ownTokens(arg)
	lit := strings.TrimSpace(arg)
	for _, mode := range []string{"byval ", "byref ", "preferref "} {
		lit = strings.TrimPrefix(lit, mode)
	}
	if strings.HasPrefix(lit, "[") {
		tok := fmt.Sprintf("[literal bb%d.%d.%d]", w.pos.Block, w.pos.Index, slot)
		if _, seen := w.g.node[tok]; !seen {
			w.sites = append(w.sites, ownSite{tok, "literal", "", w.line})
			w.g.mkSite(w.g.of(tok))
			w.literals[tok] = LiteralUse{w.pos.Block, w.pos.Index, lit}
		}
		toks = append(toks, tok)
	}
	return toks
}

// TraceOwnership traces every allocation in a body to its owners.
func TraceOwnership(fn *Function, opts OwnershipOptions) []Ownership {
	return Trace(fn, opts).Sites
}

// Trace runs the ownership trace over one body.
func Trace(fn *Function, opts OwnershipOptions) *OwnershipTrace {
	w := &ownWalk{g: newOwnGraph(opts.FileBody), opts: opts, params: map[int]string{},
		literals: map[string]LiteralUse{}}
	w.deaths, w.after = valueLiveness(fn)
	g := w.g
	opaque := ownershipOpaque(fn)
	blocks := map[int]*Block{}
	for _, b := range fn.Blocks {
		blocks[b.ID] = b
	}
	sinkAll := func(toks []string, reason string) {
		for _, t := range toks {
			g.sink(g.of(t), reason, false)
		}
	}
	// A by-reference send lets the callee change the argument in place. A
	// `preferref` send is by reference only when the callee's parameter is,
	// and counting every one as a write made half of all parameters look
	// written on Twig -- a method call's arguments are all `preferref`.
	writesByRef := func(in Inst) {
		for i, a := range splitOwnArgs(in.ArgList) {
			a = strings.TrimSpace(a)
			byRef := strings.HasPrefix(a, "byref ")
			if strings.HasPrefix(a, "preferref ") {
				byRef = opts.ByRefArg == nil || opts.ByRefArg(in, i)
			}
			if byRef {
				for _, t := range w.argTokens(a, i) {
					g.write(g.of(t))
				}
			}
		}
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for idx, in := range b.Insts {
			w.pos = ownPos{b.ID, idx}
			if in.Directive() {
				if len(in.Operands) > 0 {
					w.line = in.Operands[0]
				}
				continue
			}
			res := ""
			if in.Result != nil {
				res = in.Result.String()
			}
			switch in.Opcode {
			case "new":
				class := ""
				if len(in.Operands) > 0 {
					class = strings.Trim(in.Operands[0], `"`)
				}
				g.classOf[res] = class
				w.sites = append(w.sites, ownSite{res, "new", class, w.line})
				g.mkSite(g.of(res))
			case "clone":
				w.sites = append(w.sites, ownSite{res, "clone", "", w.line})
				g.mkSite(g.of(res))
				// The engine runs `__clone` on the copy, invisibly.
				if opts.LeaksThis == nil || opts.LeaksThis("__clone") {
					g.sink(g.of(res), "clone.this", false)
				}
			case "call.ctor":
				recv := ""
				if len(in.Operands) > 0 {
					recv = strings.TrimSpace(in.Operands[0])
				}
				if recv != "" && (opts.CtorLeaksThis == nil || opts.CtorLeaksThis(g.classOf[recv])) {
					g.sink(g.of(recv), "ctor.this", false)
				}
				writesByRef(in)
				for i, a := range splitOwnArgs(in.ArgList) {
					w.argTokens(a, i)
				}
				// A constructor argument is contained in the new object --
				// and, with no per-method argument summary, may also be kept
				// anywhere else, which is the sink.
				if opts.ArgKept == nil || opts.ArgKept("__construct") {
					for i, a := range splitOwnArgs(in.ArgList) {
						for _, t := range w.argTokens(a, i) {
							if recv != "" {
								g.contain(g.of(t), g.of(recv))
							}
							g.sink(g.of(t), "ctor.arg", false)
						}
					}
				}
			case "call.method":
				name := ""
				if len(in.Operands) > 1 {
					name = strings.Trim(in.Operands[1], `"`)
				}
				if len(in.Operands) > 0 && (opts.LeaksThis == nil || opts.LeaksThis(name)) {
					sinkAll(ownTokens(in.Operands[0]), "method.this")
				}
				writesByRef(in)
				for i, a := range splitOwnArgs(in.ArgList) {
					toks := w.argTokens(a, i)
					if opts.ArgKept == nil || opts.ArgKept(name) {
						sinkAll(toks, "method.arg")
					}
				}
				if res != "" {
					switch {
					case objectFactories[strings.ToLower(name)]:
						w.sites = append(w.sites, ownSite{res, "factory", "", w.line})
						g.mkSite(g.of(res))
					case opts.MethodReturnsFresh != nil && opts.MethodReturnsFresh(name):
						w.sites = append(w.sites, ownSite{res, "call", "", w.line})
						g.mkSite(g.of(res))
					default:
						g.external(g.of(res), "call.result")
					}
				}
			case "call.fn":
				if len(in.Operands) > 0 {
					// The callee itself: `$o()` reaches `__invoke`.
					sinkAll(ownTokens(strings.Join(in.Operands, " ")), "call.callee")
				}
				writesByRef(in)
				names := CalleeNames(in)
				for i, a := range splitOwnArgs(in.ArgList) {
					toks := w.argTokens(a, i)
					if opts.KeepsArg == nil || opts.KeepsArg(names, i) {
						sinkAll(toks, "call.arg")
					}
				}
				if res != "" {
					if opts.FnReturnsFresh != nil && opts.FnReturnsFresh(names) {
						w.sites = append(w.sites, ownSite{res, "call", "", w.line})
						g.mkSite(g.of(res))
					} else {
						g.external(g.of(res), "call.result")
					}
				}
			case IntrinsicCall:
				w.intrinsic(in, res)
			case SlotSet, "store":
				if len(in.Operands) == 2 {
					for _, t := range ownTokens(in.Operands[1]) {
						g.same(g.of(strings.TrimSpace(in.Operands[0])), g.of(t))
					}
				}
			case "local.get", "place.get", "separate", "make_ref":
				if res != "" && len(in.Operands) > 0 {
					for _, t := range ownTokens(in.Operands[0]) {
						g.same(g.of(res), g.of(t))
					}
				}
			case "bind_ref", "bind_ref.loop":
				toks := ownTokens(strings.Join(in.Operands, " "))
				for _, t := range toks[min(1, len(toks)):] {
					g.same(g.of(toks[0]), g.of(t))
				}
			case "bind_lexical":
				// The closure captures the variable: by value it is an element
				// of the closure, by reference the closure shares the place --
				// and can change it whenever it runs.
				toks := ownTokens(strings.Join(in.Operands, " "))
				if len(toks) == 2 {
					g.contain(g.of(toks[1]), g.of(toks[0]))
					if strings.Contains(in.String(), "use=ref") {
						g.write(g.of(toks[1]))
					}
				} else {
					sinkAll(toks, "closure")
				}
			case "closure":
				if res != "" {
					g.of(res)
				}
			case "bind_static", "static.set":
				sinkAll(ownTokens(strings.Join(in.Operands, " ")), "static")
			case "bind_global":
				if len(in.Operands) > 0 {
					sinkAll(ownTokens(in.Operands[0]), "global")
				}
			case "recv", "recv.variadic":
				g.external(g.of(res), "param")
				if len(in.Operands) > 0 {
					var n int
					if _, err := fmt.Sscan(in.Operands[0], &n); err == nil {
						w.params[n] = res
					}
				}
			case "this":
				g.external(g.of(res), "this")
			case ReleasePrev, "unset", "instanceof", "class_name", "get_class",
				"isset", "empty", "class_const", "const.fetch", "const.defined",
				"fetch_class", "silence_begin", "silence_end", "static.bound",
				"discard_exception", "exc.pending", "exc.restore", "exc.release", "tick", "declare_class", "declare_function",
				"declare_const", "declare_anon_class", "verify_return.none",
				"verify_never", "assertions_enabled", "called_class", "fiber.current":
				// Observes its operands, or none. A result is a fresh scalar or
				// a class reference, which holds nothing.
			default:
				// Unknown: everything mentioned escapes and may be changed,
				// and the result is something this analysis did not make.
				sinkAll(ownTokens(strings.Join(in.Operands, " ")+" "+in.ArgList), "op:"+in.Opcode)
				if res != "" {
					g.external(g.of(res), "op:"+in.Opcode)
				}
			}
		}
		w.pos = ownPos{b.ID, len(b.Insts)}
		w.term(b.Term, blocks)
	}
	t := &OwnershipTrace{Sites: ownershipResults(g, w.sites, opaque), graph: g}
	if opaque {
		return t
	}
	w.decide(fn, t)
	return t
}

func (w *ownWalk) intrinsic(in Inst, res string) {
	g, opts := w.g, w.opts
	name := ""
	if len(in.Operands) > 0 {
		name = strings.Trim(in.Operands[0], `"`)
	}
	args := splitOwnArgs(in.ArgList)
	rule, known := ownershipRules[name]
	if !known {
		for i, a := range args {
			for _, t := range w.argTokens(a, i) {
				g.sink(g.of(t), "intrinsic:"+name, false)
			}
		}
		if res != "" {
			g.external(g.of(res), "intrinsic:"+name)
		}
		return
	}
	arg := func(i int) []string {
		if i < 0 || i >= len(args) {
			return nil
		}
		return w.argTokens(args[i], i)
	}
	// Every argument, so a literal in a position the rule only observes --
	// `count([1, 2])` -- is still an allocation site with a node.
	for i := range args {
		arg(i)
	}
	if (rule.ReEnters || catalogueReEnters[name]) && (opts.ReEntry == nil || opts.ReEntry(name)) {
		for i := range args {
			for _, t := range arg(i) {
				g.sink(g.of(t), "reenter:"+name, false)
			}
		}
	}
	if len(rule.Alias) == 2 {
		for _, dst := range arg(rule.Alias[0]) {
			for _, src := range arg(rule.Alias[1]) {
				g.same(g.of(dst), g.of(src))
			}
		}
		if name == "write_var" && len(args) == 2 {
			for _, dst := range ownTokens(args[0]) {
				for _, src := range arg(1) {
					w.stores = append(w.stores, Move{w.pos.Block, w.pos.Index, src, dst})
				}
			}
		}
	}
	if res != "" {
		switch {
		case rule.Fresh:
			w.sites = append(w.sites, ownSite{res, "array", "", w.line})
			g.mkSite(g.of(res))
		case rule.External != "":
			g.external(g.of(res), rule.External)
		case rule.Result == EdgeForward:
			for _, t := range arg(rule.From) {
				g.same(g.of(res), g.of(t))
			}
		case rule.Result == EdgeExtract, rule.Result == EdgePartOf:
			for _, t := range arg(rule.From) {
				g.extract(g.of(res), g.of(t))
			}
		}
	}
	for _, i := range rule.Writes {
		for _, t := range arg(i) {
			// **Building is not writing.** `%2 = array.append(%1, %0)` changes
			// `%1` in place, but `%1` dies here and `%2` is the same identity:
			// nothing else can see the array before it is finished. Only a
			// container that is still live afterwards -- shared with a later
			// reader -- has been written.
			if rule.Result == EdgeForward && rule.Into < 0 && strings.HasPrefix(t, "%") && w.dies(t) {
				continue
			}
			g.write(g.of(t))
		}
	}
	into := func() []int {
		if rule.Into < 0 {
			if res == "" {
				return nil
			}
			return []int{g.of(res)}
		}
		var out []int
		for _, t := range arg(rule.Into) {
			out = append(out, g.of(t))
		}
		return out
	}
	for _, i := range rule.Contain {
		for _, c := range into() {
			for _, t := range arg(i) {
				g.contain(g.of(t), c)
			}
		}
	}
	for _, i := range rule.Splice {
		for _, c := range into() {
			for _, t := range arg(i) {
				g.union(g.elements(g.of(t)), g.elements(c))
			}
		}
	}
	for _, i := range rule.Sink {
		for _, t := range arg(i) {
			g.sink(g.of(t), rule.Reason, false)
		}
	}
	if name == "copy" && res != "" && len(args) == 1 {
		if toks := arg(0); len(toks) == 1 {
			w.copies = append(w.copies, [2]string{res, toks[0]})
		}
	}
	if name == "concat" && res != "" && len(args) == 2 {
		if left := ssaTokens(args[0]); len(left) == 1 && strings.TrimSpace(args[0]) == left[0] {
			w.concats = append(w.concats, ownConcat{w.pos, left[0], res})
		}
	}
}

// term applies a block terminator: a return, throw or yield sends what it
// mentions out, and a jump forwards its arguments to the target's parameters.
func (w *ownWalk) term(term string, blocks map[int]*Block) {
	g := w.g
	term = strings.TrimSpace(term)
	if term == "" {
		return
	}
	word := term
	if i := strings.IndexAny(term, " ("); i >= 0 {
		word = term[:i]
	}
	rest := strings.TrimPrefix(term, word)
	toks := func() []string { return w.argTokens(rest, 0) }
	sinkTerm := func(reason string, terminal bool) []string {
		ts := toks()
		for _, t := range ts {
			g.sink(g.of(t), reason, terminal)
		}
		return ts
	}
	switch word {
	case "ret", "ret.ref":
		w.returns = append(w.returns, sinkTerm("return", true)...)
		if word == "ret.ref" {
			// A reference return hands out the place, not a value.
			w.returns = append(w.returns, "$ref")
		}
	case "throw":
		sinkTerm("throw", true)
	case "yield", "yield_from":
		// The frame survives a yield, so the consumer is another owner.
		sinkTerm("yield", false)
	case "fiber.suspend", "call.suspending", "ret.call", "ret.suspend":
		sinkTerm("suspend", false)
	case "jmp", "br":
		for _, target := range ownTargets(rest) {
			b := blocks[target.block]
			if b == nil {
				continue
			}
			for i, a := range target.args {
				if i >= len(b.Params) {
					break
				}
				param := ownTokens(b.Params[i])
				if len(param) == 0 {
					continue
				}
				for _, t := range w.argTokens(a, i) {
					g.same(g.of(param[0]), g.of(t))
				}
			}
		}
	case "rethrow", "match", "match.error", "generator_create", "unreachable":
		// Observes, or transfers nothing.
	default:
		sinkTerm("term:"+word, false)
	}
}

// decide answers the flow questions once the graph is complete: which stores
// are moves, which concatenations extend their left operand, whether the body
// returns something fresh, and which parameters it writes.
func (w *ownWalk) decide(fn *Function, t *OwnershipTrace) {
	g := w.g
	// unowned is an identity nothing but `place` holds: no other local, no
	// container, no untracked destination.
	unowned := func(id int, place string) bool {
		if g.holds[id] > 0 {
			return false
		}
		for p := range g.places[id] {
			if p != place {
				return false
			}
		}
		return true
	}
	// aliasLive reports another name for the same identity still read after
	// the instruction, which would see a change made through the one taken.
	aliasLive := func(p ownPos, id int, except ...string) bool {
		for v := range w.after[p] {
			if slices.Contains(except, v) {
				continue
			}
			if n, ok := g.node[v]; ok && g.findIdent(n) == id {
				return true
			}
		}
		return false
	}
	for _, s := range w.stores {
		if !strings.HasPrefix(s.Value, "%") && !strings.HasPrefix(s.Value, "[literal") {
			continue
		}
		if strings.HasPrefix(s.Value, "%") && !w.deaths[ownPos{s.Block, s.Index}][s.Value] {
			continue
		}
		id := g.findIdent(g.of(s.Value))
		if !g.site[id] || g.borrowed[id] || !unowned(id, s.Place) ||
			aliasLive(ownPos{s.Block, s.Index}, id, s.Value) {
			continue
		}
		t.Moves = append(t.Moves, s)
	}
	for _, c := range w.concats {
		if !w.deaths[c.pos][c.left] {
			continue
		}
		id := g.findIdent(g.of(c.left))
		// The concat's own result is the chain's next value, not an alias of
		// the old one; a loop joins the two through its block parameter.
		// And it must be this body's own string: a parameter's bytes are the
		// caller's too. Held by no **local**, but a container or a callee may
		// hold it: what they hold is a finished string, and extending a
		// buffer only ever writes past the bytes already handed out.
		if !g.borrowed[id] && len(g.places[id]) == 0 && !aliasLive(c.pos, id, c.left, c.result) {
			t.Appends = append(t.Appends, Append{c.pos.Block, c.pos.Index, c.left, c.result})
		}
	}
	// **Shared, not copied** (P3a): an allocation this body made, that
	// nothing writes in place and nothing carries out of the frame, cannot be
	// told apart from a fresh copy of itself. Leaving the frame is refused
	// outright, not just writing: a caller or a callee holding the one
	// prebuilt array could hand it to something that writes it -- a
	// by-reference builtin given a temporary -- where this body cannot see.
	private := func(tok string) bool {
		n, ok := g.node[tok]
		if !ok {
			return false
		}
		id := g.findIdent(n)
		return g.site[id] && !g.borrowed[id] && !g.written[id] && len(g.fate(n)) == 0
	}
	for tok, use := range w.literals {
		if private(tok) {
			t.SharedLiterals = append(t.SharedLiterals, use)
		}
	}
	sort.Slice(t.SharedLiterals, func(i, j int) bool {
		a, b := t.SharedLiterals[i], t.SharedLiterals[j]
		if a.Block != b.Block {
			return a.Block < b.Block
		}
		if a.Index != b.Index {
			return a.Index < b.Index
		}
		return a.Text < b.Text
	})
	for _, c := range w.copies {
		if private(c[1]) {
			t.SharedCopies = append(t.SharedCopies, c[0])
		}
	}
	for _, st := range w.stores {
		if private(st.Value) {
			t.SharedStores = append(t.SharedStores, InstPos{st.Block, st.Index})
		}
	}
	t.ReleaseAfter = w.releasePoints(fn)
	t.ReturnsFresh = w.returnsFresh(fn)
	n := 0
	for i := range w.params {
		n = max(n, i+1)
	}
	t.ParamWritten = make([]bool, n)
	for i := range t.ParamWritten {
		tok, ok := w.params[i]
		t.ParamWritten[i] = !ok || g.isWritten(g.of(tok))
	}
	t.ParamReleasable = make([]bool, n)
	if !frameOutlivesCall(fn) {
		for i := range t.ParamReleasable {
			tok, ok := w.params[i]
			if !ok {
				continue
			}
			releasable := true
			for _, r := range g.fate(g.of(tok)) {
				if !releasableParamReason(r) {
					releasable = false
					break
				}
			}
			t.ParamReleasable[i] = releasable
		}
	}
}

// releasePoints finds the promoted copies that can hand their share back at
// their last use (OwnershipTrace.ReleaseAfter).
//
// A value marked ShareAttr is a holder of its own over storage someone else
// can write. While it lives, a write to that storage separates; once it is
// dead, keeping its share only makes the next writer copy for nobody --
// and in a loop that is a copy per iteration. So each use is checked to take
// the value as a reader, or to share it into a holder of its own, never to
// keep this very holder: a return, a raw slot, an iterator, another SSA name,
// a by-reference send. Then the share is handed back after each instruction
// where liveness says the value dies.
//
// A value that dies on a terminator, or on no instruction along some path,
// keeps its share: that costs a copy, never a wrong answer.
func (w *ownWalk) releasePoints(fn *Function) []ReleasePoint {
	if frameOutlivesCall(fn) {
		return nil
	}
	candidates := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Result != nil && hasAttr(in, ShareAttr) {
				candidates[in.Result.String()] = true
			}
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	refused := map[string]bool{}
	refuse := func(toks []string) {
		for _, t := range toks {
			if candidates[t] {
				refused[t] = true
			}
		}
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Directive() || in.Opcode == ReleasePrev {
				continue
			}
			switch in.Opcode {
			case IntrinsicCall:
				name := strings.Trim(in.Operands[0], `"`)
				rule, known := ownershipRules[name]
				args := splitOwnArgs(in.ArgList)
				for i, a := range args {
					toks := ownTokens(a)
					switch {
					case !known:
						refuse(toks)
					case rule.Result == EdgeForward && rule.From == i:
						// The result is this holder under another name.
						refuse(toks)
					}
				}
			case "call.fn", "call.method", "call.ctor", "call.static", "call.dynamic":
				for i, a := range splitOwnArgs(in.ArgList) {
					a = strings.TrimSpace(a)
					switch {
					case strings.HasPrefix(a, "byref "):
						refuse(ownTokens(a))
					case strings.HasPrefix(a, "preferref "):
						if w.opts.ByRefArg == nil || w.opts.ByRefArg(in, i) {
							refuse(ownTokens(a))
						}
					}
				}
				// A callee value (`$f()`) is not an array.
			case "instanceof", "class_name", "get_class", "isset", "empty", "unset", "new":
			default:
				refuse(ownTokens(strings.Join(in.Operands, " ") + " " + in.ArgList))
			}
		}
		term := strings.TrimSpace(b.Term)
		word := term
		if i := strings.IndexAny(term, " ("); i >= 0 {
			word = term[:i]
		}
		switch word {
		case "br", "match", "match.error":
			// The condition or subject is read; the edge arguments go on
			// under the target's parameter names.
			for _, t := range ownTargets(term) {
				for _, a := range t.args {
					refuse(ownTokens(a))
				}
			}
		case "jmp":
			for _, t := range ownTargets(term) {
				for _, a := range t.args {
					refuse(ownTokens(a))
				}
			}
		default:
			refuse(ownTokens(term))
		}
	}
	var out []ReleasePoint
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for i := range b.Insts {
			for v := range w.deaths[ownPos{b.ID, i}] {
				if candidates[v] && !refused[v] {
					out = append(out, ReleasePoint{b.ID, i, v})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Block != out[j].Block {
			return out[i].Block < out[j].Block
		}
		if out[i].Index != out[j].Index {
			return out[i].Index < out[j].Index
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// releasableParamReason is how a parameter may leave the frame without taking
// the holder it arrived as along: every one of these shares into a holder of
// its own (the callee's parameter, a store), or concerns only an object.
func releasableParamReason(r string) bool {
	switch r {
	case "param", "call.arg", "method.arg", "ctor.arg", "method.this", "ctor.this":
		return true
	}
	return strings.HasPrefix(r, "reenter:")
}

// frameOutlivesCall reports a body whose frame survives returning to its
// caller -- a generator, a resumable body -- so nothing may be released at
// the exit of one call.
func frameOutlivesCall(fn *Function) bool {
	if len(fn.States) > 0 || fn.SuspendsToCaller {
		return true
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		term := strings.TrimSpace(b.Term)
		if strings.HasPrefix(term, "yield") || strings.HasPrefix(term, "generator_create") {
			return true
		}
	}
	return false
}

// returnsFresh reports a body whose every return is an allocation it made and
// handed out alone -- or a scalar, which nothing can share.
func (w *ownWalk) returnsFresh(fn *Function) bool {
	g := w.g
	if fn.ReturnsRef || len(fn.States) > 0 || fn.SuspendsToCaller {
		return false
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		term := strings.TrimSpace(b.Term)
		if strings.HasPrefix(term, "yield") || strings.HasPrefix(term, "generator_create") {
			// A generator's call result is the generator, not its return.
			return false
		}
	}
	for _, tok := range w.returns {
		if tok == "$ref" {
			return false
		}
		// Not borrowed is enough: an allocation site, or the result of an
		// operation that answers a new value -- a sum, a concatenation. Only
		// something this body did not make can be held elsewhere.
		n := g.of(tok)
		if g.borrowed[g.findIdent(n)] {
			return false
		}
		for _, r := range g.fate(n) {
			if r != "return" {
				return false
			}
		}
	}
	return true
}

// fate is every reason a node escapes, through its containers.
func (g *ownGraph) fate(x int) []string {
	containers := g.containers()
	root := g.find(x)
	reasons := map[string]bool{}
	seen := map[int]bool{root: true}
	for queue := []int{root}; len(queue) > 0; {
		y := queue[0]
		queue = queue[1:]
		for r := range g.reasons[y] {
			reasons[r] = true
		}
		for _, c := range containers[y] {
			if !seen[c] {
				seen[c] = true
				queue = append(queue, c)
			}
		}
	}
	out := make([]string, 0, len(reasons))
	for r := range reasons {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// containers maps each escape class to the classes whose contents it is.
func (g *ownGraph) containers() map[int][]int {
	out := map[int][]int{}
	for x := range g.parent {
		if g.find(x) != x || g.contents[x] < 0 {
			continue
		}
		c := g.find(g.contents[x])
		out[c] = append(out[c], x)
	}
	return out
}

type ownSite struct {
	tok, kind, class, line string
}

// ownershipResults computes each site's fate through its containers.
func ownershipResults(g *ownGraph, sites []ownSite, opaque bool) []Ownership {
	containers := g.containers()
	var out []Ownership
	for _, s := range sites {
		tok := s.tok
		o := Ownership{Site: tok, Kind: s.kind, Class: s.class, Line: s.line}
		if opaque {
			o.Escapes = []string{"opaque"}
			o.Written = true
			out = append(out, o)
			continue
		}
		root := g.find(g.of(tok))
		// Breadth-first up the containment graph: every container reached
		// contributes its reasons, and the first one with a reason gives the
		// chain.
		reasons := map[string]bool{}
		seen := map[int]bool{root: true}
		from := map[int]int{}
		queue := []int{root}
		firstEscaping := -1
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			for r := range g.reasons[x] {
				reasons[r] = true
			}
			if firstEscaping < 0 && len(g.reasons[x]) > 0 {
				firstEscaping = x
			}
			for _, c := range containers[x] {
				if !seen[c] {
					seen[c] = true
					from[c] = x
					queue = append(queue, c)
				}
			}
		}
		for r := range reasons {
			o.Escapes = append(o.Escapes, r)
		}
		sort.Strings(o.Escapes)
		for x := firstEscaping; x >= 0 && x != root; x = from[x] {
			o.Via = append(o.Via, g.display(x))
		}
		for i, j := 0, len(o.Via)-1; i < j; i, j = i+1, j-1 {
			o.Via[i], o.Via[j] = o.Via[j], o.Via[i]
		}
		id := g.findIdent(g.of(tok))
		o.Owners = len(g.places[id]) + g.holds[id]
		// Held by a container something was read out of: the read may be
		// another owner, and which element it took is not known.
		if g.contained[id] && g.extracted[root] {
			o.Owners++
		}
		o.Shared = o.Owners > 1
		o.Written = g.written[id]
		out = append(out, o)
	}
	return out
}

// display names an identity by a place it lives in when it has one, since
// `$list` reads better than the value that first held it.
func (g *ownGraph) display(r int) string {
	best, bestPlace := "", ""
	for tok, n := range g.node {
		if g.find(n) != r {
			continue
		}
		if strings.HasPrefix(tok, "$") && (bestPlace == "" || tok < bestPlace) {
			bestPlace = tok
		}
		if best == "" || tok < best {
			best = tok
		}
	}
	if bestPlace != "" {
		return bestPlace
	}
	if best == "" {
		return g.name[r]
	}
	return best
}

// ownershipOpaque reports a body that can name a local indirectly.
func ownershipOpaque(fn *Function) bool {
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return true
		}
	}
	return strings.Contains(text, "dynvar")
}

// ownTokens extracts the value and place tokens from operand text, skipping
// string literals: `%3`, `$a`, never the `$b` inside `"a$b"`.
func ownTokens(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case c == '%' || c == '$':
			j := i + 1
			for j < len(s) && escapeTokenByte(s[j]) {
				j++
			}
			if j > i+1 && (c == '$' || s[i+1] >= '0' && s[i+1] <= '9') {
				out = append(out, s[i:j])
			}
			i = j - 1
		}
	}
	return out
}

// splitOwnArgs splits a parenthesised argument list at its top-level commas,
// so an array literal or a string holding a comma stays one argument.
func splitOwnArgs(list string) []string {
	s := strings.TrimSpace(list)
	if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
		return nil
	}
	s = s[1 : len(s)-1]
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

type ownTarget struct {
	block int
	args  []string
}

// ownTargets finds every `bbN(args)` a terminator jumps to.
func ownTargets(s string) []ownTarget {
	var out []ownTarget
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
			continue
		}
		if !strings.HasPrefix(s[i:], "bb") || i > 0 && escapeTokenByte(s[i-1]) {
			continue
		}
		j := i + 2
		n := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			n = n*10 + int(s[j]-'0')
			j++
		}
		if j == i+2 {
			continue
		}
		t := ownTarget{block: n}
		if j < len(s) && s[j] == '(' {
			depth, k := 0, j
			for ; k < len(s); k++ {
				if s[k] == '"' {
					for k++; k < len(s) && s[k] != '"'; k++ {
						if s[k] == '\\' {
							k++
						}
					}
					continue
				}
				if s[k] == '(' {
					depth++
				} else if s[k] == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			t.args = splitOwnArgs(s[j:min(k+1, len(s))])
			j = k
		}
		out = append(out, t)
		i = j
	}
	return out
}

// SharedLiteralKey is how Function.SharedLiterals names one literal.
func SharedLiteralKey(block, index int, text string) string {
	return fmt.Sprintf("%d.%d|%s", block, index, strings.TrimSpace(text))
}
