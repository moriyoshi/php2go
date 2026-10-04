package lower

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// IntRep is the representation of a value carried as a Go int64 rather than a
// boxed php.Value.
const IntRep = "int64"

// IntAttr marks an operation the emitter renders natively on int64 operands.
const IntAttr = "i64"

// IntReps chooses the int64 representation for values proved to be ints, and
// returns how many values and comparisons it made native.
//
// # Which values
//
// A value is carried unboxed when it is certainly an int, and it is defined
// by something that can produce a Go int64 directly:
//
//   - a block parameter whose every incoming edge carries an int;
//   - `++` or `--` on an int that **cannot overflow** (see below), and `+` or
//     `-` of an int and a literal under the same proof;
//   - `%`, `&`, `|` or `^` on two ints, which never leave the ints.
//
// Unlike the float representation, boxing an int64 is exact -- php.Int of it
// is the value PHP holds -- so such a value may be used anywhere, and is
// boxed at each use that wants a php.Value.
//
// # Overflow
//
// `$i++` on PHP_INT_MAX yields a float, so a step is an int only when the
// operand is known to be below the maximum. The fact comes from a branch:
// inside `for ($i = 0; $i < $n; $i++)` the step runs only where `$i < $n`
// held, and with `$n` an int that bounds `$i` by PHP_INT_MAX - 1. So a step
// qualifies when its block is reached only through the true edge of such a
// comparison on the **same SSA value** it steps -- `--` symmetrically, from
// `$n < $i`. An unbounded counter (`$a++` with no test on `$a`) stays boxed.
//
// `$i + c` is a step by c. An int bound only leaves room for 1; a literal
// bound leaves room for whatever lies between it and the edge, so `$i + 2`
// under `$i < 10` is an int and under `$i < PHP_INT_MAX` is not.
//
// # The fixed point is optimistic
//
// A loop counter is a parameter joined from `0` and its own step, and the step
// is an int only if the parameter is. So the pass assumes every candidate is
// an int and removes those that fail, until none does -- the greatest fixed
// point. That is sound by induction over the execution: the values entering a
// loop are ints, and each surviving operation keeps an int an int.
func IntReps(fn *Function, constants map[string]string) int {
	if len(fn.States) > 0 || len(fn.Frame) > 0 || fn.SuspendsToCaller {
		return 0
	}
	r := &intRep{fn: fn, facts: inferTypes(fn, constants), ops: map[string]*intOp{}, params: map[string]*floatParam{}}
	r.collect()
	if len(r.ops) == 0 && len(r.params) == 0 {
		return 0
	}
	r.solve()
	return r.commit()
}

// intOp is one operation that yields an int from int operands.
type intOp struct {
	block, inst int
	name        string // the catalogue signature: Increment, Mod, ...
	args        []string
	// step is the operand a `++`, `--`, `+ c` or `- c` moves, and delta how
	// far; empty for an operation that cannot overflow.
	step  string
	delta int64
}

// intCmp is one comparison that can be rendered on two int64s.
type intCmp struct {
	block, inst int
	name        string
	args        []string
}

type intRep struct {
	fn     *Function
	facts  typeFacts
	cfg    *CFG
	ops    map[string]*intOp
	params map[string]*floatParam
	cmps   []*intCmp
	// byID maps a block's label number to its index.
	byID map[int]int
	// cmpOf is the comparison defining each value, for the branch facts.
	cmpOf map[string]*intCmp
	// ranged are the parameters a version's entry test proved to be ints
	// within AssumedIntRange; a step of one by less than the range's width
	// cannot overflow.
	ranged map[string]bool
	alive  map[string]bool
}

var intOps = map[string]bool{
	"Increment": true, "Decrement": true, "Mod": true,
	"BitAnd": true, "BitOr": true, "BitXor": true,
}

var intCmps = map[string]bool{
	"IsSmaller": true, "IsSmallerOrEqual": true,
	"LooseEqual": true, "NotLooseEqual": true, "StrictEqual": true, "NotStrictEqual": true,
}

func (r *intRep) collect() {
	r.byID = map[int]int{}
	for i, b := range r.fn.Blocks {
		r.byID[b.ID] = i
	}
	r.cmpOf = map[string]*intCmp{}
	r.ranged = map[string]bool{}
	for bi, b := range r.fn.Blocks {
		if b.Dead {
			continue
		}
		for ii, in := range b.Insts {
			if in.Opcode == "recv" && in.Result != nil && len(in.Operands) == 1 && r.fn.assumedInt(in.Operands[0]) {
				r.ranged[in.Result.String()] = true
				// The parameter itself is an int64: the version's entry test
				// proved it, so it arrives unboxed rather than being read out of
				// a php.Value at every use.
				r.ops[in.Result.String()] = &intOp{block: bi, inst: ii, name: "Recv"}
			}
			sig, _, ok := IntrinsicOf(in)
			if !ok || in.Result == nil {
				continue
			}
			args := CallArgs(in)
			switch {
			case intOps[sig.Name] && (len(args) == 2 || len(args) == 1 && (sig.Name == "Increment" || sig.Name == "Decrement")):
				if r.numericOperands(args) {
					op := &intOp{block: bi, inst: ii, name: sig.Name, args: args}
					switch sig.Name {
					case "Increment":
						op.step, op.delta = strings.TrimSpace(args[0]), 1
					case "Decrement":
						op.step, op.delta = strings.TrimSpace(args[0]), -1
					}
					r.ops[in.Result.String()] = op
				}
			case (sig.Name == "Add" || sig.Name == "Sub") && len(args) == 2 && r.numericOperands(args):
				// Only as a step by a literal; see the overflow note above.
				x, c, ok := stepByLiteral(sig.Name, args)
				if ok {
					r.ops[in.Result.String()] = &intOp{block: bi, inst: ii, name: sig.Name, args: args, step: x, delta: c}
				}
			case intCmps[sig.Name] && len(args) == 2:
				if r.numericOperands(args) {
					c := &intCmp{block: bi, inst: ii, name: sig.Name, args: args}
					r.cmps = append(r.cmps, c)
					r.cmpOf[in.Result.String()] = c
				}
			}
		}
	}
	for bi, b := range r.fn.Blocks {
		if b.Dead {
			continue
		}
		for pi, p := range b.Params {
			name, _, _ := strings.Cut(p, ":")
			name = strings.TrimSpace(name)
			if m := r.facts.of(name); m.Known() && m.Mask&^php.MaskNumeric == 0 {
				r.params[name] = &floatParam{block: bi, index: pi}
			}
		}
	}
	for _, b := range r.fn.Blocks {
		if b.Dead {
			continue
		}
		for _, m := range branchWithArgs.FindAllStringSubmatch(b.Term, -1) {
			var to int
			if _, err := fmt.Sscanf(m[1], "bb%d", &to); err != nil {
				continue
			}
			ti, ok := r.byID[to]
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
	for name, p := range r.params {
		if len(p.args) == 0 {
			delete(r.params, name)
		}
	}
	if len(r.ops) > 0 {
		r.cfg = Analyze(r.fn)
	}
}

// numericOperands reports operands that are each an int or a float, or an int
// literal. The fixed point narrows them to ints; this only keeps out what
// cannot be one.
func (r *intRep) numericOperands(args []string) bool {
	for _, a := range args {
		if _, ok := intLiteral(a); ok {
			continue
		}
		a = strings.TrimSpace(a)
		m := r.facts.of(a)
		if !strings.HasPrefix(a, "%") || !m.Known() || m.Mask&^php.MaskNumeric != 0 {
			return false
		}
	}
	return true
}

// stepByLiteral recognises `x + c`, `c + x` and `x - c` for a nonzero int
// literal c and a value x, as a step of x by c or -c.
func stepByLiteral(name string, args []string) (string, int64, bool) {
	a, b := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
	if c, ok := intLiteral(b); ok && strings.HasPrefix(a, "%") && c != 0 && c != math.MinInt64 {
		if name == "Sub" {
			c = -c
		}
		return a, c, true
	}
	if c, ok := intLiteral(a); ok && name == "Add" && strings.HasPrefix(b, "%") && c != 0 && c != math.MinInt64 {
		return b, c, true
	}
	return "", 0, false
}

func intLiteral(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

// isInt reports an operand that is certainly an int under the current
// assumption: an int literal, a candidate still alive, or a value the
// inference already typed as exactly an int.
func (r *intRep) isInt(operand string) bool {
	operand = strings.TrimSpace(operand)
	if _, ok := intLiteral(operand); ok {
		return true
	}
	if r.ops[operand] != nil || r.params[operand] != nil {
		return r.alive[operand]
	}
	m := r.facts.of(operand)
	return strings.HasPrefix(operand, "%") && m.Mask == php.MaskInt
}

func (r *intRep) solve() {
	r.alive = map[string]bool{}
	for name := range r.ops {
		r.alive[name] = true
	}
	for name := range r.params {
		r.alive[name] = true
	}
	for {
		changed := false
		for name, op := range r.ops {
			if !r.alive[name] {
				continue
			}
			ok := true
			for _, a := range op.args {
				ok = ok && r.isInt(a)
			}
			if ok && op.step != "" {
				ok = r.stepBounded(op)
			}
			if !ok {
				delete(r.alive, name)
				changed = true
			}
		}
		for name, p := range r.params {
			if !r.alive[name] {
				continue
			}
			for _, a := range p.args {
				if !r.isInt(a) {
					delete(r.alive, name)
					changed = true
					break
				}
			}
		}
		if !changed {
			return
		}
	}
}

// stepBounded reports a step that cannot overflow: its block is reached only
// through an edge whose comparison bounds its operand, on the side it steps
// towards, far enough from the edge of the ints.
func (r *intRep) stepBounded(op *intOp) bool {
	x := op.step
	// A parameter the entry test bounded: `$n - 1` in a version cannot reach
	// either edge, which is what lets `for ($i = $n - 1; ...)` count in ints.
	if r.ranged[x] && op.delta < AssumedIntRange && op.delta > -AssumedIntRange {
		return true
	}
	for bi, b := range r.fn.Blocks {
		if b.Dead || !strings.HasPrefix(b.Term, "br ") {
			continue
		}
		fields := splitCallArgs(strings.TrimPrefix(b.Term, "br "))
		if len(fields) != 3 {
			continue
		}
		c := r.cmpOf[strings.TrimSpace(fields[0])]
		if c == nil {
			continue
		}
		for edge, target := range fields[1:] {
			if !r.bounds(c, edge == 0, x, op.delta) {
				continue
			}
			label, _, _ := strings.Cut(target, "(")
			var to int
			if _, err := fmt.Sscanf(strings.TrimSpace(label), "bb%d", &to); err != nil {
				continue
			}
			ti, ok := r.byID[to]
			if !ok {
				continue
			}
			// The edge must be the only way into its target, so that the
			// target -- and everything it dominates -- runs only when the
			// comparison answered this way. The graph's predecessors include
			// exception edges, which is the safe direction.
			if preds := r.cfg.Preds(ti); len(preds) != 1 || preds[0] != bi {
				continue
			}
			if r.cfg.Dominates(ti, op.block) {
				return true
			}
		}
	}
	return false
}

// bounds reports whether comparison c, having answered held, leaves room to
// move x by delta without leaving the ints.
//
// Each edge is a fact `lo < hi` or `lo <= hi`: the true edge of `a < b` is
// `a < b`, and its false edge is `b <= a` -- **non-strict**, which is the
// trap. Upwards x needs to be `lo`; a strict bound by any int leaves room for
// 1, and a literal bound for whatever lies between it and PHP_INT_MAX.
// Downwards x needs to be `hi`, mirrored. A non-strict bound by a variable
// leaves no room at all: `$x >= $y` holds with both at PHP_INT_MIN.
func (r *intRep) bounds(c *intCmp, held bool, x string, delta int64) bool {
	lo, hi := strings.TrimSpace(c.args[0]), strings.TrimSpace(c.args[1])
	strict := c.name == "IsSmaller"
	switch c.name {
	case "IsSmaller", "IsSmallerOrEqual":
	default:
		return false
	}
	if !held {
		lo, hi, strict = hi, lo, !strict
	}
	other := hi
	if delta < 0 {
		if hi != x {
			return false
		}
		other = lo
	} else if lo != x {
		return false
	}
	if !r.isInt(other) {
		return false
	}
	l, literal := intLiteral(other)
	if !literal {
		return strict && (delta == 1 || delta == -1)
	}
	switch {
	case delta > 0 && strict:
		// x <= L - 1.
		return l <= math.MaxInt64-delta+1
	case delta > 0:
		return l <= math.MaxInt64-delta
	case strict:
		// x >= L + 1.
		return l >= math.MinInt64+(-1-delta)
	default:
		return l >= math.MinInt64-delta
	}
}

func (r *intRep) commit() int {
	n := 0
	for name := range r.alive {
		if op, ok := r.ops[name]; !ok || op.name != "Recv" {
			// A parameter unboxed by the entry test is not a gain of the
			// version's own: IntVersions weighs what the assumption buys
			// beyond itself.
			n++
		}
		if op, ok := r.ops[name]; ok {
			in := &r.fn.Blocks[op.block].Insts[op.inst]
			in.Result.Type = IntRep
			in.Attrs = append(in.Attrs, IntAttr)
			continue
		}
		p := r.params[name]
		b := r.fn.Blocks[p.block]
		b.Params[p.index] = name + ": " + IntRep
	}
	for _, c := range r.cmps {
		if r.isInt(c.args[0]) && r.isInt(c.args[1]) {
			in := &r.fn.Blocks[c.block].Insts[c.inst]
			in.Attrs = append(in.Attrs, IntAttr)
			// Counted with the values: a comparison rendered natively is
			// what a loop test gains, and IntVersions weighs the count.
			n++
		}
	}
	return n
}
