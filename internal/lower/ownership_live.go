package lower

import "strings"

// Where each SSA value dies: the instruction after which nothing reads it
// again. The ownership trace is flow-insensitive, so on its own it can say that
// a value has no *other* owner but not that its current holder is done with it.
// A move needs both, and this supplies the second.
//
// Only `%N` values are tracked. A place (`$x`) is not SSA -- it can be written
// again after any read -- so "dies" means nothing for it, and a caller that
// needs a place to be dead has to ask a different question.

// ownPos names one instruction; Index == len(Insts) is the terminator.
type ownPos struct{ Block, Index int }

// valueDeaths maps each instruction to the values whose last use it is.
func valueDeaths(fn *Function) map[ownPos]map[string]bool {
	deaths, _ := valueLiveness(fn)
	return deaths
}

// GCOnlyResultLive proves that a direct top-level gc_collect_cycles call has
// no other SSA temporary live across it. A live temporary would be an
// unreported root of the compiled program.
func GCOnlyResultLive(fn *Function, result string) bool {
	if fn == nil || fn.Name != "{main}" || result == "" {
		return false
	}
	_, after := valueLiveness(fn)
	for _, b := range fn.Blocks {
		for i, in := range b.Insts {
			if in.Result == nil || in.Result.String() != result {
				continue
			}
			for value := range after[ownPos{b.ID, i}] {
				if value != result {
					return false
				}
			}
			return true
		}
	}
	return false
}

// valueLiveness is valueDeaths plus, per instruction, every value still live
// after it -- which a move needs for the values *aliasing* the one it takes:
// `%2 = copy(%1)` makes two names for one array, and `%2` dying says nothing
// about `%1`.
func valueLiveness(fn *Function) (map[ownPos]map[string]bool, map[ownPos]map[string]bool) {
	blocks := map[int]*Block{}
	for _, b := range fn.Blocks {
		if !b.Dead {
			blocks[b.ID] = b
		}
	}
	uses := func(in Inst) []string {
		text := strings.Join(in.Operands, " ") + " " + in.ArgList + " " + strings.Join(in.Attrs, " ")
		return append(ssaTokens(text), cpsSlotValues(text)...)
	}
	// Successors, with the exception edges folded in: a block inside a try can
	// reach every handler of its region and of every region around it. The
	// edges are over-approximate, which only ever keeps a value alive longer --
	// the direction that cannot invent a move.
	succs := map[int][]int{}
	handlers := func(r int) []int {
		var out []int
		for seen := map[int]bool{}; r >= 0 && r < len(fn.Regions) && !seen[r]; r = fn.Regions[r].Parent {
			seen[r] = true
			for _, h := range []int{fn.Regions[r].Catch, fn.Regions[r].Finally} {
				if h >= 0 {
					out = append(out, h)
				}
			}
		}
		return out
	}
	for id, b := range blocks {
		succs[id] = append(succs[id], b.Succs...)
		succs[id] = append(succs[id], handlers(b.Region)...)
		if b.InCatchBodyOf > 0 {
			succs[id] = append(succs[id], handlers(b.InCatchBodyOf-1)...)
		}
		for _, r := range b.InFinallyOf {
			succs[id] = append(succs[id], handlers(r)...)
		}
		// A terminator may name a block the Succs list does not (a `match`
		// arm): follow what it says too.
		for _, t := range ownTargets(b.Term) {
			succs[id] = append(succs[id], t.block)
		}
	}
	// Block-level use and def sets.
	type sets struct{ use, def map[string]bool }
	bs := map[int]sets{}
	for id, b := range blocks {
		s := sets{use: map[string]bool{}, def: map[string]bool{}}
		for _, p := range b.Params {
			for _, t := range ssaTokens(p) {
				s.def[t] = true
			}
		}
		for _, in := range b.Insts {
			if in.Directive() {
				continue
			}
			for _, t := range uses(in) {
				if !s.def[t] {
					s.use[t] = true
				}
			}
			if in.Result != nil {
				s.def[in.Result.String()] = true
			}
		}
		for _, t := range append(ssaTokens(b.Term), cpsSlotValues(b.Term)...) {
			if !s.def[t] {
				s.use[t] = true
			}
		}
		bs[id] = s
	}
	liveIn := map[int]map[string]bool{}
	liveOut := map[int]map[string]bool{}
	for id := range blocks {
		liveIn[id] = map[string]bool{}
		liveOut[id] = map[string]bool{}
	}
	for changed := true; changed; {
		changed = false
		for id := range blocks {
			for _, s := range succs[id] {
				for v := range liveIn[s] {
					if !liveOut[id][v] {
						liveOut[id][v] = true
						changed = true
					}
				}
			}
			for v := range bs[id].use {
				if !liveIn[id][v] {
					liveIn[id][v] = true
					changed = true
				}
			}
			for v := range liveOut[id] {
				if !bs[id].def[v] && !liveIn[id][v] {
					liveIn[id][v] = true
					changed = true
				}
			}
		}
	}
	out := map[ownPos]map[string]bool{}
	after := map[ownPos]map[string]bool{}
	snapshot := func(p ownPos, live map[string]bool) {
		m := make(map[string]bool, len(live))
		for v := range live {
			m[v] = true
		}
		after[p] = m
	}
	note := func(p ownPos, v string) {
		if out[p] == nil {
			out[p] = map[string]bool{}
		}
		out[p][v] = true
	}
	for id, b := range blocks {
		live := map[string]bool{}
		for v := range liveOut[id] {
			live[v] = true
		}
		snapshot(ownPos{id, len(b.Insts)}, live)
		term := append(ssaTokens(b.Term), cpsSlotValues(b.Term)...)
		for _, v := range term {
			if !live[v] {
				note(ownPos{id, len(b.Insts)}, v)
			}
		}
		for _, v := range term {
			live[v] = true
		}
		for i := len(b.Insts) - 1; i >= 0; i-- {
			in := b.Insts[i]
			if in.Directive() {
				continue
			}
			snapshot(ownPos{id, i}, live)
			if in.Result != nil {
				delete(live, in.Result.String())
			}
			u := uses(in)
			for _, v := range u {
				if !live[v] {
					note(ownPos{id, i}, v)
				}
			}
			for _, v := range u {
				live[v] = true
			}
		}
	}
	return out, after
}

// ssaTokens is ownTokens restricted to SSA values.
func ssaTokens(s string) []string {
	var out []string
	for _, t := range ownTokens(s) {
		if strings.HasPrefix(t, "%") {
			out = append(out, t)
		}
	}
	return out
}

// cpsSlotValues names the values a text reads through their CPS slots:
// `$__cpsN` is `%N`, demoted to the frame because it is live across a
// suspension (demoteAcross, demoteLiveAcross). Read there, the value is
// still in use, and liveness that saw only `%N` ended it at the suspension.
func cpsSlotValues(text string) []string {
	var out []string
	for i := strings.Index(text, "$__cps"); i >= 0; {
		j := i + len("$__cps")
		k := j
		for k < len(text) && text[k] >= '0' && text[k] <= '9' {
			k++
		}
		if k > j {
			out = append(out, "%"+text[j:k])
		}
		next := strings.Index(text[k:], "$__cps")
		if next < 0 {
			break
		}
		i = k + next
	}
	return out
}
