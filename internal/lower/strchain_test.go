package lower_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/internal/lower"
)

// The rewrite fires on the loop-carried chain and on nothing the trace cannot
// prove; each refusal is a way an in-place append would change what prints.
func TestStringChains(t *testing.T) {
	for _, c := range []struct {
		name, src string
		rewrites  bool
	}{
		{"loop-carried", `function t($n) { $s = ""; while ($n-- > 0) { $s .= "x"; } return strlen($s); }`, true},
		{"read in the loop", `function t($n) { $s = "a"; while ($n-- > 0) { echo $s; $s .= "x"; } return $s; }`, true},
		{"stored each time round", `function t($n) { $s = ""; $all = []; while ($n-- > 0) { $s .= "x"; $all[] = $s; } return $all; }`, true},
		// Entered with an int: converting it at the loop's entry would print
		// "0" for a loop that never ran, where PHP answers int(0).
		{"entered with an int", `function t($n) { $s = 0; while ($n-- > 0) { $s .= "x"; } return $s; }`, false},
		{"entered with a parameter", `function t($s, $n) { while ($n-- > 0) { $s .= "x"; } return $s; }`, false},
		{"old value kept", `function t($n) { $s = ""; while ($n-- > 0) { $t = $s; $s .= "x"; echo $t; } return $s; }`, false},
		{"bound by reference", `function t($n) { $s = ""; $r = &$s; while ($n-- > 0) { $s .= "x"; } return $r; }`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php "+c.src)
			n := lower.StringChains(fn)
			if (n > 0) != c.rewrites {
				t.Fatalf("rewrote %d, want any = %v\n%s", n, c.rewrites, fn)
			}
			if n == 0 {
				return
			}
			text := fn.String()
			if !strings.Contains(text, `"strbuf.append"`) || !strings.Contains(text, ": *strbuf") {
				t.Fatalf("expected a *strbuf chain\n%s", text)
			}
			if strings.Contains(text, `"concat"(%`) && !strings.Contains(text, `"strbuf.finish"`) {
				t.Fatalf("a concatenation reading a chain value must read a snapshot\n%s", text)
			}
		})
	}
}
