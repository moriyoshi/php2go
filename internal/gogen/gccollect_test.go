package gogen_test

import (
	"strings"
	"testing"
)

// gc_collect_cycles has no collector behind it: it answers 0 only when
// php.CollectWouldBeObserved says nothing could tell. What the call site and
// the program know -- whether the count is read, whether handles can be
// printed, whether references are made -- is decided here, by the emitter,
// and a wrong `false` turns a refusal into a wrong `0`.
func TestGcCollectCyclesTellsTheRuntimeWhatTheProgramCanObserve(t *testing.T) {
	cases := []struct {
		name, php string
		want      []string
		not       []string
	}{
		{
			name: "count read, references, handles printed",
			php:  `<?php $a = []; $a[] = &$a; var_dump(gc_collect_cycles());`,
			want: []string{"gcCollectCycles(ctx, true,", "ArrayRefs:       true", "HandlesObserved: true", "php.TrackObjects()"},
		},
		{
			name: "count discarded, nothing printed",
			php:  `<?php class C { public $o; } $c = new C; $c->o = $c; unset($c); gc_collect_cycles(); echo "done\n";`,
			want: []string{"gcCollectCycles(ctx, false,", "ArrayRefs:       false", "HandlesObserved: false", "php.TrackObjects()"},
		},
		{
			name: "a reference to a scalar",
			php:  `<?php $x = 1; $y = &$x; echo gc_collect_cycles();`,
			want: []string{"gcCollectCycles(ctx, true,", "ArrayRefs:       true"},
		},
		{
			name: "a by-reference capture",
			php:  `<?php $x = 1; $f = function () use (&$x) { $x++; }; $f(); gc_collect_cycles();`,
			want: []string{"ArrayRefs:       true"},
		},
		{
			name: "handles via spl_object_id",
			php:  `<?php echo spl_object_id(new stdClass); gc_collect_cycles();`,
			want: []string{"HandlesObserved: true"},
		},
		{
			name: "no collection, no registry",
			php:  `<?php class C {} $c = new C; echo "x\n";`,
			not:  []string{"php.TrackObjects()"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := emitSource(t, c.php, "s.php")
			for _, w := range c.want {
				if !strings.Contains(src, w) {
					t.Errorf("emitted Go lacks %q", w)
				}
			}
			for _, n := range c.not {
				if strings.Contains(src, n) {
					t.Errorf("emitted Go has %q", n)
				}
			}
		})
	}
}
