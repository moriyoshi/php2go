package lower

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// FloatRep is the representation of a value carried as a Go float64 rather
// than a boxed php.Value.
const FloatRep = "f64"

// FloatAttr marks an arithmetic intrinsic or a comparison that the emitter
// renders natively on float64 operands: `a * b`, not `php.Mul(a, b)`.
const FloatAttr = "f64"

// FloatReps chooses the float64 representation for arithmetic values that can
// carry one, and returns how many it chose.
//
// # Which values
//
// A value is carried unboxed when two things hold. First, every run-time value
// it can take is a float, or an int from a **small, known set** of constants.
// Second, every use can take a Go float64 directly: the operand of another such
// operation, a `<`/`<=` comparison, or an edge into a block parameter carried
// the same way. A value that is certainly a float may also be used anywhere
// else, boxed at that use. A value that may be an int may not, because boxing
// it would print `float(0)` where PHP prints `int(0)`.
//
// The int case is what makes the pass fire on real loops. `$z = 0; for (...)
// { $z = $z * $z + $c; }` joins the literal `0` with a float, so the
// parameter is `int|float` and a float-only rule stops there. That rule was the
// first draft; on mandel2 it narrowed only 2 of the 11 hot values.
//
// # Why an int can ride as a float
//
// PHP converts an int operand to double whenever the other operand is a float,
// and compares an int with a float as two doubles. So an int `c` carried as
// `float64(c)` behaves identically in those positions, provided the conversion
// is exact. Only int-with-int differs: the engine computes in integers. For
// that case the pass evaluates every combination of the known constants both
// ways, and keeps the value only if integer and double arithmetic agree **bit
// for bit**. That excludes overflow, and it excludes the signed zero:
// `0 * -3` is int 0, but `0.0 * -3.0` is `-0.0`, which echoes as `-0`.
//
// An int with no known value set (a loop counter) never rides as a float
// unless PHP would convert it anyway. `$i * 0.5` converts `$i`; `$i + $j`
// does not, and stays boxed.
//
// # What it refuses
//
// It skips resumable bodies: a value crossing a suspension lives in a frame
// slot, and the CPS rewrite has already chosen those slots' types.
func FloatReps(fn *Function, constants map[string]string) int {
	if len(fn.States) > 0 || len(fn.Frame) > 0 || fn.SuspendsToCaller {
		return 0
	}
	facts := inferTypes(fn, constants)
	r := &floatRep{fn: fn, facts: facts, ops: map[string]*floatOp{}, params: map[string]*floatParam{}, guarded: map[string]*floatGuarded{}}
	r.collect()
	if len(r.ops) == 0 && len(r.cmps) == 0 {
		return 0
	}
	if !r.solveInts() {
		return 0
	}
	r.choose()
	return r.commit()
}

// intSet is the set of int values a numeric value can hold. The zero value
// is the empty set: the value is never an int. top means an int of unknown
// value.
type intSet struct {
	top  bool
	vals []int64
}

// floatIntLimit bounds the constants the pass carries as floats. Below 2^52
// both the value and its neighbours are exact in a double, so converting the
// other operand of a comparison cannot land on the constant by rounding.
const floatIntLimit = 1 << 52

// floatSetLimit is how many distinct constants a set may hold before it
// becomes top. The sets come from literals, so they stay small in practice.
const floatSetLimit = 16

func (s intSet) empty() bool { return !s.top && len(s.vals) == 0 }

func (s intSet) equal(o intSet) bool {
	if s.top != o.top || len(s.vals) != len(o.vals) {
		return false
	}
	for i := range s.vals {
		if s.vals[i] != o.vals[i] {
			return false
		}
	}
	return true
}

func (s intSet) add(v int64) intSet {
	if s.top {
		return s
	}
	if v <= -floatIntLimit || v >= floatIntLimit {
		return intSet{top: true}
	}
	for i, have := range s.vals {
		if have == v {
			return s
		}
		if have > v {
			vals := make([]int64, 0, len(s.vals)+1)
			vals = append(vals, s.vals[:i]...)
			vals = append(vals, v)
			vals = append(vals, s.vals[i:]...)
			return capSet(vals)
		}
	}
	return capSet(append(append([]int64(nil), s.vals...), v))
}

func capSet(vals []int64) intSet {
	if len(vals) > floatSetLimit {
		return intSet{top: true}
	}
	return intSet{vals: vals}
}

func (s intSet) union(o intSet) intSet {
	if s.top || o.top {
		return intSet{top: true}
	}
	for _, v := range o.vals {
		s = s.add(v)
	}
	return s
}

// floatOp is one arithmetic intrinsic whose operands are both numeric.
type floatOp struct {
	block, inst int
	op          string // "+", "-", "*", "/"
	args        []string
	ints        intSet
}

// floatParam is one numeric block parameter.
type floatParam struct {
	block, index int
	args         []string // one per incoming edge
	ints         intSet
}

// floatGuarded is one arithmetic result whose operands are not both numeric.
type floatGuarded struct {
	args []string
	ints intSet
}

// floatCmp is one `<` or `<=` over numeric operands.
type floatCmp struct {
	block, inst int
	args        []string
}

type floatRep struct {
	fn     *Function
	facts  typeFacts
	ops    map[string]*floatOp
	params map[string]*floatParam
	cmps   []*floatCmp
	// guarded are the arithmetic results whose operands are not both known
	// numbers -- `$x * 1.0` on an untyped `$x` -- keyed to their operands.
	// They stay general and boxed, but when either operand is certainly a
	// float so is the result: PHP converts the other side to a number, and
	// a float on either side makes the result a float or an error.
	guarded map[string]*floatGuarded
	// chosen is the candidate set, shrunk to a fixed point by choose.
	chosen map[string]bool
}

var floatArith = map[string]string{"Add": "+", "Sub": "-", "Mul": "*", "Div": "/"}

// numeric reports an operand whose every run-time value is an int or a float.
func (r *floatRep) numeric(operand string) bool {
	m := r.facts.of(operand)
	return m.Known() && m.Mask&^php.MaskNumeric == 0
}

// convertible reports an operand the emitter can render as a float64: a
// finite numeric literal, or an SSA value that is numeric.
func (r *floatRep) convertible(operand string) bool {
	operand = strings.TrimSpace(operand)
	if strings.HasPrefix(operand, "%") {
		return r.numeric(operand)
	}
	if _, err := strconv.ParseInt(operand, 10, 64); err == nil {
		return true
	}
	if f, err := strconv.ParseFloat(operand, 64); err == nil {
		return !math.IsInf(f, 0) && !math.IsNaN(f) && !strings.ContainsAny(operand, "xXpP_")
	}
	return false
}

func (r *floatRep) collect() {
	byID := map[int]int{}
	for i, b := range r.fn.Blocks {
		byID[b.ID] = i
	}
	for bi, b := range r.fn.Blocks {
		for ii, in := range b.Insts {
			sig, _, ok := IntrinsicOf(in)
			if !ok || hasAttrIn(in.Attrs, IntAttr) {
				// Already claimed by the int representation.
				continue
			}
			args := CallArgs(in)
			if len(args) != 2 {
				continue
			}
			numericArgs := r.convertible(args[0]) && r.convertible(args[1])
			if op, arith := floatArith[sig.Name]; arith && in.Result != nil {
				if numericArgs {
					r.ops[in.Result.String()] = &floatOp{block: bi, inst: ii, op: op, args: args}
				} else if r.numeric(in.Result.String()) {
					r.guarded[in.Result.String()] = &floatGuarded{args: args}
				}
			}
			if (sig.Name == "IsSmaller" || sig.Name == "IsSmallerOrEqual") && numericArgs {
				r.cmps = append(r.cmps, &floatCmp{block: bi, inst: ii, args: args})
			}
		}
	}
	for bi, b := range r.fn.Blocks {
		for pi, p := range b.Params {
			name, typ, _ := strings.Cut(p, ":")
			name = strings.TrimSpace(name)
			if !r.numeric(name) || strings.TrimSpace(typ) == IntRep {
				continue
			}
			r.params[name] = &floatParam{block: bi, index: pi}
		}
	}
	if len(r.params) == 0 {
		return
	}
	for _, b := range r.fn.Blocks {
		for _, m := range branchWithArgs.FindAllStringSubmatch(b.Term, -1) {
			var to int
			if _, err := fmt.Sscanf(m[1], "bb%d", &to); err != nil {
				continue
			}
			ti, ok := byID[to]
			if !ok {
				continue
			}
			target := r.fn.Blocks[ti]
			for i, a := range splitCallArgs(m[2]) {
				if i >= len(target.Params) {
					break
				}
				name, _, _ := strings.Cut(target.Params[i], ":")
				if p, ok := r.params[strings.TrimSpace(name)]; ok {
					p.args = append(p.args, strings.TrimSpace(a))
				}
			}
		}
	}
	// A parameter reached by an edge the scan could not read, or fed something
	// that is not a number, is not a candidate.
	for name, p := range r.params {
		ok := len(p.args) > 0
		for _, a := range p.args {
			ok = ok && r.convertible(a)
		}
		if !ok {
			delete(r.params, name)
		}
	}
}

// intsOf is the int set of an operand under the current solution.
func (r *floatRep) intsOf(operand string) intSet {
	operand = strings.TrimSpace(operand)
	if n, err := strconv.ParseInt(operand, 10, 64); err == nil {
		return intSet{}.add(n)
	}
	if !strings.HasPrefix(operand, "%") {
		// A float literal.
		return intSet{}
	}
	if r.facts.of(operand).Mask&php.MaskInt == 0 {
		return intSet{}
	}
	if op, ok := r.ops[operand]; ok {
		return op.ints
	}
	if p, ok := r.params[operand]; ok {
		return p.ints
	}
	if g, ok := r.guarded[operand]; ok {
		// From the solution, not recomputed: recursing through the operands
		// here is exponential in a chain of guarded operations, and zend_bench
		// has chains long enough that emitting it never finished.
		return g.ints
	}
	return intSet{top: true}
}

// floatOnly reports an operand that is certainly a float under the current
// solution. Its mask alone does not say so for a string or a bool, which
// convert to an int, so it has to be numeric as well.
func (r *floatRep) floatOnly(operand string) bool {
	return r.numeric(operand) && r.intsOf(operand).empty()
}

// solveInts computes every candidate's int set as a least fixed point from the
// empty set. It reports false if the iteration did not settle, which would be
// a bug here, and then the pass chooses nothing.
func (r *floatRep) solveInts() bool {
	// In program order, so a round carries facts down a whole chain and the
	// number of rounds follows the loop nesting rather than the chain length.
	var order []string
	for _, b := range r.fn.Blocks {
		for _, p := range b.Params {
			name, _, _ := strings.Cut(p, ":")
			if name = strings.TrimSpace(name); r.params[name] != nil {
				order = append(order, name)
			}
		}
		for _, in := range b.Insts {
			if in.Result == nil {
				continue
			}
			if name := in.Result.String(); r.ops[name] != nil || r.guarded[name] != nil {
				order = append(order, name)
			}
		}
	}
	for round := 0; round < 256; round++ {
		changed := false
		for _, name := range order {
			var next intSet
			var cur *intSet
			switch {
			case r.ops[name] != nil:
				op := r.ops[name]
				if r.facts.of(name).Mask&php.MaskInt != 0 {
					next = intArith(op.op, r.intsOf(op.args[0]), r.intsOf(op.args[1]))
				}
				cur = &op.ints
			case r.guarded[name] != nil:
				g := r.guarded[name]
				if !r.floatOnly(g.args[0]) && !r.floatOnly(g.args[1]) {
					next = intSet{top: true}
				}
				cur = &g.ints
			default:
				p := r.params[name]
				for _, a := range p.args {
					next = next.union(r.intsOf(a))
				}
				cur = &p.ints
			}
			if !next.equal(*cur) {
				*cur, changed = next, true
			}
		}
		if !changed {
			return true
		}
	}
	return false
}

// intArith is the int set of `a op b`: the results PHP computes in integers,
// each checked to be exactly what double arithmetic gives. A combination that
// disagrees makes the set top, which keeps the value boxed.
func intArith(op string, a, b intSet) intSet {
	// An int result needs two int operands; a float on either side converts.
	if a.empty() || b.empty() {
		return intSet{}
	}
	if a.top || b.top {
		return intSet{top: true}
	}
	var out intSet
	for _, x := range a.vals {
		for _, y := range b.vals {
			var c int64
			var d float64
			fx, fy := float64(x), float64(y)
			switch op {
			case "+":
				c, d = x+y, fx+fy
			case "-":
				c, d = x-y, fx-fy
			case "*":
				c, d = x*y, fx*fy
			case "/":
				if y == 0 {
					// Throws, and yields nothing.
					continue
				}
				if x%y != 0 {
					// PHP answers a float, which the representation carries
					// as itself.
					continue
				}
				c, d = x/y, fx/fy
			}
			if math.Float64bits(float64(c)) != math.Float64bits(d) {
				return intSet{top: true}
			}
			if out = out.add(c); out.top {
				return out
			}
		}
	}
	return out
}

// valueRef finds SSA value names in operand text.
var valueRef = regexp.MustCompile(`%\d+`)

// choose shrinks the candidate set to the values whose every use can take a
// float64.
func (r *floatRep) choose() {
	r.chosen = map[string]bool{}
	for name, op := range r.ops {
		if !op.ints.top {
			r.chosen[name] = true
		}
	}
	for name, p := range r.params {
		if !p.ints.top {
			r.chosen[name] = true
		}
	}
	for {
		allowed := r.allowedUses()
		total := map[string]int{}
		count := func(text string) {
			for _, ref := range valueRef.FindAllString(text, -1) {
				if r.chosen[ref] {
					total[ref]++
				}
			}
		}
		for _, b := range r.fn.Blocks {
			for _, in := range b.Insts {
				for _, o := range in.Operands {
					count(o)
				}
				count(in.ArgList)
				for _, a := range in.Attrs {
					count(a)
				}
			}
			count(b.Term)
		}
		changed := false
		for name := range r.chosen {
			if total[name] > allowed[name] && !r.certainlyFloat(name) {
				delete(r.chosen, name)
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

// certainlyFloat reports a chosen value that is never an int, so boxing it
// anywhere is `php.Float` of what it holds.
func (r *floatRep) certainlyFloat(name string) bool {
	if op, ok := r.ops[name]; ok {
		return op.ints.empty()
	}
	if p, ok := r.params[name]; ok {
		return p.ints.empty()
	}
	return false
}

// nativeCmp reports a comparison the emitter renders on float64s: one that
// has a chosen operand to gain from.
func (r *floatRep) nativeCmp(c *floatCmp) bool {
	return r.chosen[strings.TrimSpace(c.args[0])] || r.chosen[strings.TrimSpace(c.args[1])]
}

// allowedUses counts, per chosen value, the uses that take a float64 as it is.
func (r *floatRep) allowedUses() map[string]int {
	allowed := map[string]int{}
	note := func(a string) {
		if a = strings.TrimSpace(a); r.chosen[a] {
			allowed[a]++
		}
	}
	for name, op := range r.ops {
		if r.chosen[name] {
			note(op.args[0])
			note(op.args[1])
		}
	}
	for _, c := range r.cmps {
		if r.nativeCmp(c) {
			note(c.args[0])
			note(c.args[1])
		}
	}
	for name, p := range r.params {
		if !r.chosen[name] {
			continue
		}
		for _, a := range p.args {
			note(a)
		}
	}
	return allowed
}

func (r *floatRep) commit() int {
	for name := range r.chosen {
		if op, ok := r.ops[name]; ok {
			in := &r.fn.Blocks[op.block].Insts[op.inst]
			in.Result.Type = FloatRep
			in.Attrs = append(in.Attrs, FloatAttr)
			continue
		}
		p := r.params[name]
		b := r.fn.Blocks[p.block]
		b.Params[p.index] = name + ": " + FloatRep
	}
	for _, c := range r.cmps {
		if r.nativeCmp(c) {
			in := &r.fn.Blocks[c.block].Insts[c.inst]
			in.Attrs = append(in.Attrs, FloatAttr)
		}
	}
	return len(r.chosen)
}
