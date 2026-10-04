// Command php2lower lowers a corpus through internal/lower and reports how much
// of it the pass covers.
//
// Coverage is counted in **bodies, not oplines**. One unhandled opline stops a
// whole op_array, so a per-opline rate overstates what a compiler could emit --
// the same distinction the interpreter draws between a test that runs and one
// that hits an unsupported opcode partway through.
//
// A body that cannot be lowered is reported with the opcode that stopped it.
// That histogram is the worklist: it says which opcodes to implement next, in
// the order the corpus actually asks for them.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/moriyoshi/php2go/internal/ir"
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/internal/phpt"
	"github.com/moriyoshi/php2go/internal/zend"
)

func main() {
	var (
		limit    = flag.Int("n", 0, "stop after this many files (0 for all)")
		top      = flag.Int("top", 15, "how many blocking opcodes to report")
		print    = flag.Bool("print", false, "print the lowered IR instead of a summary")
		trace    = flag.Bool("trace", false, "dump the lowerer's own view of every body it cannot lower")
		builders = flag.Bool("builders", false, "report loop-carried string builders instead of coverage")
		verify   = flag.Bool("verify", false, "check IR invariants instead of reporting coverage")
		cps      = flag.Bool("cps", false, "rewrite suspending functions into resumable form")
		graph    = flag.Bool("callgraph", false, "build a whole-corpus call graph and report what it can resolve")
	)
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: php2lower [flags] <dir-or-file>...")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Args(), *limit, *top, *print, *trace, *builders, *verify, *cps, *graph); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(roots []string, limit, top int, printIR, trace, builders, verify, cps, graph bool) error {
	var paths []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() &&
				(strings.HasSuffix(path, ".php") || strings.HasSuffix(path, ".phpt")) {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	sort.Strings(paths)
	if limit > 0 && len(paths) > limit {
		paths = paths[:limit]
	}

	engine, err := zend.Start()
	if err != nil {
		return err
	}
	defer engine.Close()
	scratch, err := os.MkdirTemp("", "php2lower-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	frameless, err := engine.FramelessFunctions()
	if err != nil {
		return err
	}
	cg := lower.NewCallGraph()
	world := lower.NewWorld()
	cg.World = world
	if graph {
		// Declarations first, in a pass of their own. Inference cannot run until
		// every file has been read, and retaining a lowered Function per body
		// until then is what ran the process out of memory on Zend/tests --
		// declarations need only the harvest, so this pass lowers nothing.
		for i, path := range paths {
			if i%500 == 0 && i > 0 {
				fmt.Fprintf(os.Stderr, "  declarations %d/%d\n", i, len(paths))
			}
			if err := engine.Reset(); err != nil {
				return err
			}
			if unit, ok := compile(engine, scratch, path); ok {
				declare(world, ir.EncodeUnit(unit), map[*pb.OpArray]string{})
			}
		}
		world.Close()
	}
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	if trace {
		lw.Trace = os.Stderr
	}

	var total, ok, oplines, insts int
	blockers := map[string]int{}
	var promotable, appendSites, soft int
	rejects := map[string]int{}
	var inferred, sites int
	var checked, suspending int
	violations := map[string]int{}
	var slots, compilerSlots, biggest int
	var biggestName string
	frameKinds := map[string]int{}
	for i, path := range paths {
		if !printIR && i%500 == 0 && i > 0 {
			fmt.Fprintf(os.Stderr, "  %d/%d\n", i, len(paths))
		}
		// Each file must start from a clean engine, or a class declared by an
		// earlier one changes how a later one compiles.
		if err := engine.Reset(); err != nil {
			return err
		}
		unit, found := compile(engine, scratch, path)
		if !found {
			continue
		}
		encoded := ir.EncodeUnit(unit)
		keys := map[*pb.OpArray]string{}
		classOf := map[*pb.OpArray]string{}
		if graph {
			for _, k := range namedOpArrays(encoded, path) {
				keys[k.oa] = k.key
			}
			for _, c := range encoded.GetClasses() {
				for _, m := range c.GetMethods() {
					classOf[m] = c.GetName()
				}
			}
		}
		for _, oa := range allOpArrays(encoded) {
			total++
			fn, err := lw.Lower(oa)
			if err != nil {
				var bail *lower.Bail
				if errors.As(err, &bail) {
					blockers[blockerKey(bail)]++
				}
				continue
			}
			ok++
			// Counted over the lowered bodies only. Instructions come from
			// nowhere else, so measuring them against the whole corpus's
			// oplines would silently fold the coverage rate into the ratio.
			oplines += len(oa.GetOplines())
			for _, b := range fn.Blocks {
				for _, in := range b.Insts {
					if !in.Directive() {
						insts++
					}
				}
				if b.Term != "" {
					insts++
				}
			}
			if builders {
				got, no := lower.FindLoopBuilders(fn)
				for _, b := range got {
					promotable++
					appendSites += b.Appends
				}
				for _, r := range no {
					rejects[r.Why]++
					if r.Soft {
						soft++
					}
				}
			}
			if cps {
				if fnSuspends(fn) {
					suspending++
					if err := lower.Resumable(fn, nil); err != nil {
						violations[err.Error()]++
						continue
					}
				}
				checked++
				if len(fn.Frame) > 0 {
					slots += len(fn.Frame)
					if len(fn.Frame) > biggest {
						biggest, biggestName = len(fn.Frame), fn.Name
					}
					for _, sl := range fn.Frame {
						frameKinds[sl.Type]++
						if sl.Compiler {
							compilerSlots++
						}
					}
				}
				for _, v := range invariants(fn) {
					violations[v]++
				}
				for _, b := range fn.Blocks {
					if strings.HasPrefix(b.Term, "yield") || strings.HasPrefix(b.Term, "fiber.suspend") ||
						strings.HasPrefix(b.Term, "generator_create") {
						violations["a suspension survived the rewrite"]++
					}
				}
				for _, v := range resumedReads(fn) {
					violations[v]++
				}
			}
			if verify {
				checked++
				if fnSuspends(fn) {
					suspending++
				}
				for _, v := range invariants(fn) {
					violations[v]++
				}
			}
			if graph {
				if k, ok := keys[oa]; ok {
					// The graph path does not rewrite, so the marker is taken
					// straight from the suspension terminator rather than from
					// Resumable.
					for _, b := range fn.Blocks {
						if strings.HasPrefix(b.Term, "fiber.suspend") {
							fn.SuspendsToCaller = true
						}
					}
					r, t := lower.InferReceivers(fn, classOf[oa], world)
					inferred += r
					sites += t
					cg.Add(k, fn)
				}
			}
			if printIR {
				fmt.Print(fn)
			}
		}
	}
	if printIR {
		return nil
	}
	if builders {
		reportBuilders(promotable, appendSites, soft, rejects)
		return nil
	}
	if graph {
		cg.Link()
		marked := cg.PropagateMarks()
		fmt.Printf("# Marker propagation\n\nnewly marked by a resolvable `yield from`: %d\n\n", marked)
		fmt.Printf("# Receiver inference\n\nmethod call sites: %d\nreceiver class known: %d  (%s)\n\n",
			sites, inferred, pct(inferred, sites))
		reportGraph(cg)
		return nil
	}
	if verify || cps {
		fmt.Printf("# IR invariants\n\nbodies checked:  %d\nwith suspension: %d\n\n", checked, suspending)
		if cps && suspending > 0 {
			fmt.Printf("## Frames\n\nslots total:      %d  (%.1f per frame)\n",
				slots, float64(slots)/float64(suspending))
			fmt.Printf("compiler slots:   %d  (%s -- narrowable by liveness; the\n"+
				"                  program's own variables are not, since PHP keeps a\n"+
				"                  generator's locals alive until the generator dies)\n",
				compilerSlots, pct(compilerSlots, slots))
			fmt.Printf("largest frame:    %d slots (%s)\n\n", biggest, biggestName)
			type kv struct {
				k string
				n int
			}
			var ks []kv
			for k, n := range frameKinds {
				ks = append(ks, kv{k, n})
			}
			sort.Slice(ks, func(i, j int) bool { return ks[i].n > ks[j].n })
			for _, k := range ks {
				fmt.Printf("  %-10s %6d\n", k.k, k.n)
			}
			fmt.Println()
		}
		if len(violations) == 0 {
			fmt.Println("no violations")
			return nil
		}
		type row struct {
			what string
			n    int
		}
		var rows []row
		for what, n := range violations {
			rows = append(rows, row{what, n})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
		for _, r := range rows {
			fmt.Printf("  %-56s %6d\n", r.what, r.n)
		}
		return nil
	}
	report(total, ok, oplines, insts, blockers, top)
	return nil
}

// blockerKey names a bail for the histogram. The reason is kept alongside the
// opcode when it says something other than "this opcode is not implemented" --
// a body stopped because a value was not live where the pass expected it is a
// different piece of work from one stopped by a missing opcode.
func blockerKey(b *lower.Bail) string {
	if b.Reason == "not lowered" || b.Opcode == "" {
		if b.Opcode == "" {
			return b.Reason
		}
		return b.Opcode
	}
	return fmt.Sprintf("%s (%s)", b.Opcode, b.Reason)
}

func report(total, ok, oplines, insts int, blockers map[string]int, top int) {
	fmt.Printf("# Lowering coverage\n\n")
	fmt.Printf("op_arrays:        %d\n", total)
	fmt.Printf("lowered:          %d  (%s)\n", ok, pct(ok, total))
	fmt.Printf("\nover the lowered bodies:\n")
	fmt.Printf("  oplines in:     %d\n", oplines)
	fmt.Printf("  IR instructions:%d  (%s of the oplines)\n", insts, pct(insts, oplines))

	type row struct {
		what string
		n    int
	}
	var rows []row
	for what, n := range blockers {
		rows = append(rows, row{what, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].what < rows[j].what
	})
	if len(rows) == 0 {
		return
	}
	fmt.Printf("\n## What stopped the rest\n\n")
	shown := rows
	if top > 0 && len(shown) > top {
		shown = shown[:top]
	}
	for _, r := range shown {
		fmt.Printf("  %-52s %6d\n", r.what, r.n)
	}
	if len(rows) > len(shown) {
		var rest int
		for _, r := range rows[len(shown):] {
			rest += r.n
		}
		fmt.Printf("  ... and %d more reasons, %d bodies\n", len(rows)-len(shown), rest)
	}
}

// reportBuilders answers the question the plan gates D3 on: how much is there
// to win, and of what is missed, how much is analysis that could be improved
// versus something the language puts out of reach.
func reportBuilders(promotable, appendSites, soft int, rejects map[string]int) {
	fmt.Printf("# Loop-carried string builders\n\n")
	fmt.Printf("promotable variables: %d\n", promotable)
	fmt.Printf("their append sites:   %d\n", appendSites)
	total := promotable
	for _, n := range rejects {
		total += n
	}
	fmt.Printf("candidates seen:      %d  (%s promotable)\n", total, pct(promotable, total))
	fmt.Printf("  + refused only by the buffer representation: %d  (%s if the target's\n"+
		"    builder can be read without destroying it, which Go's can)\n",
		soft, pct(promotable+soft, total))
	if len(rejects) == 0 {
		return
	}
	type row struct {
		why string
		n   int
	}
	var rows []row
	for why, n := range rejects {
		rows = append(rows, row{why, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].why < rows[j].why
	})
	fmt.Printf("\n## Why the rest were refused\n\n")
	for _, r := range rows {
		fmt.Printf("  %-52s %6d\n", r.why, r.n)
	}
}

// resumedReads checks the property the runtime session raised: a continuation
// that *uses* its resumed value must be reachable only from its own suspension.
//
// Every continuation begins with `frame.resumed`, including the ones that
// discard it -- that read is where a thrown-in exception surfaces, so it has to
// be there even when the value is not wanted. But a continuation can also be a
// join point, and then control can arrive without a resumption having happened.
// Reading a *stale* value there would be wrong; reading and discarding it is
// not, and re-raising is impossible because the driver reports a pending throw
// once and clears it.
//
// So the condition is precise: used implies exactly one predecessor. They
// verified it on four hand-written shapes; this is the same question asked of
// every rewritten body in a corpus.
func resumedReads(fn *lower.Function) []string {
	if len(fn.States) < 2 {
		return nil
	}
	c := lower.Analyze(fn)
	value := regexp.MustCompile(`%\d+`)
	quoted := regexp.MustCompile(`"(\\.|[^"\\])*"`)
	var out []string
	// States[1:Terminal]. The terminal state is a dispatch target but not a
	// continuation: nothing resumes a finished generator, so it reads no resumed
	// value and returns the recorded one instead.
	for _, sN := range fn.States[1:fn.Terminal] {
		blk := fn.Blocks[sN]
		if len(blk.Insts) == 0 || blk.Insts[0].Opcode != "frame.resumed" ||
			blk.Insts[0].Result == nil {
			out = append(out, "a continuation does not begin by reading the resumed value")
			continue
		}
		name := blk.Insts[0].Result.String()
		used := false
		for _, in := range blk.Insts[1:] {
			text := quoted.ReplaceAllString(strings.Join(in.Operands, " ")+" "+in.ArgList, `""`)
			for _, m := range value.FindAllString(text, -1) {
				if m == name {
					used = true
				}
			}
		}
		for _, m := range value.FindAllString(quoted.ReplaceAllString(blk.Term, `""`), -1) {
			if m == name {
				used = true
			}
		}
		if used && len(c.Preds(sN)) > 1 {
			out = append(out,
				"a continuation uses its resumed value and is also reachable without resuming")
		}
	}
	return out
}

// namedOpArrays keys every body the way PHP resolves it: a plain name for a
// function, `class::method` for a method. The lowered Function carries only the
// bare name, so a method's class has to come from the unit structure or every
// `run()` in the corpus collapses into one node.
type keyedOpArray struct {
	key string
	oa  *pb.OpArray
}

func namedOpArrays(unit *pb.Unit, path string) []keyedOpArray {
	var out []keyedOpArray
	var closures func(prefix string, oa *pb.OpArray)
	closures = func(prefix string, oa *pb.OpArray) {
		for i, nested := range oa.GetClosures() {
			k := fmt.Sprintf("%s{closure:%d}", prefix, i)
			out = append(out, keyedOpArray{k, nested})
			closures(k+"/", nested)
		}
	}
	if f := unit.GetFile(); f != nil {
		out = append(out, keyedOpArray{path + "::{main}", f})
		closures(path+"/", f)
	}
	for _, fn := range unit.GetFunctions() {
		out = append(out, keyedOpArray{fn.GetName(), fn})
		closures(fn.GetName()+"/", fn)
	}
	for _, class := range unit.GetClasses() {
		for _, m := range class.GetMethods() {
			k := class.GetName() + "::" + m.GetName()
			out = append(out, keyedOpArray{k, m})
			closures(k+"/", m)
		}
	}
	return out
}

// declare fills the world's declaration tables from one harvested unit, and
// records which class each method belongs to -- the op_array does not carry its
// own scope, so `$this` would otherwise have no type.
func declare(w *lower.World, unit *pb.Unit, classOf map[*pb.OpArray]string) {
	for _, fn := range unit.GetFunctions() {
		if t := fn.GetReturnType(); t != "" {
			w.Returns[strings.ToLower(fn.GetName())] = t
		}
	}
	for _, c := range unit.GetClasses() {
		name := strings.ToLower(c.GetName())
		if p := c.GetParent(); p != "" {
			w.Parent[name] = strings.ToLower(p)
		}
		for _, p := range c.GetProperties() {
			if t := p.GetType(); t != "" {
				w.Props[name+"::"+strings.ToLower(p.GetName())] = t
			}
		}
		for _, m := range c.GetMethods() {
			classOf[m] = c.GetName()
			if t := m.GetReturnType(); t != "" {
				w.Returns[name+"::"+strings.ToLower(m.GetName())] = t
			}
		}
	}
}

func reportGraph(cg *lower.CallGraph) {
	st := cg.Stats()
	fmt.Printf("# Call graph\n\nfunctions:        %d\n", st.Functions)
	fmt.Printf("edges:            %d\n", st.Edges)
	fmt.Printf("suspending:       %d\n", st.Suspending)
	fmt.Printf("with an opaque call site: %d  (%s)  -- call.dynamic or a computed callee\n",
		st.Opaque, pct(st.Opaque, st.Functions))
	fmt.Printf("call sites naming a callee not in the corpus: %d  -- engine builtins, mostly\n",
		st.Unresolved)

	fmt.Printf("edges via a method name alone: %d  (%s)\n",
		st.MethodEdges, pct(st.MethodEdges, st.Edges))

	floor := cg.Reach(false, false)
	withMethods := cg.Reach(true, false)
	sound := cg.Reach(true, true)
	fmt.Printf("\n## How far a suspension spreads\n\n")
	fmt.Printf("  %-52s %5d  (%s)\n", "directly named callees only (a floor)",
		len(floor), pct(len(floor), st.Functions))
	fmt.Printf("  %-52s %5d  (%s)\n", "+ every method of a matching name",
		len(withMethods), pct(len(withMethods), st.Functions))
	fmt.Printf("  %-52s %5d  (%s)\n", "+ opaque calls treated soundly",
		len(sound), pct(len(sound), st.Functions))
	fmt.Printf("\n  the receiver's class is worth %s of the program; opaque calls %s\n",
		pct(len(withMethods)-len(floor), st.Functions),
		pct(len(sound)-len(withMethods), st.Functions))

	fan := cg.FanIn()
	if len(fan) == 0 {
		return
	}
	at := func(q float64) int { return fan[int(float64(len(fan)-1)*q)] }
	fmt.Printf("\n## Blast radius if any one function suspended\n\n")
	fmt.Printf("  transitive callers, median %d, p90 %d, p99 %d, max %d\n",
		at(0.5), at(0.9), at(0.99), fan[len(fan)-1])
	var over int
	for _, v := range fan {
		if v > st.Functions/10 {
			over++
		}
	}
	fmt.Printf("  functions whose blast radius exceeds 10%% of the program: %d (%s)\n",
		over, pct(over, len(fan)))
}

func fnSuspends(fn *lower.Function) bool {
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "yield") || strings.HasPrefix(b.Term, "fiber.suspend") ||
			strings.HasPrefix(b.Term, "generator_create") {
			return true
		}
	}
	return false
}

// invariants checks the structural properties later passes depend on. The
// load-bearing one is that **no SSA value is used outside the block defining
// it**: anything crossing a block boundary is already a place, which is what
// makes the persistent frame of a resumable function exactly its set of places
// rather than a liveness computation over values.
func invariants(fn *lower.Function) []string {
	var out []string
	value := regexp.MustCompile(`%\d+`)
	// A `%N` inside a string literal is text, not a value: `sprintf("%2s")`
	// would otherwise read as a use of %2.
	quoted := regexp.MustCompile(`"(\\.|[^"\\])*"`)
	strip := func(text string) string { return quoted.ReplaceAllString(text, `""`) }
	defined := map[string]int{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Result != nil {
				defined[in.Result.String()] = b.ID
			}
		}
		for _, p := range b.Params {
			if m := value.FindString(p); m != "" {
				defined[m] = b.ID
			}
		}
	}
	seen := map[string]bool{}
	for _, b := range fn.Blocks {
		use := func(text string) {
			for _, m := range value.FindAllString(strip(text), -1) {
				at, ok := defined[m]
				if ok && at != b.ID && !seen[m] {
					seen[m] = true
					out = append(out, "an SSA value is used outside its defining block")
				}
			}
		}
		for _, in := range b.Insts {
			if in.Result != nil && in.Result.String() != "" {
				// Skip the result itself; only operands are uses.
			}
			use(strings.Join(in.Operands, " "))
			use(in.ArgList)
		}
		use(b.Term)
	}
	return out
}

func pct(n, of int) string {
	if of == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(of))
}

func compile(engine *zend.Engine, scratch, path string) (*zend.Unit, bool) {
	source := path
	if strings.HasSuffix(path, ".phpt") {
		test, err := phpt.Parse(path)
		if err != nil || test.File == "" || test.SkipReason() != "" {
			return nil, false
		}
		source = filepath.Join(scratch,
			strings.TrimSuffix(filepath.Base(path), ".phpt")+".php")
		if err := os.WriteFile(source, []byte(test.File), 0o644); err != nil {
			return nil, false
		}
		defer os.Remove(source)
	}
	var unit *zend.Unit
	var err error
	func() {
		// A file the engine refuses is not a lowering failure, and a bailout
		// must not take the survey down with it.
		defer func() { recover() }()
		unit, err = engine.CompileUnit(source)
	}()
	if err != nil || unit == nil {
		return nil, false
	}
	return unit, true
}

func allOpArrays(unit *pb.Unit) []*pb.OpArray {
	var out []*pb.OpArray
	var add func(*pb.OpArray)
	add = func(oa *pb.OpArray) {
		if oa == nil {
			return
		}
		out = append(out, oa)
		for _, nested := range oa.GetClosures() {
			add(nested)
		}
	}
	add(unit.GetFile())
	for _, fn := range unit.GetFunctions() {
		add(fn)
	}
	for _, class := range unit.GetClasses() {
		for _, method := range class.GetMethods() {
			add(method)
		}
		for _, prop := range class.GetProperties() {
			add(prop.GetGetHook())
			add(prop.GetSetHook())
		}
	}
	return out
}
