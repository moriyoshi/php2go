package interp_test

import (
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/moriyoshi/php2go/runtime/interp"
)

// Object handles appear in var_dump output as #1, #2 and so on. The oracle runs
// in an engine that has already allocated handles of its own, so its counter
// starts ahead of the interpreter's. The numbering is real PHP behaviour but
// this harness cannot align it, so it is normalized away rather than compared
// and quietly tolerated.
// It is anchored on the closing paren of object(Class) so that the frame
// numbers of a stack trace, which are also #0 and #1, stay comparable.
var objectHandle = regexp.MustCompile(`\)#\d+`)

// A diagnostic names the file it came from, and the two sides cannot agree on
// that: the oracle evaluates a string, so the engine calls it php2go-eval,
// while the interpreter runs a real temporary file. The path is a property of
// the harness rather than of PHP, so it is normalized away -- everything else
// in the message, including the line number, is still compared.
var diagnosticFile = regexp.MustCompile(` in (?:php2go-eval|/[^ ]*) on line `)

// The same path appears in an uncaught exception's header and in every stack
// trace frame, in two further shapes: "in FILE:LINE" and "#0 FILE(LINE):".
var traceFile = regexp.MustCompile(`(?m)^(#\d+ )(?:php2go-eval|/\S*?)\((\d+)\):`)
var thrownFile = regexp.MustCompile(` in (?:php2go-eval|/[^ :]*):(\d+)`)
var differentialRun atomic.Uint64

func normalizeHandles(s string) string {
	// An uncaught exception's frame numbering has to survive normalization, so
	// the trace substitutions run before object handles are collapsed.
	s = traceFile.ReplaceAllString(s, "${1}FILE($2):")
	s = thrownFile.ReplaceAllString(s, " in FILE:$1")
	s = objectHandle.ReplaceAllString(s, ")#N")
	return diagnosticFile.ReplaceAllString(s, " in FILE on line ")
}

// Every case here is run twice: once by the real PHP engine and once by the
// interpreter, and the outputs must match exactly.
//
// This is the oracle the plan relies on. Asserting against expected strings
// written by hand only tests what the author believed PHP does; comparing
// against the engine tests what it actually does. That distinction already
// caught a divergence in numeric coercion.
var differentialCases = []string{
	// Arithmetic, including the cases where PHP differs from Go.
	`echo 6 * 7;`,
	`echo 7 / 2;`,
	`echo 6 / 3;`,
	`echo 10 - 25;`,
	`echo PHP_INT_MAX + 1;`,
	`echo 0.1 + 0.2;`,
	`echo 1e15;`,
	`echo -0.5;`,

	// String conversion and concatenation.
	`echo "a" . "b" . 1 . 2.5;`,
	`echo true, "|", false, "|", null, "|";`,
	`$s = "12abc"; echo (int)$s;`,
	`$s = "  8"; echo (int)$s;`,

	// Comparison, where PHP 8 changed behaviour.
	`echo (0 == "abc") ? "T" : "F";`,
	`echo (0 == "0") ? "T" : "F";`,
	`echo ("10" == "1e1") ? "T" : "F";`,
	`echo ("abc" == "abc") ? "T" : "F";`,
	`echo (1 === "1") ? "T" : "F";`,
	`echo (null == "") ? "T" : "F";`,
	`echo (2 <= 2) ? "T" : "F";`,
	`echo (3 < 2) ? "T" : "F";`,

	// foreach, whose iteration order is insertion order rather than key order,
	// and whose by-value form is unaffected by mutation inside the loop.
	`foreach ([1,2,3] as $v) { echo $v; }`,
	`foreach (["x"=>1,"y"=>2,3] as $k => $v) { echo "$k=$v,"; }`,
	`foreach ([] as $v) { echo "never"; } echo "done";`,
	`$a = [1,2,3]; foreach ($a as $v) { $a[] = 9; echo $v; } echo count($a);`,
	`foreach ([[1,2],[3,4]] as $p) { foreach ($p as $v) { echo $v; } }`,
	`$a = ["a"=>1]; foreach ($a as $k => $v) { echo gettype($k); }`,
	`foreach ([1,2,3,4] as $v) { if ($v == 3) { break; } echo $v; }`,
	`foreach ([1,2,3,4] as $v) { if ($v == 2) { continue; } echo $v; }`,

	// Static methods and stdClass, neither of which needs an instance.
	`class C { static function f($x) { return $x * 2; } } echo C::f(21);`,
	`class C { static function a() { return "A"; } static function b() { return self::a() . "B"; } } echo C::b();`,
	`$o = new stdClass(); $o->x = 5; echo $o->x;`,
	`$o = new stdClass(); echo get_class($o);`,
	`$o = new stdClass(); $o->a = 1; $o->b = 2; var_dump($o);`,

	// Constants: engine-provided, script-defined, and class ones.
	`echo E_ALL;`,
	`echo PHP_EOL === "\n" ? "T" : "F";`,
	`echo SORT_STRING, ",", COUNT_RECURSIVE;`,
	`define("X", 41); echo X + 1;`,
	`define("X", 1); echo defined("X") ? "T" : "F", defined("Y") ? "T" : "F";`,
	`define("X", "v"); echo constant("X");`,
	`const K = 7; echo K;`,
	`class C { const A = 1; const B = "two"; } echo C::A, C::B;`,
	`class B { const A = 5; } class D extends B {} echo D::A;`,
	`interface I { const A = 9; } class C implements I {} echo C::A;`,
	`class C { const A = 3; static function f() { return self::A; } } echo C::f();`,

	`echo E_ALL === 32767 ? "T" : "F";`,

	// clone is shallow: an array property is copied, an object property shared.
	`class C { public $n = 1; } $a = new C(); $b = clone $a; $b->n = 2; echo $a->n, $b->n;`,
	`class C { public $a = [1,2]; } $x = new C(); $y = clone $x; $y->a[] = 3; echo count($x->a), count($y->a);`,
	`class C { public $n = 0; function __clone() { $this->n = 9; } } $x = new C(); $y = clone $x; echo $x->n, $y->n;`,

	// Nested writes, which descend through a write path rather than copying.
	`$a = []; $a[0][1] = "x"; echo $a[0][1];`,
	`$a = []; $a["k"][] = 1; $a["k"][] = 2; echo count($a["k"]), $a["k"][1];`,
	`$a = [[1,2],[3,4]]; $a[1][0] = 9; echo $a[1][0], $a[0][0];`,
	`$a = []; $a[0][0][0] = "deep"; echo $a[0][0][0];`,
	`class C { public $a = []; } $c = new C(); $c->a[] = 1; $c->a[] = 2; echo count($c->a);`,
	`class C { public $a = []; } $c = new C(); $c->a["k"][0] = "v"; echo $c->a["k"][0];`,
	`$a = [1,2]; $b = $a; $b[0] = 9; echo $a[0], $b[0];`,
	`$a = [[1]]; $b = $a; $b[0][0] = 9; echo $a[0][0], $b[0][0];`,

	// Exceptions: throw, catch by class, inheritance, and rethrow.
	`try { throw new Exception("boom"); } catch (Exception $e) { echo $e->getMessage(); }`,
	`try { throw new Exception("m", 7); } catch (Exception $e) { echo $e->getCode(); }`,
	`try { throw new RuntimeException("r"); } catch (Exception $e) { echo get_class($e); }`,
	`try { throw new InvalidArgumentException("i"); } catch (LogicException $e) { echo "logic"; }`,
	`try { throw new Exception("x"); } catch (RuntimeException $e) { echo "wrong"; } catch (Exception $e) { echo "right"; }`,
	`try { throw new Error("e"); } catch (Throwable $t) { echo get_class($t), $t->getMessage(); }`,
	`class MyEx extends Exception {} try { throw new MyEx("mine"); } catch (Exception $e) { echo get_class($e), $e->getMessage(); }`,
	`function f() { throw new Exception("deep"); } try { f(); } catch (Exception $e) { echo $e->getMessage(); }`,
	`try { try { throw new Exception("inner"); } catch (Exception $e) { echo "1"; throw new Exception("outer"); } } catch (Exception $e) { echo $e->getMessage(); }`,
	`try { throw new Exception("a", 0, new Exception("b")); } catch (Exception $e) { echo $e->getPrevious()->getMessage(); }`,
	`echo (new Exception("z")) instanceof Throwable ? "T" : "F";`,

	`try { echo 1 % 0; } catch (DivisionByZeroError $e) { echo $e->getMessage(); }`,
	`class C { function __clone() { throw new Exception("c"); } } $x = new C(); try { $y = clone $x; } catch (Exception $e) { echo $e->getMessage(); } echo "ok";`,

	// Integer-only and bitwise operators.
	`echo 7 % 3, " ", -7 % 3, " ", 7 % -3;`,
	`echo 2 ** 10, " ", 2 ** 0.5, " ", (-2) ** 3;`,
	`echo PHP_INT_MAX ** 2;`,
	`echo 12 & 10, " ", 12 | 10, " ", 12 ^ 10, " ", ~12;`,
	`echo 1 << 10, " ", -16 >> 2, " ", 1 << 64;`,
	`try { echo 1 << -1; } catch (ArithmeticError $e) { echo $e->getMessage(); }`,

	// Closures: capture by value at creation, arrow functions, recursion,
	// and the several shapes a dynamic callee can take.
	`$f = function ($x) { return $x + 1; }; echo $f(41);`,
	`$n = 10; $f = function ($x) use ($n) { return $x + $n; }; echo $f(5);`,
	`$n = 1; $f = function () use ($n) { return $n; }; $n = 99; echo $f();`,
	`$g = fn($x) => $x * 2; echo $g(21);`,
	`$m = 3; $g = fn($x) => $x * $m; echo $g(4);`,
	`$f = function () { return "no args"; }; echo $f();`,
	`echo (function ($x) { return $x; })("immediate");`,
	`$f = function ($a, $b) { return $a . $b; }; echo $f("x", "y");`,
	`$fs = []; foreach ([1,2,3] as $i) { $fs[] = function () use ($i) { return $i; }; } foreach ($fs as $f) { echo $f(); }`,
	`function outer() { return function () { return "inner"; }; } $f = outer(); echo $f();`,
	`$f = function ($x) { return $x; }; echo $f instanceof Closure ? "T" : "F", get_class($f);`,
	`function twice($x) { return $x * 2; } $name = "twice"; echo $name(10);`,
	`class C { function m($x) { return $x + 1; } } $c = new C(); $call = [$c, "m"]; echo $call(41);`,
	`class C { static function s($x) { return $x * 3; } } $call = ["C", "s"]; echo $call(5);`,
	`class C { static function s() { return "st"; } } $call = "C::s"; echo $call();`,
	`$f = function ($x) { return $x <= 1 ? 1 : $x; }; echo $f(0), $f(5);`,
	`class C { public $v = 7; function get() { return function () { return $this->v; }; } } $c = new C(); $f = $c->get(); echo $f();`,

	// Static variables, which share the closure binding opcode.
	`function c() { static $n = 0; $n++; return $n; } echo c(), c(), c();`,
	`function c() { static $s = "a"; $s .= "b"; return $s; } echo c(), c();`,

	// Compound assignment through properties and elements.
	`class C { public $n = 1; } $c = new C(); $c->n += 5; echo $c->n;`,
	`class C { public $s = "a"; } $c = new C(); $c->s .= "b"; echo $c->s;`,
	`$a = [1,2]; $a[0] += 10; $a[1] *= 3; echo $a[0], ",", $a[1];`,
	`$a = ["k" => "x"]; $a["k"] .= "y"; echo $a["k"];`,
	`class C { public $a = [1]; } $c = new C(); $c->a[0] += 41; echo $c->a[0];`,

	// Anonymous classes.
	`$o = new class { public $v = 5; function m() { return $this->v * 2; } }; echo $o->m();`,
	`$o = new class { public $v = 1; }; echo $o->v;`,
	`class B { function f() { return "b"; } } $o = new class extends B {}; echo $o->f();`,

	// foreach over an object visits the properties visible from this scope.
	`class C { public $a = 1; public $b = 2; } foreach (new C() as $k => $v) { echo "$k$v"; }`,
	`class C { public $a = 1; private $b = 2; protected $c = 3; } foreach (new C() as $k => $v) { echo "$k$v"; }`,
	`class C { public $a = 1; private $b = 2; function each() { foreach ($this as $k => $v) { echo "$k$v"; } } } (new C())->each();`,
	`$o = new stdClass(); $o->x = 1; $o->y = 2; foreach ($o as $k => $v) { echo "$k$v"; }`,

	// References: two names sharing one slot.
	`$a = 1; $b = &$a; $b = 5; echo $a;`,
	`$a = 1; $b = &$a; $a = 7; echo $b;`,
	`$a = 1; $b = &$a; unset($b); $b = 9; echo $a, $b;`,
	`function inc(&$x) { $x++; } $a = 1; inc($a); inc($a); echo $a;`,
	`function setIt(&$x) { $x = "set"; } $a = null; setIt($a); echo $a;`,
	`$arr = [1,2,3]; foreach ($arr as &$v) { $v *= 2; } unset($v); echo implode(",", $arr);`,
	`$arr = ["a"=>1,"b"=>2]; foreach ($arr as $k => &$v) { $v = $k; } unset($v); echo implode(",", $arr);`,
	`$n = 1; $f = function () use (&$n) { $n = 9; }; $f(); echo $n;`,
	`$n = 0; $f = function () use (&$n) { $n++; }; $f(); $f(); echo $n;`,
	`$a = [1,2]; $r = &$a[0]; $r = 9; echo $a[0];`,
	`$a = 1; $b = &$a; $c = $b; $b = 5; echo $c, $a;`,
	`function f(&$x) { $x[] = 3; } $a = [1,2]; f($a); echo count($a);`,
	`$a = ["x" => 1]; $r = &$a["x"]; $r++; echo $a["x"];`,
	`$a = 1; $b = &$a; $b = &$c; $c = 3; echo $a, $b;`,

	// Static properties, which belong to the class and are shared by subclasses
	// that do not redeclare them.
	`class C { public static $s = 1; } C::$s = 5; echo C::$s;`,
	`class C { public static $n = 0; static function bump() { self::$n++; } } C::bump(); C::bump(); echo C::$n;`,
	`class B { public static $v = "b"; } class D extends B {} D::$v = "d"; echo B::$v, D::$v;`,
	`class C { public static $a = [1,2]; } C::$a[] = 3; echo count(C::$a);`,
	`class C { public static $s = 2; } echo C::$s * 21;`,

	// isset and empty, which are not each other's negation.
	`$a = ["k" => 1]; echo isset($a["k"]) ? "y" : "n", isset($a["z"]) ? "y" : "n";`,
	`$a = ["k" => null]; echo isset($a["k"]) ? "y" : "n", array_key_exists("k", $a) ? "y" : "n";`,
	`class C { public $p = 3; } $o = new C(); echo isset($o->p) ? "y" : "n", isset($o->q) ? "y" : "n";`,
	`$a = [0, "", null, "x"]; foreach ($a as $v) { echo empty($v) ? "e" : "f"; }`,
	`$s = "abc"; echo isset($s[1]) ? "y" : "n", isset($s[9]) ? "y" : "n";`,
	`echo isset($nope) ? "y" : "n"; $x = null; echo isset($x) ? "y" : "n";`,
	`class C { public static $s = 1; } echo isset(C::$s) ? "y" : "n";`,

	// unset on the several kinds of target.
	`$a = ["k" => 1, "j" => 2]; unset($a["k"]); echo count($a), implode(",", array_keys($a));`,
	`class C { public $p = 1; } $o = new C(); unset($o->p); echo isset($o->p) ? "y" : "n";`,

	// Nullsafe access short-circuits the rest of the chain.
	`$x = null; var_dump($x?->foo);`,
	`class C { public $p = "v"; } $o = new C(); echo $o?->p;`,

	// Diagnostic suppression.
	`$x = @$nope; var_dump($x);`,

	// Array and string helpers, added as the corpus demanded them.
	`echo implode(",", [1,2,3]);`,
	`echo implode([1,2,3]);`,
	`var_dump(explode(",", "a,b,c"));`,
	`var_dump(explode(",", "a,b,c", 2));`,
	`var_dump(explode(",", "a,b,c", -1));`,
	`var_dump(array_keys(["a"=>1, 5=>2]));`,
	`var_dump(array_values(["a"=>1, "b"=>2]));`,
	`echo in_array("1", [1,2]) ? "y" : "n", in_array("1", [1,2], true) ? "y" : "n";`,
	`echo str_repeat("ab", 3), "|", str_repeat("x", 0), "|";`,
	// str_shuffle is implemented in PHP under internal/userland, and the thing
	// that has to hold is not just the permutation: it must draw from the
	// seeded generator the same number of times as the engine's own loop, so
	// the *next* mt_rand agrees too. A version that skipped the self-swap, or
	// drew once per byte instead of once per byte but the first, passes a
	// naive check and fails this one.
	`mt_srand(1234); $a = str_shuffle("abcdefghij"); $b = mt_rand(0, 99);
	 mt_srand(1234); $c = str_shuffle("abcdefghij"); $d = mt_rand(0, 99);
	 var_dump($a, $a === $c, $b, $b === $d);`,
	`mt_srand(7); var_dump(str_shuffle(""), str_shuffle("x"), str_shuffle("aa"), strlen(str_shuffle("abcdef")));`,
	// The batch reimplemented in PHP under internal/userland. Each line is an
	// edge the engine was asked about rather than one reasoned from: ucfirst is
	// ASCII-only so a non-ASCII first byte is untouched; ucwords' separator
	// argument *replaces* the default set; nl2br keeps the original bytes after
	// the tag and treats a CRLF pair as one break; quotemeta does not escape
	// braces; str_pad's BOTH puts the odd byte on the right.
	// The non-ASCII case -- ucfirst("\xe9tre") -- cannot live here: a non-UTF-8
	// literal does not survive the IR, whose Literal.string_value is a proto3
	// string. Covered by a php2eval probe instead; see TODO.md.
	`var_dump(ucfirst("hello world"), ucfirst(""), ucfirst("1abc"), lcfirst("HELLO"), lcfirst("1A"));`,
	`var_dump(ucwords("hello world-foo bar"), ucwords("a\tb\nc"), ucwords("hello|world", "|"), ucwords("a-b_c", "-_"), ucwords(""), ucwords("  x"), ucwords("a\fb\vc\rd"));`,
	`var_dump(nl2br("a\nb"), nl2br("a\r\nb"), nl2br("a\rb"), nl2br("a\n\rb"), nl2br("a\nb", false));`,
	`var_dump(quotemeta("1+1=2. (a)? [b] {c} *x \\z"), quotemeta(""));`,
	`var_dump(strrev("abc"), strrev(""), strrev("ab\tc"));`,
	`var_dump(str_pad("5", 5, "0", STR_PAD_LEFT), str_pad("ab", 7, "xyz", STR_PAD_BOTH), str_pad("ab", 1), str_pad("ab", 5));`,
	`try { str_pad("ab", 6, ""); } catch (ValueError $e) { echo $e->getMessage(); }`,
	`var_dump(array_sum([1, 2.5, "3", true, null]), array_sum([]), array_product([2, "3", 2.0]), array_product([]));`,
	// array_is_list's TypeError names the value through php.ValueName now that
	// the array library moved to runtime/stdlib, where the interpreter's
	// givenTypeName is not reachable. The two differ on paper -- one cuts an
	// anonymous class's name at its NUL and the other does not -- so the case
	// that would show it is here.
	`$o = new class {};
	 try { array_is_list($o); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { array_is_list("s"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { array_is_list(true); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { array_is_list(null); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// The is_* family compiles to ZEND_TYPE_CHECK, so a direct call and a
	// dynamic one take different routes to the same answer and have to agree.
	// is_numeric is the one that is genuinely a library call, and the one whose
	// dynamic form read a reference as a non-number until it derefed.
	`foreach (["is_int", "is_float", "is_string", "is_array", "is_object", "is_null",
	           "is_bool", "is_scalar", "is_numeric"] as $f) {
	   echo $f, ":";
	   foreach ([1, 1.5, "2", "x", [1], null, true, false] as $v) { echo $f($v) ? "1" : "0"; }
	   echo "|";
	 }`,
	`var_dump(array_flip(["a"=>1, "b"=>"x", 5=>2]));`,
	// array_flip stays native because it warns; the case is kept so the
	// behaviour is pinned either way.
	`var_dump(array_flip(["a"=>[1], "b"=>2, "c"=>1.5, "d"=>true, "e"=>null]));`,
	// Output buffering moved to runtime/stdlib, which meant three things had to
	// keep holding: the **phase word** a handler is told (8 is final, |2 a
	// clean, |1 the handler's first run), that `ob_get_clean` answers with the
	// **raw** buffer while a flush writes what the handler returned, and that a
	// handler returning false leaves the original text alone.
	//
	// **Nothing here may observe the buffer *level*.** The engine arm captures
	// output with `php_output_start_default()`, so the engine runs one level
	// deep where the CLI runs at zero, and a case that reads `ob_get_level()`
	// or ends more buffers than it opened measures the harness. See
	// TESTING.md.
	`ob_start(); echo "a"; $s = ob_get_clean(); var_dump($s);`,
	`ob_start(function ($t, $p) { return "[$p:$t]"; }); echo "x"; ob_end_flush();`,
	`ob_start(function ($t, $p) { return "[$p:$t]"; }); echo "x"; var_dump(ob_get_clean());`,
	`ob_start(function ($t, $p) { return "[$p:$t]"; }); echo "x"; var_dump(ob_get_flush());`,
	`ob_start(function ($t, $p) { return false; }); echo "x"; ob_end_flush();`,
	`ob_start(function ($t, $p) { return null; }); echo "x"; ob_end_flush(); echo "|end";`,
	`ob_start("strtoupper"); echo "ab"; ob_end_flush();`,
	`ob_start(); echo "a"; ob_start(); echo "b"; var_dump(ob_get_clean(), ob_get_clean());`,
	`ob_start(); echo "abc"; $c = ob_get_contents(); $n = ob_get_length(); ob_end_clean(); var_dump($c, $n);`,
	`ob_start(); echo "a"; $r = ob_clean(); $c = ob_get_contents(); ob_end_clean(); var_dump($r, $c);`,
	`ob_start(function ($t, $p) { return "<$t>"; }); echo "a"; ob_flush(); echo "b"; ob_end_flush();`,
	// A chunk size is not a hint: the handler runs once the buffer holds that
	// much, and a program can count the calls.
	`$n = 0; ob_start(function ($t, $p) use (&$n) { $n++; return $t; }, 4);
	 echo "abcdef"; ob_end_flush(); echo "|", $n;`,
	// A buffer the script never closes is flushed by the engine at the end of
	// the request, through its handler.

	`var_dump(ob_start(42), ob_start("no_such_function_here"), ob_start([1, 2]), ob_start(new stdClass));`,
	`class C {} var_dump(ob_start(["C", "m"]));`,
	// An object with no `__invoke` is refused by the callback-argument check
	// for its **shape**, in the same words as an int -- not with the wording
	// invoking one would produce.
	`class D { public function m() {} }
	 try { array_map(new D, [1]); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { set_error_handler(new stdClass); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 $o = new stdClass;
	 try { $o(); } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	// A handler that throws: what it was given is written out unchanged, the
	// same as declining, and the throw carries on.
	`ob_start(function ($t, $p) { throw new Exception("boom"); });
	 echo "held";
	 try { ob_end_flush(); } catch (Exception $e) { echo "|caught:", $e->getMessage(); }`,
	// debug_backtrace and trigger_error moved to runtime/stdlib. The trace's
	// key *order* is the engine's and var_dump compares it, the `object` key
	// is present only under DEBUG_BACKTRACE_PROVIDE_OBJECT, and a
	// `#[\SensitiveParameter]` argument is replaced through the Ctx hook
	// rather than shown.
	// The `file` key is the harness's own path and an object's handle number
	// is the harness's own count, so the frames are printed **key by key**
	// with those two left out. Printing the frame whole compares two things
	// this oracle cannot make agree; see TESTING.md.
	`function show($frames) {
	   foreach ($frames as $i => $f) {
	     echo $i, ":";
	     foreach ($f as $k => $v) {
	       if ($k === "file") { continue; }
	       echo " ", $k, "=";
	       if ($k === "object") { echo get_class($v); }
	       else if ($k === "args") { echo json_encode(array_map("gettype", $v)); }
	       else { echo $v; }
	     }
	     echo "\n";
	   }
	 }
	 function inner($a, $b) { show(debug_backtrace()); }
	 function outer($x) { inner($x, "second"); }
	 outer(1);
	 class H { public function run($p) { show(debug_backtrace()); }
	           public static function fire() { (new H)->run("x"); } }
	 H::fire();
	 function f($a) { show(debug_backtrace(0)); show(debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS)); }
	 f(1);
	 function secret(#[\SensitiveParameter] $password, $user) {
	   $frames = debug_backtrace();
	   var_dump(get_class($frames[0]["args"][0]), $frames[0]["args"][1]);
	 }
	 secret("hunter2", "root");`,
	`function d3() { var_dump(count(debug_backtrace(0, 2)), count(debug_backtrace(0, 100))); }
	 function d2() { d3(); } function d1() { d2(); } d1();`,
	`trigger_error("a notice");
	 trigger_error("a warning", E_USER_WARNING);
	 trigger_error("deprecated now", E_USER_DEPRECATED);
	 try { trigger_error("bad", 12345); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
	 user_error("through the alias", E_USER_WARNING);`,
	// array_map moved to runtime/stdlib. The shape of the answer depends on how
	// many arrays it is given: one keeps its keys, several are walked
	// positionally into a plain list padded with null, and a null callback
	// zips -- except with one array, where it comes back unchanged.
	`var_dump(array_map("strtoupper", ["a" => "x", 5 => "y"]));`,

	// An array's internal pointer survives a copy: PHP's is refcounted and the
	// pointer belongs to the array, so `$b = $a` and a by-value parameter both
	// see the advanced element.
	`$a=[1,2,3]; next($a); $b=$a; var_dump(current($a), current($b));`,
	`$a=[1,2,3]; next($a); function f(array $x){ return current($x); } var_dump(f($a));`,
	`$a=[1,2]; next($a); next($a); $b=$a; var_dump(current($a), current($b));`,
	`$a=[1,2,3,4]; unset($a[1]); next($a); $b=$a; var_dump(current($a), current($b));`,

	// `&$s[0]` on a string is refused by **type**, not by whether the offset
	// exists: an empty string refuses too, while null autovivifies. The three
	// neighbouring errors are deliberately distinct.
	`$a = "s"; try { $b = &$a[0]; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`$a = ""; try { $b = &$a[0]; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`$a = "s"; try { $b = &$a[]; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`$a = 5; try { $b = &$a[0]; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`$a = null; $b = &$a[0]; var_dump($a);`,

	`var_dump(array_map(null, ["a" => 1, "b" => 2]));`,
	`var_dump(array_map(null, [1, 2, 3], ["a", "b"]));`,
	`var_dump(array_map(fn ($a, $b) => "$a-$b", ["x" => 1, 2], [3]));`,
	`var_dump(array_map("strtoupper", []));`,
	// **Only argument #2 is named.** It is the declared `$array`; #3 onwards
	// are the variadic `...$arrays`, and the engine names no variadic slot.
	`try { array_map("strtoupper", 1); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { array_map("strtoupper", [1], "s"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { array_map("strtoupper", [1], [2], 3); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { array_map(null, 1); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { array_map("nope", [1]); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	`function throws($v) { throw new Exception("at $v"); }
	 try { array_map("throws", [1, 2]); } catch (Exception $e) { echo $e->getMessage(), "\n"; }`,
	// The scalar readers moved to runtime/stdlib. `gettype` has its own
	// vocabulary -- `integer`/`double`, and `resource (closed)` for a handle
	// that has been closed -- which is not `get_debug_type`'s.
	`foreach ([1, 1.5, "s", true, false, null, [1], new stdClass] as $v) {
	   echo gettype($v), "|", get_debug_type($v), " ";
	 }
	 echo "\n";
	 $f = fopen("/dev/null", "r"); echo gettype($f), " ";
	 fclose($f); echo gettype($f), "\n";`,
	`var_dump(strlen(""), strlen("abc"), strlen("a\0b"), strlen(1234));`,
	`foreach (["12abc", "", "0x1A", " 7 ", "1e3", true, null, 1.9, -1.9] as $v) {
	   echo intval($v), "/", floatval($v), "/", strval($v), "/", (boolval($v) ? 1 : 0), " ";
	 }`,
	// **The base applies to a string and to nothing else**, and base 10 is not
	// "strtol at base 10" -- it is the ordinary numeric-string conversion, so
	// `intval("1e3", 10)` is 1000 where `intval("1e3", 0)` is 1. An invalid
	// base answers 0 rather than raising. And at base 0 or 2 PHP strips a
	// leading `0b` itself and then reads the rest as **binary** whatever the
	// caller asked for, which is why `intval("0B11", 0)` is 3 and not 11.
	`var_dump(doubleval("2.5"), floatval("2.5"), intval("42", 8), intval("0x1A", 16), intval("101", 2), intval("42", 0));`,
	`foreach (["42", "0x1A", "1A", "101", "0b11", "0o17", "017", "  12 ", "12abc", "zz",
	          "-ff", "+10", "1e3", "2.5", "0x", "", "0b1", "9", "1_0", "0xg", " -12",
	          "0b0b1", "0b1b0", "00b1", "0X1f", "0B11", "08", "0", "00", "x0b1", "0b"] as $s) {
	   echo $s, "|";
	   foreach ([0, 2, 8, 10, 16, 36] as $b) { echo intval($s, $b), ","; }
	   echo "\n";
	 }`,
	`foreach ([1, 37, -1, 0, 2, 36] as $b) { var_dump(intval("10", $b)); }
	 var_dump(intval("99999999999999999999", 8), intval("-99999999999999999999", 16),
	          intval("ffffffffffffffffff", 16), intval("\n\t 0x1f", 0));
	 var_dump(intval(42.9, 8), intval(true, 8), intval(null, 8), intval([1], 8));`,
	// get_class_vars and get_object_vars both answer for the **calling scope**,
	// and they differ in three ways that are easy to get backwards: the class
	// question lists a class's *own* declarations before its ancestors' where
	// the instance question uses the slot order, which is parent-first; an
	// uninitialised typed property is null in one and absent from the other;
	// and a static reports **what the class declares**, not what the program
	// has since written to it.
	`class VB { public $a = 1; protected $b = 2; private $c = 3;
	            public static $s = "S"; public int $u;
	            static function inside() { return get_class_vars("VB"); }
	            function objIn() { return get_object_vars($this); } }
	 class VC extends VB { public $d = 4; private $e = 5; public static $t = "T";
	                       static function insideC() { return get_class_vars("VC"); }
	                       function objInC() { return get_object_vars($this); } }
	 var_dump(get_class_vars("VC"), VB::inside(), VC::insideC());
	 VB::$s = "changed";
	 var_dump(get_class_vars("VB"));
	 $o = new VC; $o->dyn = 9;
	 var_dump(get_object_vars($o), $o->objIn(), $o->objInC());
	 try { get_class_vars("no_such_class_here"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	`class VP { private $p = 1; } class VQ extends VP {}
	 var_dump(get_object_vars(new VQ), get_class_vars("VQ"));
	 class VT { public int $set = 1; public int $never; public ?string $n = null; }
	 var_dump(get_object_vars(new VT), get_class_vars("VT"));
	 $s = new stdClass; $s->x = 1; $s->{"a b"} = 2;
	 var_dump(get_object_vars($s), get_object_vars(new stdClass));`,
	// get_class_methods lists what the *calling scope* can reach, in the
	// engine's order: a class's own declarations, then its traits', then its
	// interfaces', then each ancestor's the same way -- and a name a subclass
	// redeclares appears once, at the subclass.
	`trait MT { public function tm() {} private function tp() {} }
	 interface MI { public function im(); }
	 abstract class MA implements MI { use MT;
	   public function ap() {} protected function aq() {} private function ar() {}
	   public static function asx() {} abstract public function ab();
	   public function fromA() { return get_class_methods("MD"); } }
	 class MD extends MA { public function im() {} public function ab() {}
	   private function dp() {}
	   public function fromD() { return get_class_methods("MD"); } }
	 $d = new MD;
	 var_dump(get_class_methods("MD"), get_class_methods($d));
	 var_dump($d->fromA(), $d->fromD());
	 var_dump(get_class_methods("MI"), get_class_methods("MA"), get_class_methods("MT"));`,
	// `$GLOBALS` read whole is a **snapshot**: since PHP 8.1 writing through it
	// does nothing, and it never contains `GLOBALS` itself.
	`$ga = 1; $gb = "x";
	 $g = $GLOBALS;
	 var_dump(isset($g["GLOBALS"]), array_key_exists("ga", $g), $g["ga"], $g["gb"]);
	 $g["ga"] = 99; var_dump($ga);
	 $sum = $GLOBALS + $GLOBALS; var_dump($sum["ga"]);
	 function readsGlobals() { $local = 5; return array_key_exists("local", $GLOBALS); }
	 var_dump(readsGlobals());`,
	// **Touching one enum case builds them all**, in declaration order, on
	// first access. The handles are observable: on a three-case enum `E::B` is
	// handle 3 and the next `new` is 5. And building them all is why reading
	// one case raises for another's bad backing value.
	//
	// **Relative to a baseline, never absolute.** The engine arm shares one
	// process across every case in this file, so its handles start wherever the
	// previous case left off; only the gaps between them mean anything.
	`enum EH: int { case A = 1; case B = 2; case C = 3; }
	 // The anchor is **kept in a variable**: a temporary would be freed, and
	 // whether its handle is reused is a separate question this case is not
	 // about.
	 $anchor = new stdClass; $base = spl_object_id($anchor);
	 $o = new stdClass; echo spl_object_id($o) - $base, " ";
	 $x = EH::B; echo spl_object_id($x) - $base, " ",
	   spl_object_id(EH::A) - $base, " ", spl_object_id(EH::C) - $base, " ";
	 $p = new stdClass; echo spl_object_id($p) - $base, " ";
	 enum FH { case Z; }
	 $q = new stdClass; echo spl_object_id($q) - $base, " ";
	 echo count(FH::cases()), " ", spl_object_id(FH::Z) - $base, " ";
	 $r = new stdClass; echo spl_object_id($r) - $base, "\n";
	 var_dump(spl_object_hash($o) === spl_object_hash($o), spl_object_hash($o) === spl_object_hash($p));`,
	`enum YH: int { case D = 1; case Z = "s"; }
	 try { var_dump(YH::D); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// var_export renders a value as PHP source: the output has to parse back to
	// the same value, and the indentation offsets are the engine's.
	`var_export(null); echo "|"; var_export(true); echo "|"; var_export(false); echo "|";
	 var_export(0); echo "|"; var_export(-12); echo "|"; var_export(2.0); echo "|";
	 var_export(0.1); echo "|"; var_export(1e100); echo "|"; var_export(INF); echo "|";
	 var_export(-INF); echo "|"; var_export(NAN); echo "\n";
	 var_export("plain"); echo "\n";
	 var_export("it's \\ a \"quote\""); echo "\n";
	 var_export("nul\0byte"); echo "\n";
	 var_export([]); echo "\n";
	 var_export(["a" => 1, 5 => [2, ["deep" => true]]]); echo "\n";
	 $o = new stdClass; $o->a = 1; $o->nested = new stdClass; $o->nested->b = [1, 2];
	 var_export($o); echo "\n";
	 class VE { public $pub = 1; protected $prot = 2; private $priv = 3; }
	 var_export(new VE); echo "\n";
	 var_export([new VE]); echo "\n";
	 var_dump(var_export([1, "two"], true));`,
	// A literal is an **interned** string, which is the one place the
	// distinction shows.
	`debug_zval_dump(1, "s", [1, 2], null, 2.5, true, 1.0);
	 $made = "a" . "b"; debug_zval_dump($made);`,
	// INI settings are request state, and what php2go models is a closed list:
	// anything else answers false rather than inventing a value. Two modelled
	// settings still refuse a write, and `syslog.filter` takes one of four
	// words.
	`var_dump(ini_get("arg_separator.output"), ini_get("zend.enable_gc"),
	          ini_get("url_rewriter.tags"), ini_get("no.such.setting.at.all"));
	 var_dump(ini_set("arg_separator.output", ";"), ini_get("arg_separator.output"));
	 var_dump(ini_set("arg_separator.output", "&amp;"), ini_get("arg_separator.output"));
	 var_dump(ini_set("realpath_cache_size", "1M"), ini_set("realpath_cache_ttl", "5"));
	 var_dump(ini_set("syslog.filter", "raw"), ini_get("syslog.filter"));
	 var_dump(ini_set("syslog.filter", "nonsense"), ini_get("syslog.filter"));
	 var_dump(ini_set("no.such.setting.at.all", "x"));
	 // **Put back what this case changed.** The engine arm shares one process
	 // across every case in this file, so a setting left written leaks into
	 // whichever case runs next -- it turned up as a syslog case reading "raw".
	 ini_set("arg_separator.output", "&");
	 ini_set("syslog.filter", "no-ctrl");`,
	// settype writes through a reference the caller owns, and is the casts
	// written as a function. An object with `__toString` converts and one
	// without is an Error; there is no conversion *to* a resource.
	`foreach ([["bool", 1], ["boolean", "0"], ["int", "12abc"], ["integer", 2.9],
	          ["float", "1.5"], ["double", 3], ["string", 42], ["array", "x"],
	          ["array", null], ["null", "gone"]] as [$type, $value]) {
	   $held = $value;
	   var_dump(settype($held, $type), $held);
	 }
	 class STRendered { public function __toString(): string { return "rendered"; } }
	 $o = new STRendered; var_dump(settype($o, "string"), $o);
	 $bare = new stdClass;
	 try { settype($bare, "string"); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 $a = ["x" => 1, 2 => "two"]; var_dump(settype($a, "object"), $a);
	 $n = 5; var_dump(settype($n, "object"), $n);
	 $r = 1;
	 try { settype($r, "resource"); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
	 try { settype($r, "nonsense"); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }`,
	// call_user_func and its array form. The compiler folds a literal
	// `call_user_func("f", ...)` into a direct call, so what the library
	// function sees is the dynamic form -- and an array element that is
	// already a reference is passed as one, so a by-reference parameter writes
	// back into whatever it aliases.
	`function cufGreet($who, $punct = "!") { return "hi $who$punct"; }
	 $name = "cufGreet";
	 var_dump(call_user_func($name, "world"), call_user_func($name, "world", "?"));
	 var_dump(call_user_func_array($name, ["world", "."]));
	 class CufGreeter { public $prefix = ">";
	   public function say($what) { return $this->prefix . $what; }
	   public static function shout($what) { return strtoupper($what); } }
	 $o = new CufGreeter;
	 var_dump(call_user_func([$o, "say"], "instance"),
	          call_user_func(["CufGreeter", "shout"], "static"),
	          call_user_func_array([$o, "say"], ["array form"]));
	 $closure = function ($a, $b) { return $a . "/" . $b; };
	 var_dump(call_user_func($closure, 1, 2), call_user_func_array($closure, [3, 4]));
	 function cufBump(&$n) { $n++; }
	 $counter = 5; $args = [&$counter];
	 call_user_func_array("cufBump", $args); var_dump($counter);
	 $direct = 7; call_user_func_array("cufBump", [&$direct]); var_dump($direct);
	 try { call_user_func_array($closure, "nope"); }
	 catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// `[&$x]` binds a reference **into the element**: the element keeps the box
	// rather than copying through it. Unpacking passes that box on, so a
	// by-reference parameter binds and a by-value one unboxes.
	`$c = 5; $a = [&$c]; $a[0]++; var_dump($c, $a);
	 $c = 99; var_dump($a); $a[0] = "through"; var_dump($c);
	 $p = 1; $q = 2; $mixed = [$p, &$q, 3];
	 $mixed[0] = "no"; $mixed[1] = "yes"; var_dump($p, $q, $mixed);
	 $k = "start"; $keyed = ["slot" => &$k, 7 => &$k];
	 $keyed["slot"] = "changed"; var_dump($k, $keyed);
	 $n = 10; $nested = [[&$n]]; var_dump($nested);
	 $nested[0][0] = 11; var_dump($n);
	 $src = 1; $one = [&$src]; $two = $one; $two[0] = 42;
	 var_dump($src, $one, $two);`,
	`function refGrow(&$n) { $n .= "!"; }
	 function refLook($n) { $n .= "?"; return $n; }
	 $word = "hi"; $spread = [&$word];
	 refGrow(...$spread); var_dump($word);
	 call_user_func_array("refGrow", $spread); var_dump($word);
	 var_dump(refLook(...$spread), $word);`,
	// **A by-value element of an array literal is a copy.** `[$inner, $inner]`
	// puts two independent arrays in, and writing one is not visible through
	// the other; only a `&` element shares.
	`$inner = [1, 2]; $outer = [$inner, $inner];
	 $outer[0][] = "x"; print_r($outer);
	 foreach ($outer as $k => &$w) { $w[] = "t$k"; } unset($w); print_r($outer);
	 $held = [1]; $bound = [&$held, $held];
	 $bound[0][] = "through"; $bound[1][] = "copy"; print_r($held); print_r($bound);
	 $obj = new stdClass; $obj->n = 1; $pair = [$obj, $obj];
	 $pair[0]->n = 2; var_dump($pair[1]->n);`,
	// print_r is harvested PHP now. The whole rendering is in
	// internal/userland; the one question it asks natively is what an object
	// shows, which is where a Closure's [name]/[file]/[line]/[parameter], a
	// Generator's [function] and an enum's `Enum:string` label come from.
	`print_r(null); echo "|"; print_r(true); echo "|"; print_r(false); echo "|";
	 print_r(0); echo "|"; print_r(-12); echo "|"; print_r(2.0); echo "|";
	 print_r(0.1); echo "|"; print_r("plain"); echo "|\n";
	 print_r([]); print_r([1, "two", [3, ["deep" => true]], null, 1.5, false]);
	 print_r(["a" => ["b" => ["c" => 1]]]);
	 var_dump(print_r([1, 2], true));
	 var_dump(print_r("scalar", true), print_r(7, true));`,
	`class PrObj { public $a = 1; protected $b = 2; private $c = 3; public int $u;
	   public $nested; }
	 $o = new PrObj; $o->nested = ["x" => new PrObj];
	 print_r($o);
	 class PrDebug { public $real = 1;
	   public function __debugInfo(): array { return ["shown" => "instead"]; } }
	 print_r(new PrDebug);
	 print_r([new PrDebug, "after"]);`,
	`enum PrSuit: string { case Hearts = "H"; }
	 enum PrPlain { case Only; }
	 print_r(PrSuit::Hearts); print_r([PrPlain::Only]);
	 // The closure's name and file are the harness's own path, so they are
	 // replaced rather than compared -- see TESTING.md.
	 echo str_replace(__FILE__, "FILE", print_r(function ($x, $y = 1) {}, true));
	 function prGen() { yield 1; }
	 print_r(prGen());`,
	// **Recursion**, which is why the walk takes its subject by reference: a
	// by-value one sees a fresh identity at every level.
	`$rec = [1]; $rec[] = &$rec; print_r($rec);
	 $inner = [1, 2]; $twice = [$inner, $inner]; print_r($twice);
	 class PrNode { public $next; public $v = 1; }
	 $n = new PrNode; $n->next = $n; print_r($n);
	 $pair = new PrNode; $other = new PrNode; $pair->next = $other; $other->next = $pair;
	 print_r($pair);
	 // **Break the cycles.** The engine arm shares one process, so a cycle left
	 // standing is still collectable when a later case calls
	 // gc_collect_cycles() -- which reported 4 against the interpreter's 0.
	 $n->next = null; $pair->next = null; $other->next = null;
	 // The self-referencing array is a cycle too: unsetting the name leaves the
	 // element pointing back at it, so the element goes first.
	 $rec[1] = null; unset($rec);`,
	// `function f(&...$rest)`: each collected argument keeps its box, so
	// writing an element writes the variable the caller named.
	`function bvSetAll(&...$args) {
	   foreach ($args as $k => &$v) { $v = "set$k"; }
	   return count($args);
	 }
	 $a = 1; $b = 2; var_dump(bvSetAll($a, $b), $a, $b);
	 function bvNone(&...$xs) { return count($xs); }
	 var_dump(bvNone());
	 function bvMixed($first, &...$rest) {
	   $first = "ignored"; $rest[0] = "changed"; return count($rest);
	 }
	 $p = "keep"; $q = "before"; var_dump(bvMixed($p, $q), $p, $q);
	 function bvBump(&...$ns) { foreach ($ns as &$n) { $n++; } }
	 $x = 1; $spread = [&$x]; bvBump(...$spread); var_dump($x);
	 function bvJoined(&...$parts) { return implode("-", $parts); }
	 $one = "a"; $two = "b"; var_dump(bvJoined($one, $two));`,
	// A **by-reference** foreach follows the array as the body changes it, and
	// the position moves when the array is rebuilt: `array_unshift` inside the
	// loop shifts every element right, and a cursor that stayed put walked the
	// new elements and then the old ones again, for ever.
	`$a = [1, 2, 3];
	 foreach ($a as &$v) { echo "$v\n"; if ($v == 2) { array_unshift($a, 0, 0, 0); } }
	 unset($v); var_dump(count($a), $a);`,
	`$b = [1, 2, 3];
	 foreach ($b as $k => &$w) { echo "$k=$w\n"; if ($k === 0) { array_push($b, 9); } }
	 unset($w); var_dump($b);`,
	`$c = [1, 2, 3, 4];
	 foreach ($c as $k => &$u) { echo "$k=$u\n"; if ($k === 1) { array_shift($c); } }
	 unset($u); var_dump($c);`,
	// `get_mangled_object_vars` reports the table under the engine's storage
	// keys, where `get_object_vars` asks what the calling scope can reach.
	`class MvHolder { public $a = 1; protected $b = 2; private $c = 3;
	   public int $never; public ?string $null = null; }
	 var_dump(get_mangled_object_vars(new MvHolder));
	 class MvChild extends MvHolder { private $d = 4; }
	 var_dump(get_mangled_object_vars(new MvChild));
	 $plain = new stdClass; $plain->x = 1; $plain->{"a b"} = 2;
	 var_dump(get_mangled_object_vars($plain), get_mangled_object_vars(new stdClass));
	 try { get_mangled_object_vars("s"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 $h = fopen("/dev/null", "r"); var_dump(get_resource_type($h));
	 fclose($h); var_dump(get_resource_type($h));`,
	`trait MvFirst {} trait MvSecond { public function m() {} }
	 var_dump(get_declared_traits());`,
	// `get_defined_vars()` is the variables **assigned so far**, in slot order.
	// One declared in a branch that did not run has a slot and no value and is
	// not in the array; a by-reference parameter is in it **as a reference**,
	// so the element tracks a later write.
	`function gdvScoped($a, $b = 2) {
	   $c = 3; var_dump(get_defined_vars());
	   $d = 4; var_dump(array_keys(get_defined_vars()));
	   if (false) { $never = 1; }
	   var_dump(array_keys(get_defined_vars()));
	   return $d;
	 }
	 gdvScoped(1);
	 function gdvBare() { var_dump(get_defined_vars()); }
	 gdvBare();
	 function gdvByRef(&$r, $plain) {
	   $seen = get_defined_vars();
	   $r = "changed";
	   var_dump($seen, get_defined_vars());
	 }
	 $outer = "before"; gdvByRef($outer, "x"); var_dump($outer);
	 class GdvHolder { public function show($arg) { $local = 1; return array_keys(get_defined_vars()); } }
	 var_dump((new GdvHolder)->show("v"));
	 $fn = function ($p) use (&$outer) { $q = 2; return array_keys(get_defined_vars()); };
	 var_dump($fn("given"));`,
	// `var_export` of a hooked object **runs the hooks and applies no
	// visibility**: every declared property under its plain name, virtual ones
	// included, private and protected alongside the public. That is a different
	// answer from `get_object_vars`, which the calling scope filters, and from
	// the raw table a cast shows.
	`class VeBase {
	   public $a = "a";
	   public $virtual { get { return "V"; } }
	   public $backed = "b" { get { return strtoupper($this->backed); } set { $this->backed = $value; } }
	   protected $prot = "prot";
	 }
	 class VeDerived extends VeBase {
	   public $own = "own" { get { return strtoupper($this->own); } }
	   public $plain = "plain";
	 }
	 $d = new VeDerived;
	 var_export($d); echo "\n";
	 var_dump(get_object_vars($d));
	 var_dump((array) $d);`,
	// Reaching a property as **storage** creates it where the `&` is, holding
	// NULL -- not the undef a fresh box carries, which is the uninitialised
	// state of a declared typed property and something a dynamic one never is.
	// And `get_object_vars` duplicates the table rather than reading it, so a
	// program-visible reference stays one.
	`class DynRef { public $x = 1; }
	 $o = new DynRef; $b = &$o->missing; unset($b);
	 var_dump(get_object_vars($o));
	 $p = new DynRef; $r = &$p->later; $r = 5;
	 var_dump(get_object_vars($p));
	 $d = new DynRef; $q = &$d->x; $q = 9;
	 var_dump(get_object_vars($d));
	 $e = new DynRef; $e->list[] = "first";
	 var_dump(get_object_vars($e));
	 unset($q, $r);`,
	// `protected(set)` is measured against the **first** class to declare the
	// name, not the subclass that redeclared it -- so a sibling of that subclass
	// may still write it -- while the refusal names the redeclaration. The two
	// disagree and both are observable.
	`class AsymBase { public protected(set) int $p = 1; }
	 class AsymMid extends AsymBase { public protected(set) int $p = 2; }
	 class AsymSib extends AsymBase { static function bump($o) { return $o->p += 1; } }
	 var_dump(AsymSib::bump(new AsymMid));
	 class AsymOutside { static function bump($o) { $o->p = 9; } }
	 try { AsymOutside::bump(new AsymMid); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 // Declared only by the subclass: a sibling is not in its hierarchy at all.
	 class AsymRoot { }
	 class AsymOnly extends AsymRoot { public protected(set) int $q = 1; }
	 class AsymCousin extends AsymRoot { static function bump($o) { return $o->q += 1; } }
	 try { var_dump(AsymCousin::bump(new AsymOnly)); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 // And a virtual hooked property is governed the same way.
	 class AsymHooked { private $s = 0; public protected(set) int $v { get => $this->s; set { $this->s = $value; } } }
	 class AsymHeir extends AsymHooked { static function set($o) { $o->v = 7; return $o->v; } }
	 var_dump(AsymHeir::set(new AsymHooked));
	 try { AsymOutside::bump(new AsymHooked); } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	// A plain object walked by reference binds to the **property slots**: the
	// writes reach the object, only what the calling scope can see is walked,
	// an uninitialised typed property is skipped, and a readonly one has no
	// reference to give.
	`class FeO { public $a = 1; public $b = 2; protected $p = 3; private $q = 4; }
	 $o = new FeO;
	 foreach ($o as $k => &$v) { $v = $v * 10; }
	 unset($v);
	 var_dump(get_object_vars($o));
	 class FeInner {
	   public $x = 1; protected $y = 2; private $z = 3;
	   function walk() { foreach ($this as $k => &$v) { $v++; } unset($v); }
	   function seen(): array { return get_object_vars($this); }
	 }
	 $i = new FeInner; $i->walk(); var_dump($i->seen());
	 class FeTyped { public int $set = 1; public int $unset; }
	 $t = new FeTyped;
	 foreach ($t as $k => &$u) { $u = 9; }
	 unset($u);
	 var_dump(get_object_vars($t));
	 class FeLocked { public function __construct(public readonly int $n) {} }
	 try { foreach (new FeLocked(1) as &$r) { } } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	// A Traversable unpacks by being drained. An **integer key is not a
	// position** -- `5 => "x"` is the next argument, not the sixth -- a key that
	// is neither int nor string stops the unpack, and a by-reference parameter
	// gets a warning rather than a box, with the call still happening.
	`function upTake($a, $b, $c = "dflt") { var_dump($a, $b, $c); }
	 function upInt() { yield 1; yield 2; }
	 upTake(...upInt(), ...[3]);
	 function upSkip() { yield 5 => "x"; yield 9 => "y"; }
	 upTake(...upSkip());
	 function upFloat() { yield 1.5 => "x"; }
	 try { upTake(...upFloat()); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 function upByRef($x, &$r) { $r = "changed"; }
	 $upHolder = "before";
	 upByRef(...upInt());
	 var_dump($upHolder);
	 try { upTake(...new stdClass); } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	// A class-typed parameter admits null only when the declaration says so --
	// the `?` it carries, or the one an `= null` default confers. And a
	// **by-reference** parameter is verified too, with the coercion written back
	// through the box: what would break the link the `&` makes is copying the
	// slot, not writing through it.
	`class TpReal {}
	 function tpPlain(TpReal $a) {}
	 function tpNullable(?TpReal $a) {}
	 function tpDefaulted(TpReal $a = null) {}
	 try { tpPlain(null); echo "plain: accepted\n"; } catch (TypeError $e) { echo "plain: ", $e->getMessage(), "\n"; }
	 try { tpNullable(null); echo "nullable: accepted\n"; } catch (TypeError $e) { echo "nullable: ", $e->getMessage(), "\n"; }
	 try { tpDefaulted(null); echo "defaulted: accepted\n"; } catch (TypeError $e) { echo "defaulted: ", $e->getMessage(), "\n"; }
	 function tpByRefInt(int &$r) { var_dump($r); }
	 $tpS = "21"; tpByRefInt($tpS); var_dump($tpS);
	 $tpBad = [1];
	 try { tpByRefInt($tpBad); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($tpBad);
	 function tpByRefClass(TpReal &$r) {}
	 $tpN = null;
	 try { tpByRefClass($tpN); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 function tpYieldNull() { yield null; }
	 try { tpByRefClass(...tpYieldNull()); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// A **variadic** parameter collects what no parameter claimed, under its
	// name. A named argument may claim a declared parameter while the others go
	// to the variadic, and the frame records how each one arrived.
	`function nvV($a, ...$rest) { var_dump($a, $rest); }
	 nvV(1, 2, 3);
	 nvV(1, x: "X", y: "Y");
	 nvV(a: 1, z: "Z");
	 function nvW(...$all) { var_dump($all); }
	 nvW(1, k: "K");
	 nvW(p: 1, q: 2);
	 function nvTraced($a, ...$rest) { var_dump(debug_backtrace()[0]["args"]); }
	 nvTraced(1, 2, x: 3, y: 4);
	 function nvTyped(int ...$nums) { var_dump($nums); }
	 nvTyped(1, second: 2);
	 try { nvTyped(1, bad: "x"); } catch (TypeError $e) { echo get_class($e), "\n"; }
	 // A name never claims the variadic parameter itself, and an extra named
	 // argument is not one of the call's arguments to func_num_args().
	 function nvNamed($a, string ...$extra) { var_dump($a, $extra, func_num_args(), func_get_args()); }
	 nvNamed(b: "b", a: "a", c: "c", extra: "extra");
	 nvNamed("a", "b", "c", d: "d");`,
	// **Two closures written on one line share `{closure:file:line}`**, which is
	// the name PHP gives them -- so a table keyed by that name keeps only one
	// and both references reach the same body.
	`$clPair = [fn() => "first", fn() => "second"];
	 foreach ($clPair as $i => $f) { echo $i, "=", $f(), "\n"; }
	 $clMixed = [function () { return "a"; }, function () { return "b"; }];
	 echo $clMixed[0](), $clMixed[1](), "\n";
	 // An exception's trace omits the receiver; debug_backtrace() supplies it.
	 function clInner($x) { throw new RuntimeException("boom", 7); }
	 function clOuter($a, $b) { clInner($a); }
	 try { clOuter("one", 2); } catch (RuntimeException $e) {
	   foreach ($e->getTrace() as $i => $frame) {
	     echo $i, ": ", $frame["function"], " args=", count($frame["args"] ?? []), "\n";
	   }
	   var_dump(count($e->getTrace()));
	 }
	 try { throw new Exception("top"); } catch (Exception $e) { var_dump($e->getTrace()); }
	 // A sensitive-parameter argument is not shown: the frame still holds the
	 // value, the trace renders a placeholder, and the wrapper shows no
	 // properties to anything that walks a table.
	 function clLogin($user, #[\SensitiveParameter] $password, $note) { throw new Exception("nope"); }
	 try { clLogin("bob", "hunter2", "x"); } catch (Exception $e) {
	   echo $e->getTraceAsString(), "\n";
	   $clArgs = $e->getTrace()[0]["args"];
	   echo get_class($clArgs[1]), " props=", count(get_object_vars($clArgs[1])), "\n";
	   var_dump($clArgs[0], $clArgs[2], $clArgs[1]->getValue());
	 }`,
	// serialize writes the **storage** property table, an enum case writes its
	// name rather than its state, __serialize replaces the whole state while
	// __sleep only selects, and a repeat is a back-reference rather than a
	// second copy.
	`class SerP { public $pub = 1; protected $prot = 2; private $priv = 3; }
	 class SerQ extends SerP { private $qpriv = 4; public $qpub = 5; }
	 enum SerSuit: string { case Hearts = "H"; }
	 var_dump(serialize(null), serialize(7), serialize(1.5), serialize("a\"b"));
	 echo str_replace("\0", "@", serialize(new SerP)), "\n";
	 echo str_replace("\0", "@", serialize(new SerQ)), "\n";
	 var_dump(serialize(SerSuit::Hearts));
	 class SerSleep { public $a = 1; public $b = 2; public $c = 3; function __sleep() { return ["c", "a"]; } }
	 class SerDesc { public $x = 1; function __serialize(): array { return ["k" => "v"]; } }
	 class SerBoth { public $x = 1; function __sleep() { return ["x"]; } function __serialize(): array { return ["s" => 1]; } }
	 var_dump(serialize(new SerSleep), serialize(new SerDesc), serialize(new SerBoth));
	 class SerOdd { public $here = 1; public int $blank; function __sleep() { return ["here", "blank", "nope"]; } }
	 var_dump(serialize(new SerOdd));
	 $serO = new SerP;
	 var_dump(serialize([$serO, $serO]));
	 $serR = 1; $serPair = [&$serR, &$serR];
	 var_dump(serialize($serPair));
	 var_dump(serialize(NAN), serialize(INF), serialize(-INF));`,
	// unserialize is the mirror of the writer. An enum case is its identity, a
	// repeated object comes back as one object and a repeated reference as one
	// **box** -- which is why `R:` has to box the storage the element occupies
	// rather than the value that was recorded, and mark it as a reference the
	// program can see so copying the array keeps it.
	`class UnP { public $pub = 1; protected $prot = 2; private $priv = 3; }
	 enum UnSuit: string { case Hearts = "H"; case Spades = "S"; }
	 class UnWake { public $a = 1; public $woke = false; function __wakeup(): void { $this->woke = true; } }
	 class UnTake { public $x = 0; function __unserialize(array $d): void { $this->x = $d["x"] * 10; } }
	 foreach ([null, true, false, 7, 1.5, "s", "", [1, 2], ["k" => "v"], [[1], [2]]] as $v) {
	   var_dump(unserialize(serialize($v)));
	 }
	 var_dump(get_object_vars(unserialize(serialize(new UnWake))));
	 var_dump(unserialize(serialize(new UnTake))->x);
	 var_dump(str_replace("\0", "@", serialize(unserialize(serialize(new UnP)))));
	 var_dump(unserialize(serialize(UnSuit::Hearts)) === UnSuit::Hearts);
	 $unO = new UnP;
	 $unBoth = unserialize(serialize([$unO, $unO]));
	 var_dump($unBoth[0] === $unBoth[1]);
	 $unR = 1; $unPair = [&$unR, &$unR];
	 $unBack = unserialize(serialize($unPair));
	 $unBack[0] = 9;
	 var_dump($unBack[1]);
	 var_dump(unserialize("i:1;x"), unserialize("a:2:{i:0;i:1;}"), unserialize("junk"), unserialize(""));
	 var_dump(unserialize("s:5:\"ab\";"), unserialize("E:9:\"UnSuit:No\";"));`,
	// The engine's own classes that refuse to be written at all -- a program
	// that merely *uses* one never declares it, so the harvest's per-class flag
	// does not cover it. And a library function gets a **frame**, which is what
	// the trace names when one of them throws.
	`function nsGen() { yield 1; }
	 foreach ([nsGen(), function () {}] as $v) {
	   try { serialize($v); echo "allowed\n"; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 }
	 try { serialize(nsGen()); } catch (Exception $e) {
	   foreach ($e->getTrace() as $i => $frame) { echo $i, ": ", $frame["function"], "\n"; }
	 }`,
	// A call whose callee is unknown at compile time sends its named arguments
	// trailing under their names, and the **callee** places them: only it knows
	// which position each claims. A name it cannot claim, with no variadic to
	// collect it, is an Error -- and nobody else can report it, because the call
	// site had no signature to check against.
	`function ndF(string $a = "a", string $b = "b", string $c = "c") { echo "$a|$b|$c\n"; }
	 $ndG = ndF(...);
	 $ndG(c: "C");
	 $ndG("A", c: "C");
	 $ndG(b: "B", c: "C");
	 $ndH = function ($x = 1, $y = 2) { echo "$x/$y\n"; };
	 $ndH(y: 9);
	 try { $ndH(nope: 1); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 $ndV = function ($first, ...$rest) { var_dump($first, $rest); };
	 $ndV(1, tail: "T");
	 // A name never claims the variadic parameter itself, whichever side
	 // places it.
	 $ndW = function ($head, ...$extra) { var_dump($head, $extra); };
	 $ndW(1, extra: "E");
	 $ndW(head: 2, other: "O");`,
	`echo intdiv(7, 2), " ", intdiv(-7, 2);`,
	`try { echo intdiv(1, 0); } catch (DivisionByZeroError $e) { echo "caught"; }`,
	`class C { public $n = 1; } $o = new C(); $o->n++; ++$o->n; echo $o->n;`,
	`class C { public static $n = 0; static function bump() { self::$n++; } } C::bump(); C::bump(); echo C::$n;`,

	`$name = "x"; $x = 42; echo $$name;`,
	`$name = "y"; $$name = 7; echo $y;`,

	// finally, which the compiler turns into a subroutine call.
	`function f() { try { return "t"; } finally { echo "F"; } } echo f();`,
	`try { echo "a"; } finally { echo "b"; } echo "c";`,
	`function f() { try { throw new Exception("e"); } finally { echo "F"; } } try { f(); } catch (Exception $e) { echo $e->getMessage(); }`,
	`try { try { throw new Exception("x"); } finally { echo "F"; } } catch (Exception $e) { echo "C"; }`,
	`function f() { try { return 1; } finally { return 2; } } echo f();`,
	`try { throw new Exception("m"); } catch (Exception $e) { echo "c"; } finally { echo "f"; }`,

	// list() destructuring, in its several spellings.
	`[$a, $b] = [1, 2]; echo $a, $b;`,
	`list($a, $b) = [1, 2]; echo $a, $b;`,
	`["x" => $p] = ["x" => 9]; echo $p;`,
	`foreach ([[1,2],[3,4]] as [$m, $n]) { echo $m, $n; }`,
	`list($q, list($r)) = [1, [2]]; echo $q, $r;`,
	`[, $second] = [1, 2]; echo $second;`,

	// A property bound to a reference.
	`class C { public $p = 1; } $o = new C(); $x = 5; $o->p = &$x; $x = 9; echo $o->p;`,
	`class C { public $p = 1; } $o = new C(); $x = 5; $o->p = &$x; $o->p = 7; echo $x;`,

	// Casts between the composite types.
	`$o = (object)["x" => 1]; echo $o->x;`,
	`$o = (object)["x" => 1]; $a = (array)$o; echo $a["x"];`,
	`var_dump((array)"s"); var_dump((array)null);`,
	`class C { public $a = 1; public $b = 2; } var_dump((array)(new C()));`,
	`$o = (object)"hello"; echo $o->scalar;`,

	// A method call or property write on a non-object is a catchable Error.
	`$x = null; try { $x->m(); } catch (Error $e) { echo $e->getMessage(); }`,
	`$x = 5; try { $x->p = 1; } catch (Error $e) { echo $e->getMessage(); }`,

	// A static initialized by an expression runs its initializer once.
	`function c() { static $n = PHP_INT_SIZE; $n++; return $n; } echo c(), c();`,

	// Generators, which suspend a frame rather than running to completion.
	`function g() { yield 1; yield 2; yield 3; } foreach (g() as $v) { echo $v; }`,
	`function g() { yield 1; yield 2; } foreach (g() as $k => $v) { echo "$k=$v,"; }`,
	`function g() { yield "a" => 1; yield "b" => 2; } foreach (g() as $k => $v) { echo "$k$v"; }`,
	`function g() { return; yield; } foreach (g() as $v) { echo "never"; } echo "done";`,
	`function g() { yield 1; return 9; } $it = g(); foreach ($it as $v) { echo $v; } echo $it->getReturn();`,
	`function g() { yield 1; yield 2; } $it = g(); echo $it->current(), $it->key(); $it->next(); echo $it->current(), $it->key();`,
	`function g() { yield 1; } $it = g(); echo $it->valid() ? "y" : "n"; $it->next(); echo $it->valid() ? "y" : "n";`,
	`function g($n) { for ($i = 0; $i < $n; $i++) { yield $i * 2; } } foreach (g(4) as $v) { echo $v; }`,
	`function g() { $x = yield 1; echo "[$x]"; yield 2; } $it = g(); echo $it->current(); echo $it->send("S"); `,
	`function g() { yield 1; yield from [2, 3]; yield 4; } foreach (g() as $v) { echo $v; }`,
	`function inner() { yield 2; yield 3; return "R"; } function outer() { yield 1; $r = yield from inner(); echo "[$r]"; yield 4; } foreach (outer() as $v) { echo $v; }`,
	`function g() { yield 1; throw new Exception("boom"); } try { foreach (g() as $v) { echo $v; } } catch (Exception $e) { echo $e->getMessage(); }`,
	`function g() { static $n = 0; $n++; yield $n; } foreach (g() as $v) { echo $v; } foreach (g() as $v) { echo $v; }`,
	`function g() { foreach ([1,2] as $v) { yield $v; } } foreach (g() as $v) { echo $v; }`,
	`function g() { yield 1; } $it = g(); echo $it instanceof Generator ? "T" : "F", get_class($it);`,
	`function g() { yield 3 => "a"; yield "b"; } foreach (g() as $k => $v) { echo "$k$v"; }`,

	// Null coalescing and argument unpacking.
	`$a = null; echo $a ?? "d";`,
	`$a = 0; echo $a ?? "d";`,
	`echo $nope ?? "d";`,
	`$a = ["k" => null]; echo $a["k"] ?? "d", $a["z"] ?? "e";`,
	`function f($a, $b, $c) { return "$a$b$c"; } echo f(...[1,2,3]);`,
	`function f($a, $b) { return $a + $b; } $args = [1, 2]; echo f(...$args);`,
	`function f(...$rest) { return count($rest); } echo f(1,2,3);`,

	// Enums, whose cases the engine stores as class constants but which are
	// singleton objects of the enum's own type.
	`enum S { case A; case B; } echo S::A->name;`,
	`enum S { case A; } var_dump(S::A === S::A);`,
	`enum S: string { case A = "x"; } echo S::A->value, S::A->name;`,
	`enum S: int { case A = 1; case B = 2; } echo S::from(2)->name;`,
	`enum S: int { case A = 1; } var_dump(S::tryFrom(9));`,
	`enum S { case A; case B; } echo count(S::cases()), S::cases()[1]->name;`,
	`enum S: string { case A = "x"; const K = "k"; } echo S::K, S::A->value;`,
	`enum S { case A; } $v = S::A; echo $v instanceof S ? "T" : "F", get_class($v);`,
	`enum S: int { case A = 1; } try { S::from(7); } catch (ValueError $e) { echo $e->getMessage(); }`,
	`enum S { case A; public function label(): string { return "L" . $this->name; } } echo S::A->label();`,
	`enum S { case A; case B; } foreach (S::cases() as $c) { echo $c->name; }`,

	// Object identity and equality, which are not the same relation.
	`class C { public $n = 1; } $a = new C(); $b = new C(); var_dump($a === $b, $a == $b, $a === $a);`,
	`class C { public $n = 1; } $a = new C(); $b = new C(); $b->n = 2; var_dump($a == $b);`,
	`class C {} class D {} var_dump(new C() == new D());`,
	`class C { public $n = 1; } $a = new C(); $b = $a; var_dump($a === $b);`,

	// Program-level builtins that need the class table or the interpreter.
	// register_shutdown_function and set_exception_handler are absent here on
	// purpose: both only show their effect when the request ends, which the
	// oracle's eval() never does. They are covered by .phpt cases instead.
	`class C {} echo class_exists("C") ? "y" : "n", class_exists("Z") ? "y" : "n";`,
	`interface I {} echo interface_exists("I") ? "y" : "n", class_exists("I") ? "y" : "n";`,
	`enum E { case A; } echo enum_exists("E") ? "y" : "n";`,
	`function f() {} echo function_exists("f") ? "y" : "n", function_exists("nope") ? "y" : "n";`,
	`echo function_exists("count") ? "y" : "n";`,
	`class C { function m() {} } echo method_exists("C", "m") ? "y" : "n", method_exists("C", "z") ? "y" : "n";`,
	`class C { public $p = 1; } echo property_exists("C", "p") ? "y" : "n", property_exists("C", "z") ? "y" : "n";`,
	`class B {} class D extends B {} echo get_parent_class(new D());`,
	`class C {} var_dump(get_parent_class(new C()));`,
	`class C { const K = 1; } class_alias("C", "A"); echo A::K, class_exists("A") ? "y" : "n";`,

	// match and switch, both driven by a constant jump table.
	`function f($x) { return match($x) { 1 => "a", 2 => "b", 3, 4 => "cd", default => "z" }; } echo f(1), f(2), f(3), f(4), f(9);`,
	`echo match(true) { 1 > 2 => "no", default => "yes" };`,
	`function f($s) { return match($s) { "a" => 1, "b" => 2, default => 0 }; } echo f("a"), f("b"), f("q");`,
	`try { echo match(9) { 1 => "a" }; } catch (\UnhandledMatchError $e) { echo $e->getMessage(); }`,
	`try { echo match("q") { 1 => "a" }; } catch (\UnhandledMatchError $e) { echo $e->getMessage(); }`,
	`function f($x) { switch ($x) { case 1: return "one"; case 2: return "two"; default: return "other"; } } echo f(1), f(2), f(5);`,
	`function f($x) { switch ($x) { case "a": return 1; case "b": return 2; default: return 0; } } echo f("a"), f("b"), f("z");`,
	`$n = 2; switch ($n) { case 1: echo "1"; case 2: echo "2"; case 3: echo "3"; break; case 4: echo "4"; } echo "|";`,

	// Inline type checks.
	`var_dump(is_int(1), is_int("1"), is_string("s"), is_float(1.5), is_bool(true), is_null(null), is_array([]));`,
	`$o = new stdClass(); var_dump(is_object($o), is_object(1));`,
	`var_dump(is_scalar(1), is_scalar([]), is_numeric("12"), is_numeric("x"));`,

	// global, which aliases a name from the global scope.
	`$g = 1; function f() { global $g; $g = 5; } f(); echo $g;`,
	`$g = 1; function f() { global $g; return $g; } echo f();`,
	`function f() { global $fresh; $fresh = "made"; } f(); echo $fresh;`,

	// Short-circuit operators that keep their value.
	`$a = 0; $b = ($a && print("no")) ? "t" : "f"; echo $b;`,
	`var_dump(true && false, false || true, 1 && 2);`,

	// First-class callable syntax.
	`$f = strlen(...); echo $f("hello");`,
	`function g($x) { return $x * 2; } $f = g(...); echo $f(21);`,
	`class C { function m($x) { return $x + 1; } } $c = new C(); $f = $c->m(...); echo $f(41);`,
	`class C { static function s($x) { return $x * 3; } } $f = C::s(...); echo $f(5);`,

	// Closure rebinding and conversion.
	`class C { public $v = 7; } $f = function () { return $this->v; }; $b = Closure::bind($f, new C(), C::class); echo $b();`,
	`class C { public $v = 3; } $f = function () { return $this->v; }; $b = $f->bindTo(new C(), C::class); echo $b();`,
	`class C { public $v = 9; } $f = function () { return $this->v; }; echo $f->call(new C());`,
	`$f = Closure::fromCallable("strlen"); echo $f("abcd");`,
	`function g($x) { return $x + 1; } $f = Closure::fromCallable("g"); echo $f(41);`,
	`class C { function m() { return "m"; } } $f = Closure::fromCallable([new C(), "m"]); echo $f();`,

	// String offsets, which assign one byte in place rather than growing.
	`$s = "abc"; $s[0] = "X"; echo $s;`,
	`$s = "abc"; $s[1] = "YZ"; echo $s;`,
	`$s = "abc"; $s[5] = "X"; echo $s, "|", strlen($s);`,
	`$s = "abc"; $s[-1] = "Z"; echo $s;`,
	`$s = "abc"; echo $s[0], $s[-1];`,

	// Callability and class introspection.
	`function g() {} var_dump(is_callable("g"), is_callable("nope"), is_callable("strlen"));`,
	`var_dump(is_callable(function () {}), is_callable([new stdClass(), "m"]));`,
	`class C { function a() {} function b() {} } $m = get_class_methods("C"); sort($m); echo implode(",", $m);`,
	`class C { public $x = 1; public $y = 2; } var_dump(get_object_vars(new C()));`,
	`echo gc_collect_cycles();`,

	// The sort family, which rewrites its argument through a reference.
	`$a = [3,1,2]; sort($a); echo implode(",", $a);`,
	`$a = [3,1,2]; rsort($a); echo implode(",", $a);`,
	`$a = ["b"=>2, "a"=>1]; ksort($a); echo implode(",", array_keys($a));`,
	`$a = ["b"=>2, "a"=>1]; asort($a); echo implode(",", array_keys($a));`,
	`$a = ["b"=>2, "a"=>1]; arsort($a); echo implode(",", array_keys($a));`,
	`$a = ["x"=>1, "y"=>2]; sort($a); var_dump($a);`,

	// Diagnostics, which PHP prints into the script's own output.
	`$a = [1]; echo $a[5]; echo "|";`,
	`$a = ["k"=>1]; echo $a["z"]; echo "|";`,
	`echo $undefined; echo "|";`,
	`$a = [1]; echo @$a[5]; echo "|";`,

	// Write fetches, which create what they write through and stay silent about
	// it. The read-modify-write forms do warn, because they read first, and the
	// two are easy to conflate: every case below distinguishes them.
	`function t(&$v) {} t($arr[]); var_dump($arr);`,
	`$b[0] = 1; var_dump($b);`,
	`$b[0][1] = 1; var_dump($b);`,
	`$c[0] .= "x"; var_dump($c);`,
	`$a = ["x"=>1]; $a["y"] .= "q"; var_dump($a);`,
	`$f = false; $f["k"] = "v"; var_dump($f);`,
	`class C { public $p = 1; } $o = new C(); $o->p += 5; var_dump($o->p);`,
	`$g = null; echo $g->r; echo "|"; $s = "x"; echo $s->q; echo "|"; $i = 1; echo $i->q;`,
	`$o = new stdClass(); var_dump(isset($o->nope), $o->nope ?? "fallback");`,
	`$n = null; var_dump(isset($n->nope), $n->nope ?? "fallback");`,
	`$a = ["k"=>1]; var_dump($a["z"] ?? "fallback"); echo $a["z"];`,

	// Uncaught exceptions, whose whole report -- message, creation site and
	// stack trace, arguments included -- is printed to the script's own output
	// and so is what a test compares against.
	`function g($x) { throw new Exception("boom"); } function h() { g(42); } h();`,
	`class C { function m($s, $a, $o, $b, $n, $f) { throw new RuntimeException("x"); }
	  static function s() { (new C)->m("a-very-long-string-indeed", [1], new stdClass, true, null, 1.5); } } C::s();`,
	`throw new Error("top level");`,
	`throw new Exception();`,
	`class F { public array $b = []; function add($v) { $this->b[] = $v; } }
	  $f = new F(); $f->add("x"); var_dump($f->b);`,
	`class F { public $b; function set() { $this->b["k"] = 1; $this->b["k"] += 2; } }
	  $f = new F(); $f->set(); var_dump($f->b);`,
	`function f() { $x = null; $x->p = 1; } f();`,
	`function f() { return 1 % 0; } f();`,
	`$e = new Exception("m"); var_dump($e->getLine(), $e->getMessage(), $e->getTraceAsString());`,
	// The trace's file is the harness's temporary path, and inside a var_dump
	// even its length shows, so these compare the shape rather than the text.
	`class C { function m() { return new Exception("m"); } }
	  function f() { return (new C)->m(); } $t = f()->getTrace();
	  foreach ($t as $frame) { echo $frame["function"], "/", $frame["class"] ?? "-", "/", $frame["type"] ?? "-", "\n"; }`,
	`function f($a) { throw new Exception("m"); }
	  try { f(7); } catch (Exception $e) { $t = $e->getTrace();
	    echo count($t), ":", $t[0]["function"], ":", implode(",", $t[0]["args"]), "\n"; }`,

	// error_reporting, which many tests set at the top of the file and whose
	// effect on later diagnostics is the whole point of setting it.
	`error_reporting(0); echo $x; echo "|";`,
	`error_reporting(E_ALL); echo $y; echo "|";`,
	`var_dump(error_reporting());`,
	`error_reporting(E_ALL & ~E_WARNING); echo $z; echo "done";`,
	`error_reporting(0); $a = [1]; echo $a[9]; echo "quiet";`,
	`var_dump(error_reporting(0), error_reporting());`,

	// References define what they point at. Taking one of an unset variable
	// makes it null silently -- it is not a read -- which is why none of these
	// warn even though every target starts undefined.
	`$c = &$undef; var_dump($c, $undef);`,
	`function u(&$q) {} u($z); var_dump($z);`,
	`$l = function () use (&$j) { return ++$j; }; $l(); $l(); var_dump($j);`,
	`$l = function () use ($i) { return $i; }; var_dump($l());`,
	`function t(&$v) { $v = 9; } t($n["k"]); var_dump($n);`,
	`$a = [3,1]; sort($a); var_dump($a);`,

	// Operand types. PHP 8 draws three lines where PHP 7 drew one: a wholly
	// numeric string converts silently, one that merely starts with a number
	// warns, and anything else is a TypeError.
	`function t(callable $f) { try { var_dump($f()); } catch (\Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }
	  t(fn() => "abc" + 2); t(fn() => "5abc" + 2); t(fn() => "5" + 2); t(fn() => " 12 " + 1);
	  t(fn() => "5.5" % 2); t(fn() => 1.5 & 2); t(fn() => 2.0 % 2);
	  t(fn() => [1] + 2); t(fn() => [1] + [2]); t(fn() => [1] . 2); t(fn() => [1] ^ [2]);
	  t(fn() => "abc" & "abd"); t(fn() => null + [1]); t(fn() => new stdClass() - 1);
	  t(fn() => "" * 2); t(fn() => "abc" <=> 2); t(fn() => "0x1A" + 1);`,
	`$x = "abc"; try { $x += 1; } catch (\Throwable $e) { echo $e->getMessage(), "\n"; }`,
	`$x = "5abc"; $x .= "!"; var_dump($x); $y = "5abc"; $y *= 2; var_dump($y);`,
	`var_dump((string)[1]);`,
	`echo [1], "|";`,
	`try { echo UNDEFINED_CONSTANT; } catch (\Error $e) { echo get_class($e), ": ", $e->getMessage(); }`,
	// A program's own mistake is a catchable PHP error, not a gap in the
	// interpreter; each of these used to escape as a bare Go error.
	`function t(callable $f) { try { var_dump($f()); } catch (\Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }
	  class A { const B = 1; }
	  t(fn() => constant("A::NOPE")); t(fn() => constant("NOPE")); t(fn() => A::NOPE);
	  t(function () { $o = 1; return clone $o; });
	  t(function () { throw "string"; });`,
	`function bar($x) { return $x; }
	  try { bar(); } catch (\Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`function baz($x, $y = 2) { return $x; }
	  try { baz(); } catch (\Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`try { new NoSuchClassAnywhere(); } catch (\Error $e) { echo get_class($e), ": ", $e->getMessage(); }`,

	// Late static binding, where static:: names the class the call site used
	// and self:: names the one whose body is running. An inherited static
	// method reached through a subclass is where the two part company.
	`class A { static function make() { return new static(); } static function who() { return static::class; }
	   static function mine() { return self::class; } } class C extends A {}
	  var_dump(get_class(C::make()), get_class(A::make()), C::who(), A::who(), C::mine());`,
	`class A { static function make() { return new self(); } } class C extends A {} var_dump(get_class(C::make()));`,
	`class A { function make() { return new static(); } } class C extends A {} var_dump(get_class((new C())->make()));`,
	`class A { static function f() { return static::g(); } static function g() { return "A"; } }
	  class C extends A { static function g() { return "C"; } } echo C::f(), A::f();`,

	// Method dispatch through $this, which the compiler encodes as an unused
	// operand rather than a variable, and through a trait or an inherited body.
	`class A { function f() { return "f"; } function g() { return "g" . $this->f(); } } echo (new A())->g();`,
	`class P { function f() { return "pf"; } function g() { return $this->f(); } }
	  class Q extends P { function f() { return "qf"; } } echo (new Q())->g();`,
	`abstract class A { abstract function f(); function g() { return "g" . $this->f(); } }
	  class B extends A { function f() { return "f"; } } echo (new B())->g();`,
	`trait T { public function hello() { return "hi " . self::class; } } class C { use T; } echo (new C())->hello();`,
	`trait T { public function who() { return static::class; } } class C { use T; } class D extends C {} echo (new D())->who();`,
	`class A { private $v = 7; function get() { return $this->v; } } echo (new A())->get();`,
	// Declaration order, which var_dump and foreach both expose. Sorting the
	// harvested properties by name once made every multi-property dump wrong.
	`class A { public $p = 1; public $q = "x"; public $arr = []; }
	  class B extends A { public $z = 3; public $a = 4; } var_dump(new A(), new B());`,
	`class A { public $zz = 1; public $aa = 2; } foreach (new A() as $k => $v) { echo $k, "=", $v, ","; }`,
	`class A { public $zz = 1; public $aa = 2; } var_dump(get_object_vars(new A()));`,
	`class A { public $p = 1; public $q = 2; function t() { $this->p++; $this->q .= "y"; var_dump($this); } } (new A())->t();`,

	// Magic accessors, which turn access to a property or method the class does
	// not have into a call. isset() is answered by __isset alone while empty()
	// goes on to ask __get, and the two must not be conflated.
	`class M {
	    private $d = ["a" => 1, "n" => null];
	    function __get($n) { echo "get($n) "; return $this->d[$n] ?? null; }
	    function __set($n, $v) { echo "set($n) "; $this->d[$n] = $v; }
	    function __isset($n) { echo "isset($n) "; return isset($this->d[$n]); }
	    function __unset($n) { echo "unset($n) "; unset($this->d[$n]); }
	    function __call($m, $args) { return "call($m:" . implode(",", $args) . ")"; }
	    static function __callStatic($m, $args) { return "static($m:" . implode(",", $args) . ")"; }
	    function dump() { var_dump($this->d); }
	  }
	  $m = new M(); var_dump($m->a, $m->zz); $m->b = 5;
	  var_dump(isset($m->a), isset($m->n), isset($m->zz), empty($m->a), empty($m->zz));
	  unset($m->a); $m->dump();
	  echo $m->doThing(1, 2), "\n", M::staticThing(3, 4), "\n";`,
	// A declared property is reached directly; __get only covers what is
	// missing, and must not shadow what the object holds.
	`class M { public $real = 1; function __get($n) { return "magic:$n"; } }
	  $m = new M(); var_dump($m->real, $m->other);`,
	// __get returning through the same property must not re-enter itself.
	`class M { function __get($n) { return $this->$n; } } $m = new M(); var_dump(@$m->x);`,

	// A callee the program got wrong is a PHP Error, and each shape of mistake
	// has its own wording. A callee the runtime merely has not implemented must
	// stay a gap, so these all name things that genuinely do not exist.
	`class C { function m($x) { return "m$x"; } static function s($x) { return "s$x"; } }
	  function t($f) { try { var_dump($f(1)); } catch (\Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }
	  $o = new C(); t([$o, "m"]); t(["C", "s"]); t([$o, "nope"]); t("nofunc");
	  t("C::nope"); t("Nope::m"); t([1, 2, 3]); t(new C()); t(42);`,
	`class C {} $o = new C();
	  try { $o->nope(); } catch (\Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	  try { C::snope(); } catch (\Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,

	// An argument whose by-reference-ness the compiler could not settle, so the
	// runtime decides: by reference it creates the element, by value it reads
	// and warns.
	`function byref(&$x) { $x = 5; } $a = []; $f = "byref"; $f($a["k"]); var_dump($a);`,
	`$b = ["z" => [3,1,2]]; sort($b["z"]); var_dump($b);`,
	`function byval($x) { return $x; } $c = []; $g = "byval"; var_dump($g($c["missing"]));`,
	`function byref(&$x) { $x = 1; } $o = new stdClass(); $f = "byref"; $f($o->p); var_dump($o);`,

	// Autoloading: a class the program never declared is not necessarily
	// missing, and every place that names one has to consult the callbacks.
	`spl_autoload_register(function ($name) {
	    echo "autoload($name)\n";
	    eval("class $name { function hi() { return 'hi from $name'; } const K = 7; }");
	  });
	  $o = new Foo(); echo $o->hi(), "\n";
	  var_dump(class_exists("Bar"), class_exists("Baz", false));
	  echo Qux::K, "\n", count(spl_autoload_functions()), "\n";`,
	`spl_autoload_register(function ($name) { eval("class $name { static function s() { return 'S'; } }"); });
	  echo Thing::s();`,

	// An unqualified call inside a namespace falls back to the global function,
	// and only a function does: the namespaced name wins when it exists.
	`namespace Ns; function strlen($s) { return "own"; } echo strlen("abc"), "|", \strlen("abc");`,
	`namespace Ns; echo strlen("abc"), "|", count([1,2]), "|", \strtoupper("x");`,
	`namespace Ns; echo gettype(1);`,
	// A name that exists in neither is reported with the namespace, and the
	// error comes from the call site, so the trace has no frame for the callee.
	`namespace Ns; nope();`,

	// A constant follows the same rule: unqualified falls back to the global
	// one, a written-out namespace does not.
	`namespace Ns; define("Ns\OWN", 1); echo OWN, "|", PHP_EOL === "\n" ? "y" : "n", "|", E_ALL;`,
	`namespace Ns; echo UNKNOWN;`,

	// Constant initializers the compiler could not fold. Each depends on
	// something that does not exist until a declaration before it has run, so
	// the engine keeps the shape and evaluates it on first use.
	`const a = [1, 2, [3, [4]]];
	 const b = a[0]; const c = a[2][0]; const d = a[2];
	 const e = ["string" => [1]]["string"][0];
	 var_dump(b, c, d, e);
	 function t($x = d[1][0]) { var_dump($x); } t();`,
	`class Foo { const A = self::B; const B = "ok"; const C = self::A . "!"; }
	 var_dump(defined("Foo::A"), Foo::A, Foo::C);`,
	`const X = 1 << 3; const Y = X | 2; const Z = -X; const W = X > 4 ? "big" : "small";
	 var_dump(X, Y, Z, W);`,
	`class P { const N = 1; } class Q extends P { const M = parent::N + 1; }
	 var_dump(Q::M, Q::N);`,

	// An object is callable through __invoke, and a first-class callable on a
	// method the class does not declare wraps __call.
	`class Foo { public function __invoke($x = "d") { return "invoked $x"; } }
	 $foo = new Foo();
	 var_dump($foo("a"), (Closure::fromCallable($foo))("b"), call_user_func($foo, "c"),
	          is_callable($foo), array_map($foo, ["e"]));`,
	`class Bar {
	   public function __call($m, $a) { return $m . ":" . count($a); }
	   public static function __callStatic($m, $a) { return static::class . "::" . $m; }
	 }
	 $bar = new Bar(); $f = $bar->anythingInstance(...);
	 echo $f(), "\n", $f(1, 2), "\n";`,

	// A reference into a string offset is refused differently from an index.
	`$a = "string"; try { $b = &$a[1]; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(); }`,

	// __debugInfo replaces what a dump shows -- for var_dump and print_r, but
	// not var_export -- and returning something else is fatal.
	`class D { public $a = 1; function __debugInfo(): array { return ["x" => 9, "y" => [1]]; } }
	 var_dump(new D()); print_r(new D()); echo "\n"; var_export(new D());`,
	`class P { public $a = 1; protected $b = 2; } var_dump(new P()); print_r(new P());`,

	// An anonymous class is stored under a name with a NUL and its definition
	// site appended; every display of it stops at the NUL.
	`$o = new class { private $foo; };
	 var_dump($o); print_r($o); echo "\n";
	 try { $o->nope(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 try { $x = $o->foo; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }`,

	// A by-value argument fetch is a read, and an append has nothing to read.
	`function test($x) { test($x[][$y]); }
	 try { test([]); } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	// Deep recursion a real program would do is not a gap.
	`class foo { function __call($name, $args) {} }
	 function test($n, $x) { if ($n > 0) { $x->bug(); test($n - 1, $x); } }
	 test(3000, new foo()); echo "OK\n";`,

	// self, parent and static with no class executing are the program's
	// mistake, worded by the keyword, and catchable.
	`foreach ([fn() => static::x, fn() => parent::x, fn() => self::x, fn() => new static(),
	          fn() => static::foo(), fn() => static::$x] as $try) {
	   try { $try(); } catch (Error $e) { var_dump($e->getMessage()); }
	 }`,

	// self, parent and static in a callable name something only the executing
	// scope knows, and naming them at all is deprecated -- wherever the
	// callable is resolved, is_callable included.
	`class A { static function s() { return "s"; } }
	 class B extends A {
	   function go() {
	     var_dump(call_user_func("self::s"), call_user_func(["self", "s"]), call_user_func("parent::s"),
	              call_user_func("static::s"), is_callable("self::s"));
	   }
	 }
	 (new B())->go();`,

	// An offset a string cannot use is a TypeError when read and simply "not
	// set" when asked about, and unsetting through one has its own wording.
	`$arr = ["exists" => "foz"];
	 var_dump(isset($arr["exists"]["nope"]), isset($arr["exists"][1]), isset($arr["exists"][9]),
	          isset($arr["exists"]["1"]), empty($arr["exists"][0]), $arr["exists"]["nope"] ?? "dflt");`,
	`$s = "test"; try { unset($s["a"]["b"]); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(); }`,
	// A closure reached through the method every closure has.
	`$a = [function () { echo "123\n"; }, "__invoke"]; $a();
	 $b = Closure::fromCallable($a); $b();`,

	// A handful of small ones: $this without an instance is a program error
	// rather than a gap, a Traversable unpacks into a literal, a closure can be
	// called through the method every one of them has, an ArrayAccess supports
	// compound assignment, and trigger_error raises at a user level.
	`class S { static function m() { try { var_dump($this); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } } } S::m();`,
	`function gen() { yield 1; yield 2; } var_dump([...gen(), 3]);`,
	`$c = function ($x) { return $x * 2; }; var_dump($c->__invoke(5));`,
	`class AA implements ArrayAccess { public $d = ["k" => "v", "n" => 1];
	   function offsetExists($o): bool { return isset($this->d[$o]); }
	   function offsetGet($o): mixed { return $this->d[$o] ?? null; }
	   function offsetSet($o, $v): void { $this->d[$o] = $v; }
	   function offsetUnset($o): void { unset($this->d[$o]); } }
	 $a = new AA(); $a["k"] .= "!"; $a["n"] += 5; var_dump($a["k"], $a["n"]);`,
	`var_dump(trigger_error("msg"), trigger_error("warn", E_USER_WARNING),
	          trigger_error("dep", E_USER_DEPRECATED));`,

	// ArrayIterator and ArrayObject: engine classes with no harvested bodies,
	// so their ArrayAccess, Countable and Iterator behaviour is supplied here.
	`$a = new ArrayIterator([1, 2, 3]);
	 foreach ($a as $k => $v) { echo "$k=$v "; } echo "\n";
	 var_dump(count($a), $a[1], isset($a[5]), $a->getArrayCopy());
	 $a[] = 9; $a["k"] = 8; var_dump($a->getArrayCopy());`,
	`$o = new ArrayObject(["x" => 1]);
	 foreach ($o as $k => $v) { echo "$k=$v "; } echo "\n";
	 var_dump(count($o), $o["x"], $o->getArrayCopy(),
	          $o instanceof IteratorAggregate, (new ArrayIterator([])) instanceof Traversable);`,
	// **var_export shows the storage flat**, with no `storage` entry -- the
	// opposite of what var_dump shows for the same object, and an integer key
	// is written bare where a property name would be quoted.
	`var_export(new ArrayObject([1, 2])); echo "\n";
	 var_export(new ArrayIterator(["a" => 1, 7 => 2])); echo "\n";
	 var_dump(new ArrayObject([1, 2]));`,
	// The constructor's declared type is `array|object`, and the TypeError
	// names the **engine** class even when a subclass is being built.
	`class Sub extends ArrayObject {}
	 foreach ([["ArrayIterator", "s"], ["ArrayObject", 42], ["ArrayIterator", null],
	           ["ArrayObject", 1.5], ["ArrayIterator", true], ["Sub", "x"]] as [$c, $v]) {
	   try { new $c($v); echo "$c ok\n"; }
	   catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 $o = new stdClass; $o->p = 1; $w = new ArrayObject($o); $w["q"] = 2;
	 var_dump($o, $w->getArrayCopy());
	 $a = new ArrayIterator([1, 2]); $b = clone $a; $b[] = 3;
	 var_dump($a->getArrayCopy(), $b->getArrayCopy());`,
	// `ReflectionAttribute`'s constructor is **private**, and the refusal is the
	// ordinary one for that -- naming the calling scope -- rather than a bespoke
	// "use getAttributes() instead" message.
	`try { new ReflectionAttribute(); } catch (Throwable $e) { var_dump(get_class($e), $e->getMessage()); }
	 class H { static function f() { try { new ReflectionAttribute(); } catch (Throwable $e) { var_dump($e->getMessage()); } } }
	 H::f();
`,
	// Unpacking a Traversable: only int|string keys, and an integer-like
	// **string** key is an integer key -- renumbered rather than preserved.
	`function upK1() { yield [] => 1; }
	 function upK2() { yield 1.23 => 1; }
	 function upK3() { yield "42" => 1; yield "x" => 2; yield 7 => 3; }
	 foreach (["upK1", "upK2", "upK3"] as $g) {
	   try { var_export([...$g()]); echo "\n"; }
	   catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	// SeekableIterator. The refusal is an OutOfBoundsException a program can
	// catch, and it names the position it was given rather than the bound.
	`$a = new ArrayIterator([3, 1, 2]);
	 $a->seek(2); var_dump($a->current(), $a->key(), $a instanceof SeekableIterator);
	 foreach ([9, -1] as $p) {
	   try { $a->seek($p); } catch (Throwable $e) { var_dump(get_class($e), $e->getMessage()); }
	 }`,
	// A subclass gets the same store, reaches it through `parent::`, and its
	// own override wins over the engine class's body.
	`class Two extends ArrayIterator {
	   public $extra = 0;
	   public function __construct($a) { parent::__construct($a); $this->extra = count($a); }
	   public function current(): mixed { return "[" . parent::current() . "]"; }
	 }
	 $t = new Two(["x" => 1, "y" => 2]);
	 foreach ($t as $k => $v) { echo "$k=$v "; } echo "\n";
	 var_dump($t->extra, $t["x"], get_parent_class($t), $t instanceof Traversable,
	          $t instanceof ArrayIterator, count($t));`,
	// `$c instanceof Traversable` for a class that implements Iterator: the
	// `extends` edge is the **engine's**, and no program's own table carries
	// it.
	`class Own implements Iterator {
	   public function current(): mixed { return 1; }
	   public function key(): mixed { return 0; }
	   public function next(): void {}
	   public function rewind(): void {}
	   public function valid(): bool { return false; }
	 }
	 var_dump(new Own() instanceof Traversable, new Own() instanceof Iterator);`,
	`class Cnt implements Countable { function count(): int { return 42; } } var_dump(count(new Cnt()));`,

	// The internal array pointer, which parks past either end once it runs off
	// and is only brought back by reset() or end().
	`$a = [1, 2, 3];
	 var_dump(current($a), key($a), next($a), current($a), key($a), end($a), key($a),
	          prev($a), reset($a), key($a));`,
	`$e = []; var_dump(current($e), key($e), reset($e), end($e), next($e));`,
	`$b = ["x" => 1, "y" => 2];
	 var_dump(key($b), next($b), key($b), next($b), key($b), prev($b), key($b), reset($b), key($b));`,
	`$c = [1, 2, 3]; next($c); unset($c[1]); var_dump(current($c), key($c), next($c), key($c));`,

	// func_get_args reports what was passed, not what was declared, and has
	// nothing to report outside a function.
	`function f($x) { var_dump(func_num_args(), func_get_args(), func_get_arg(1));
	   try { var_dump(func_get_arg(5)); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }
	 f(1, 2, 3);
	 function g() { var_dump(func_num_args(), func_get_args()); } g();`,
	`foreach ([fn() => func_num_args(), fn() => func_get_args(), fn() => func_get_arg(0)] as $try) {
	   try { var_dump($try()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,

	// assert(): the guard opcode branches on the build's zend.assertions, and
	// the compiler supplies the source text as the default description.
	`var_dump(assert(1 == 1));
	 foreach ([fn() => assert(1 == 2), fn() => assert(false, "custom"),
	           fn() => assert(false, new RuntimeException("obj"))] as $try) {
	   try { $try(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,

	// Generator::throw raises at the yield the generator is suspended on, so a
	// try/catch inside the body is what catches it.
	`function g() { try { yield 1; yield 2; } catch (Exception $e) { echo "caught: ", $e->getMessage(), "\n"; yield 99; } }
	 $it = g(); var_dump($it->current(), $it->throw(new Exception("boom")));
	 function h() { yield 1; } $j = h(); $j->current();
	 try { $j->throw(new Exception("out")); } catch (Exception $e) { echo "escaped: ", $e->getMessage(), "\n"; }`,

	// A Traversable drives foreach, `yield from` and argument unpacking, and
	// the rewind/valid/current/key/next order is observable.
	`class It implements Iterator {
	   private $i = 0; private $d = ["a", "b", "c"];
	   function rewind(): void { echo "rewind\n"; $this->i = 0; }
	   function valid(): bool { return $this->i < count($this->d); }
	   function current(): mixed { return $this->d[$this->i]; }
	   function key(): mixed { return $this->i * 10; }
	   function next(): void { $this->i++; }
	 }
	 foreach (new It() as $k => $v) { echo "$k=$v "; } echo "\n";
	 class Agg implements IteratorAggregate { function getIterator(): Iterator { return new It(); } }
	 foreach (new Agg() as $k => $v) { echo "$k=$v "; } echo "\n";
	 function gen() { yield from new It(); yield "z"; }
	 foreach (gen() as $k => $v) { echo "$k=$v "; }`,
	`function f($a, $b, $c) { return "$a-$b-$c"; }
	 function gen() { yield 1; yield 2; yield 3; }
	 class It implements Iterator { private $i = 0; private $d = [7, 8, 9];
	   function rewind(): void { $this->i = 0; } function valid(): bool { return $this->i < 3; }
	   function current(): mixed { return $this->d[$this->i]; } function key(): mixed { return $this->i; }
	   function next(): void { $this->i++; } }
	 var_dump(f(...gen()), f(...new It()));`,

	// serialize, whose format encodes a property's visibility in its name.
	`var_dump(serialize(null), serialize(true), serialize(false), serialize(5), serialize(-1),
	          serialize(1.5), serialize(1.0), serialize("abc"), serialize(""), serialize("q\"uo"));`,
	`var_dump(serialize([1, 2]), serialize(["a" => 1, "b" => [true]]), serialize([]));`,
	`class C { public $a = 1; protected $b = 2; private $c = 3; }
	 $o = new stdClass(); $o->x = 1;
	 var_dump(serialize(new C()), serialize(new stdClass()), serialize($o));`,
	`enum E: string { case A = "a"; } var_dump(serialize(E::A), unserialize(serialize(E::A)) === E::A);`,
	`var_dump(serialize(NAN), serialize(INF), serialize(0.1));`,
	`foreach (["N;", "b:1;", "i:42;", "d:1.5;", 's:3:"abc";', 'a:2:{i:0;i:1;i:1;s:1:"x";}',
	          'a:1:{s:1:"k";a:1:{i:0;b:0;}}'] as $c) { var_dump(unserialize($c)); }`,
	`class C { public $a = 0; protected $b = 0; private $c = 0; }
	 var_dump(unserialize(serialize(new C())), unserialize(serialize(["x" => [1, 2.5, "s", null, true]])));`,
	`class W { public $v = 1; function __wakeup(): void { $this->v = 99; } }
	 var_dump(unserialize(serialize(new W())));`,
	`class S2 { public $a = 1; function __serialize(): array { return ["z" => 5]; }
	   function __unserialize(array $d): void { $this->a = $d["z"] * 2; } }
	 $t = serialize(new S2()); var_dump($t, unserialize($t));`,

	// var_dump and print_r annotate a non-public property, and a private one
	// additionally names the class that declared it.
	`class C { public $a = 1; protected $b = 2; private $c = 3; }
	 class D extends C {} $d = new D(); $d->e = 5;
	 var_dump(new C(), $d); print_r(new C()); print_r($d); print_r([1, ["k" => 2]]);`,

	// json_encode: a PHP array is a list only when its keys are exactly 0..n-1,
	// slashes are escaped, non-ASCII becomes \u, and a value JSON cannot hold
	// makes the whole call return false.
	`var_dump(json_encode([1, 2, 3]), json_encode(["a" => 1, "b" => [true, null]]),
	          json_encode("x\"y\n"), json_encode(1.5), json_encode(new stdClass()),
	          json_encode([]), json_encode(["0" => 1, "2" => 2]));`,
	`class J implements JsonSerializable { function jsonSerialize(): mixed { return ["j" => 1]; } }
	 class P2 { public $a = 1; private $b = 2; }
	 var_dump(json_encode(new J()), json_encode(new P2()),
	          json_encode("héllo"), json_encode("a/b"), json_encode(NAN));`,

	// list() by reference binds each target to the element, and the temporary
	// holding the aliased array must be released without writing through it.
	`$a = [1, 2]; [$x, $y] = $a; var_dump($x, $y);
	 [&$p, &$q] = $a; $p = 99; var_dump($a[0], $a[1]);
	 foreach ([[1, 2], [3, 4]] as [$m, $n]) { echo "$m-$n "; }
	 $rows = [[1, 2]]; foreach ($rows as [&$u, $v]) { $u = 7; } var_dump($rows[0][0]);`,

	// Writing through something that cannot hold elements. PHP words it by what
	// the container is, and words an append differently again.
	`$s = "abc";
	 foreach ([fn() => $s[0][1] = "x", fn() => $s[] = "x"] as $try) {
	   try { $try(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 $i = 5; try { $i[0] = 1; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 $t = true; try { $t[0] = 1; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }`,
	// An ArrayAccess on a write path descends through offsetGet.
	`class AA implements ArrayAccess { public $d = [];
	   function offsetExists($o): bool { return isset($this->d[$o]); }
	   function offsetGet($o): mixed { return $this->d[$o] ?? null; }
	   function offsetSet($o, $v): void { if ($o === null) { $this->d[] = $v; } else { $this->d[$o] = $v; } }
	   function offsetUnset($o): void { unset($this->d[$o]); } }
	 $a = new AA(); $a["k"] = ["x" => 1]; var_dump($a["k"]["x"]);`,

	// call_user_func's dynamic forms. A literal call is folded into a direct
	// one by the compiler, so only these reach the builtin.
	`class C { function m($x) { return "m$x"; } static function s($x) { return "s$x"; } }
	 $c = new C();
	 var_dump(call_user_func([$c, "m"], 1), call_user_func(["C", "s"], 2), call_user_func("C::s", 3),
	          call_user_func(function ($x) { return $x + 1; }, 4),
	          call_user_func_array([$c, "m"], [5]), call_user_func("strtoupper", "x"));
	 $f = "call_user_func"; var_dump($f("strlen", "abcd"));`,

	// Both of the vars builtins filter by the class whose body made the call,
	// and get_class_vars hands the properties out in a different order from
	// var_dump: the class's own first, then inherited, then the statics.
	`class A { public $p = 1; protected $q = 2; private $r = 3; public static $s = 4; public $u; }
	 class B extends A { public $t = 9; function inside() { var_dump(get_class_vars("A")); } }
	 var_dump(get_class_vars("A"), get_class_vars("B"));
	 (new B())->inside();
	 try { var_dump(get_class_vars("Nope")); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`class V { public $a = 1; protected $b = 2; private $c = 3;
	   function inside() { return get_object_vars($this); } }
	 $v = new V(); var_dump(get_object_vars($v), $v->inside());`,

	// Indexing something that is not a container, and indexing with something
	// that is not a key. Both diagnose by the value's own type name.
	`foreach ([null, 1, 1.5, true, false] as $v) { var_dump($v[0]); }`,
	`$s = "abc"; var_dump($s["1"]); var_dump($s[1.5]); var_dump($s[true]); var_dump($s[1]);
	 try { var_dump($s["x"]); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump($s[9]);`,
	`$a = [1, 2];
	 foreach ([fn() => $a[[]], fn() => $a[new stdClass()], fn() => $a[null], fn() => $a[1.9],
	           fn() => $a[true]] as $t) {
	   try { var_dump($t()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 $b = ["" => "empty"]; var_dump($b[null]);`,

	// Truncating a fractional float to an array key loses information, and PHP
	// says so wherever the key is used -- read, write, isset, unset.
	`$a = []; $a[1.5] = "w"; var_dump($a); $a[2.5] .= "x";
	 var_dump(isset($a[1.5])); unset($a[1.5]);
	 var_dump($a, array_key_exists(1.5, $a));
	 $s = "abc"; $s[1.5] = "Z"; var_dump($s);
	 $b = [1, 2]; var_dump($b[1.9]);`,

	// Binding a reference to something that has no slot behind it. Both
	// notices depend on what the value turns out to be at run time, not on how
	// it was written: a by-reference function returns a reference even when it
	// had to invent one, which is what keeps the caller quiet.
	`function f() { return 1; } $a = &f();
	 function &h() { return 5; } $b = &h();
	 function &g() { $x = 1; return $x; } $c = &g();
	 var_dump($a, $b, $c);`,
	`function &byref(&$arr) { return $arr[0]; }
	 $data = [1, 2]; $r = &byref($data); $r = 99; var_dump($data[0], $data[1]);`,

	// Property visibility. A private property of an *ancestor* is not merely
	// out of reach, it is invisible: the diagnostic is about an undefined
	// property, and writing the name creates a dynamic one beside it.
	`class P { private $a = 1; protected $b = 2; public $c = 3;
	   function readAll() { return "$this->a $this->b $this->c"; } }
	 class Q extends P { function readB() { return $this->b; } }
	 $q = new Q(); echo $q->readAll(), "|", $q->readB(), "\n";
	 foreach (["a", "b", "c"] as $n) {
	   try { echo $q->$n, "\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 try { $q->a = 9; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { unset($q->b); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump(isset($q->a), isset($q->c));`,
	`class A { private $p = 1; protected $q = 2; } $a = new A();
	 foreach ([fn() => $a->p, fn() => $a->q, fn() => $a->p = 5] as $try) {
	   try { $try(); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 var_dump(isset($a->p));
	 class B extends A {} $b = new B(); echo $b->p;`,
	// An accessor answers for a property the caller may not reach.
	`class M { private $x = 1; function __get($n) { echo "get($n)\n"; return 7; }
	   function __set($n, $v) { echo "set($n)\n"; } }
	 $m = new M(); var_dump($m->x); $m->x = 2;`,
	// readonly: writable once, and only from the class that declared it.
	`class R { public function __construct(public readonly int $v) {} function bump() { $this->v++; } }
	 $r = new R(5); var_dump($r->v);
	 foreach ([fn() => $r->v = 6, fn() => $r->bump(), fn() => $r->v .= "x"] as $try) {
	   try { $try(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 try { unset($r->v); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`class S { public readonly int $w; function set($x) { $this->w = $x; } }
	 $s = new S();
	 try { $s->w = 1; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 $s->set(3); var_dump($s->w);
	 try { $s->set(4); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }`,
	// The deprecation attached to creating an undeclared property, and the
	// opt-in that turns it off.
	`class D { public $known; } $d = new D(); $d->known = 1; $d->other = 2; var_dump($d);`,
	`#[AllowDynamicProperties] class E {} $e = new E(); $e->other = 2; var_dump($e);`,
	`$o = new stdClass(); $o->anything = 1; var_dump($o);`,

	// Parameter and return types. Weak mode converts, and the conversions that
	// lose information are deprecated rather than silent.
	`function f(int $x) { var_dump($x); } f(5); f("5"); f(5.0); f(true);`,
	`function f(int $x) { var_dump($x); }
	 foreach (["5abc", "abc", [], null, 1.5, "1.5"] as $v) {
	   try { f($v); } catch (TypeError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	`function g(?string $s) { var_dump($s); } g(null); g(1);`,
	`function h(): int { return "7"; } var_dump(h());
	 function bad(): int { return "x"; }
	 try { bad(); } catch (TypeError $e) { echo $e->getMessage(); }`,
	`class K { function m(float $f, string $s = "d") { var_dump($f, $s); } } (new K())->m(1);
	 try { (new K())->m("z"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	`function arr(array $a) {} function obj(K $k) {} class K {}
	 try { arr("s"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { obj(new stdClass()); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	`function un(int|string $v) { var_dump($v); } un(1); un("a");
	 try { un([]); } catch (TypeError $e) { echo $e->getMessage(); }`,
	`function nores(): void {} var_dump(nores());
	 function noret(): string { echo "x\n"; }
	 try { noret(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 function m(): mixed {} try { m(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 function u() {} var_dump(u());`,
	`function gen(): Generator { yield 1; } foreach (gen() as $g) { var_dump($g); }`,
	// strict_types turns the conversions off, keeping only int-to-float.
	`declare(strict_types=1);
	 function f(int $x) { var_dump($x); } f(5);
	 foreach (["5", 5.0, true, 1.0] as $v) {
	   try { f($v); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 }
	 function g(float $y) { var_dump($y); } g(3);
	 try { g("3.5"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 function h(): string { return 5; }
	 try { h(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// __toString is part of the string type rather than a coercion, and null
	// to a non-nullable *internal* parameter is deprecated where the same
	// value to a user function throws.
	`class T { function __toString() { return "T!"; } }
	 var_dump(strlen(new T()), str_repeat(new T(), 2), strlen(null), str_repeat("a", "3"));
	 function f(string $s) { var_dump($s); } f(new T());
	 try { f(null); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 function c(callable $cb) { var_dump($cb()); } c([new T(), "__toString"]);`,
	// get_class() with no argument names the class whose body is running, not
	// the object's, and says so through its own opcode.
	`class foo { function bar() { var_dump(get_class()); } }
	 class foo2 extends foo {} (new foo2())->bar();
	 try { get_class(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(); }`,

	// An engine function's parameters are checked from the harvested signature,
	// including the ones the compiler specializes into their own opcode.
	`foreach ([fn() => strlen(new stdClass()), fn() => strlen([]), fn() => str_repeat("a", []),
	          fn() => count("s"), fn() => abs("x")] as $t) {
	   try { $t(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,

	// set_error_handler: the callback sees every diagnostic, including ones
	// error_reporting would hide and @ would suppress, and only an explicit
	// false asks for the default text as well. A diagnostic raised inside the
	// handler is not fed back to it.
	`set_error_handler(function ($no, $msg) { echo "H($no,$msg)\n"; return true; });
	 error_reporting(0); echo $a; error_reporting(E_ALL); echo @$b;`,
	`set_error_handler(function ($no, $msg) { echo "H($msg)\n"; return false; }); echo $c;`,
	`set_error_handler(function ($no, $msg) { echo "H($msg)\n"; echo $inner; return true; }); echo $d;`,
	`set_error_handler(function ($no, $msg) { echo "outer\n"; return true; });
	 set_error_handler(function ($no, $msg) { echo "inner\n"; return true; });
	 echo $e; restore_error_handler(); echo $f;`,
	// A handler that throws unwinds from the point of the diagnostic.
	`set_error_handler(function ($no, $msg) { throw new Exception($msg); });
	 try { if ($z === null) { echo "unreached"; } } catch (Exception $ex) { echo "caught: ", $ex->getMessage(), "\n"; }
	 echo "okey\n";`,

	// The by-name variable opcodes carry the *name* in op1, not the variable.
	`$n = "nope"; var_dump(isset($$n)); unset($$n); echo $$n;`,
	`$n = "nope"; $$n .= "x"; var_dump($nope); $m = "made"; $$m = 5; var_dump($made);`,
	`$var_name = "unexisting"; var_dump(isset($$var_name));
	 $test = "var_name"; var_dump(isset($$$test)); unset($$var_name); unset($$$test); echo "done";`,

	// ArrayAccess: indexing an object is four ordinary method calls, and the
	// order they happen in is observable.
	`class C implements ArrayAccess {
	   private $d = [];
	   function offsetExists($o): bool { echo "exists($o)\n"; return isset($this->d[$o]); }
	   function offsetGet($o): mixed { echo "get($o)\n"; return $this->d[$o] ?? null; }
	   function offsetSet($o, $v): void { echo "set(", $o ?? "null", ")\n";
	     if ($o === null) { $this->d[] = $v; } else { $this->d[$o] = $v; } }
	   function offsetUnset($o): void { echo "unset($o)\n"; unset($this->d[$o]); }
	 }
	 $c = new C(); $c["a"] = 1; $c[] = 2;
	 var_dump($c["a"], isset($c["a"]), isset($c["z"]), empty($c["a"]));
	 unset($c["a"]); var_dump(isset($c["a"]));`,
	`$p = new stdClass();
	 try { echo $p["x"]; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { $p["x"] = 1; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,

	// Method modifiers, which are harvested rather than inferred: a static
	// method has no receiver even when called from an instance, and a trace
	// names it with :: because of that.
	`class A { static function s() { debug_print_backtrace(); var_dump(isset($this)); }
	           function m() { self::s(); var_dump(isset($this)); } } (new A())->m();`,
	// The frames are compared piecewise rather than var_dumped: every entry
	// carries the file it came from, which the two sides cannot agree on.
	`class A { function m($x) { self::s(1); } static function s($y) { g("z"); } }
	 function g($q) {
	   debug_print_backtrace();
	   foreach (debug_backtrace() as $f) {
	     echo $f["function"], "|", $f["class"] ?? "-", "|", $f["type"] ?? "-", "|",
	          get_debug_type($f["object"] ?? null), "|", count($f["args"]), "|", $f["line"], "\n";
	   }
	 }
	 (new A())->m(5);`,

	// Visibility is enforced against the declaring class, and __call answers
	// for a method the caller cannot see just as it does for a missing one.
	`class D { private function p() {} protected function q() {} private static function sp() {} }
	 class E extends D { function callp() { $this->p(); } function callq() { $this->q(); } }
	 $e = new E();
	 foreach ([fn() => $e->p(), fn() => $e->q(), fn() => $e->callp(), fn() => $e->callq(),
	           fn() => D::sp()] as $try) {
	   try { $try(); echo "ok\n"; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	`abstract class F { abstract function a(); }
	 class G extends F { function a() { echo "ok\n"; } function up() { parent::a(); } }
	 try { (new G())->up(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(); }`,
	`class H { private function m() {} function __call($n, $a) { echo "call($n)\n"; } } (new H())->m();`,

	// parent::__construct() is compiled without a method name, since the engine
	// takes the constructor from the class entry rather than looking it up.
	`class A { function __construct() { echo "A ctor\n"; } }
	 class B extends A { function __construct() { parent::__construct(); echo "B ctor\n"; } } new B();`,
	`class C {} class D extends C { function __construct() { parent::__construct(); } }
	 try { new D(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(); }`,
	`class I { private static function m() {} static function __callStatic($n, $a) { echo "static($n)\n"; } } I::m();`,

	// The sprintf family, whose format language is PHP's rather than Go's.
	`printf("[%s][%5s][%-5s][%.2s]", "abc", "abc", "abc", "abcdef");`,
	`printf("[%d][%5d][%-5d][%05d][%+d][%+d]", 42, 42, 42, 42, 42, -42);`,
	`printf("[%b][%o][%x][%X][%u]", 250, 250, 250, 250, -1);`,
	`printf("[%c][%%]", 65);`,
	`printf("[%f][%.2f][%10.2f][%-10.2f|]", 3.14159, 3.14159, 3.14159, 3.14159);`,
	`printf("[%e][%E][%.2e][%e]", 12345.6789, 12345.6789, 0.000123, 0.0);`,
	`printf("[%g][%G][%.3g][%g][%g][%g]", 0.00001234, 123456789.0, 1234.5678, 100.0, 1e20, 1e-5);`,
	`printf("[%2$s %1$s][%1$s]", "a", "b");`,
	`printf("[%'x10d][%-'x10d][%-010d][%010.4f]", 42, 42, 42, -3.14159);`,
	`printf("[%s][%s][%s][%s][%d][%d]", true, false, null, 1.5, "12abc", 3.9);`,
	`echo sprintf("%.0f|%.10f", 2.5, 1 / 3), "|", vsprintf("%s-%s", ["x", "y"]);`,
	`try { printf("%q", 1); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(); }`,
	`try { sprintf("%s %s", "x"); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(); }`,
	`try { vsprintf("%s %s", ["x"]); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(); }`,

	// var_export, whose output has to parse back to the same value -- which is
	// why its indentation is not var_dump's.
	`var_export([1, 2.0, "a'b", true, null, ["k" => [1]]]);`,
	`var_export(1.5); echo "|"; var_export(-0.0); echo "|"; var_export(1e100); echo "|"; var_export(PHP_INT_MAX);`,
	`$o = new stdClass(); $o->a = 1; var_export($o);`,
	`class C { public $x = 1; private $y = 2; } var_export(new C());`,
	`enum E { case A; } var_export(E::A); echo "|", var_export("x", true);`,

	// The array functions that call back into PHP.
	`$a = [3, 1, 2]; usort($a, fn($x, $y) => $x <=> $y); var_dump($a);`,
	`$b = ["c" => 3, "a" => 1]; uasort($b, fn($x, $y) => $x <=> $y); var_dump($b);
	 uksort($b, fn($x, $y) => strcmp($y, $x)); var_dump($b);`,
	`$log = []; $a = [17, 3, 15, 1, 13, 5, 11, 7, 9, 2, 16, 4, 14, 6, 12, 8, 10];
	 usort($a, function($x, $y) use (&$log) { $log[] = "$x:$y"; return $x <=> $y; });
	 echo serialize([$a, $log]), "\n";
	 $warnings = []; set_error_handler(function($level, $message) use (&$warnings) { $warnings[] = [$level, $message]; return true; });
	 $b = [3, 1, 2]; usort($b, fn($x, $y) => $x > $y); restore_error_handler();
	 echo serialize([$b, $warnings]), "\n";`,
	`var_dump(array_map(fn($x) => $x * 2, ["k" => 1, "j" => 2]));`,
	`var_dump(array_map(null, [1, 2], ["a", "b", "c"]), array_map(fn($x, $y) => "$x$y", [1, 2], ["a", "b"]));`,
	`var_dump(array_filter([1, 0, 2, null, 3]), array_filter(["a" => 1, "b" => 2], fn($v) => $v > 1));`,
	`var_dump(array_filter(["a" => 1, "b" => 2], fn($k) => $k == "b", ARRAY_FILTER_USE_KEY),
	          array_filter(["a" => 1, "b" => 2], fn($v, $k) => $k == "a" || $v > 1, ARRAY_FILTER_USE_BOTH));`,

	// The ordering operator, which yields the comparison rather than a boolean.
	`var_dump(1 <=> 2, "a" <=> "a", [1, 2] <=> [1], 2.5 <=> 1);`,

	// String builtins, which work on bytes rather than runes.
	`var_dump(strtoupper("aBc1"), strtolower("AbC1"), strrev("abc"));`,
	`var_dump(trim("  x  "), ltrim("xxaxx", "x"), rtrim("a\n"), trim("[a]", "[]"));`,
	`var_dump(strcmp("a", "b"), strcmp("b", "a"), strcmp("a", "a"), strcasecmp("A", "a"));`,
	`var_dump(strpos("hello", "l"), strpos("hello", "z"), strpos("hello", "l", 3), strrpos("hello", "l"));`,
	`var_dump(str_contains("abc", "b"), str_starts_with("abc", "a"), str_ends_with("abc", "c"));`,
	`var_dump(substr("hello", 1), substr("hello", 1, 3), substr("hello", -3), substr("hello", -3, 2),
	          substr("hello", 0, -1), substr("hello", 10));`,
	`var_dump(str_replace("a", "X", "banana"), str_replace(["a", "b"], ["1", "2"], "abc"),
	          str_replace(["a", "b"], "Z", "abc"), str_replace("a", "X", ["ba", "ca"]));`,
	`var_dump(str_pad("5", 3, "0", STR_PAD_LEFT), str_pad("x", 5), str_pad("x", 5, "-", STR_PAD_BOTH), str_pad("abc", 2));`,

	// Scalars.
	`var_dump(chr(65), chr(321), chr(-1), ord("A"), ord(""), ord("abc"));`,
	`var_dump(range(1, 5), range(5, 1), range(0, 10, 3), range("a", "e"), range("e", "a"), range(0, 1, 0.25));`,
	`function f() { echo "in f\n"; exit("bye\n"); } try { f(); } finally { echo "finally\n"; } echo "after\n";`,

	// Late static binding asked for by name rather than by keyword.
	`class A { static function who() { return get_called_class(); }
	           function inst() { return get_called_class(); } }
	 class B extends A {} echo B::who(), "|", A::who(), "|", (new B())->inst();`,
	`get_called_class();`,

	// Whole-array argument passing and array unpacking.
	`function f($a, $b) { return $a + $b; } $args = [1, 2];
	 echo f(...$args), "|", call_user_func_array("f", $args);`,
	`$a = [1, 2]; var_dump([...$a, 3, ...["k" => 9], ...[]]);`,

	// switch, which compiles to its own comparison opcode rather than ==.
	`function t($x) { switch ($x) { case "a": return "A"; case 0: return "zero";
	   case null: return "null"; default: return "other"; } }
	 var_dump(t("a"), t(0), t(null), t(5));`,
	`$v = 3; switch ($v) { case 1: case 2: echo "low"; break; case 3: echo "three"; break; }`,
	`switch ("x") { case "y": echo "y"; }`,

	// The short ternary, which keeps the left side rather than re-evaluating it.
	`$a = 0; $b = "x"; var_dump($a ?: "fallback", $b ?: "no", null ?: 7);`,

	// $GLOBALS: an element is a fetch against the global symbol table, and the
	// whole thing is a snapshot.
	`$x = 1; function f() { echo $GLOBALS["x"]; $GLOBALS["y"] = 2;
	   var_dump(array_key_exists("x", $GLOBALS)); } f(); echo $y;`,
	`function f() { echo $GLOBALS["nope"]; } f();`,

	// A function declared inside a conditional does not exist until it runs.
	`var_dump(function_exists("cond")); if (true) { function cond($x) { return $x * 2; } }
	 var_dump(function_exists("cond")); echo cond(21);`,

	// An enum case does not print like the object it is.
	`enum Suit: string { case Hearts = "H"; case Spades = "S"; }
	  var_dump(Suit::Hearts, Suit::cases(), Suit::from("S"), Suit::tryFrom("x"));`,
	`enum Plain { case A; case B; } var_dump(Plain::A, [Plain::B]);`,

	// A typed property with no initializer is uninitialized, which is neither
	// null nor absent: it is listed but not counted, isset() is false without
	// consulting __isset, and reading it is an Error.
	`class T { public int $a; public ?string $b; public $c; public int $d = 3; }
	  $t = new T(); var_dump($t, isset($t->a), empty($t->a), $t->a ?? "dflt");`,
	`class B { public int $p; } class D extends B {}
	  $d = new D(); try { echo $d->p; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(); }`,
	`class T { public int $a; public function __isset($n) { echo "isset($n)\n"; return true; } }
	  $t = new T(); var_dump(isset($t->a)); unset($t->a); var_dump(isset($t->a));`,
	`class T { public int $a; } $t = new T(); $t->a = 5; var_dump($t);`,

	// A class reference can be an object, which names its class rather than
	// being converted to a string.
	`$o = new stdClass(); $o->x = 1; var_dump(new $o);`,
	`class K { const C = 7; static function s() { return "s"; } } $k = new K(); echo $k::C, $k::s();`,
	`class P {} class Q extends P {} $q = new Q(); var_dump($q instanceof P, get_class(new $q));`,
	`nope();`,
	`function outer() { nope(); } outer();`,

	// Control flow.
	`$i = 0; while ($i < 5) { $i = $i + 1; } echo $i;`,
	`$t = 0; for ($i = 0; $i < 5; $i = $i + 1) { $t = $t + $i; } echo $t;`,
	`if (1 < 2) { echo "yes"; } else { echo "no"; }`,
	`$i = 0; while ($i < 3) { echo $i; $i++; }`,

	// Increment and decrement, where pre and post differ.
	`$i = 5; echo $i++; echo $i;`,
	`$i = 5; echo ++$i; echo $i;`,
	`$i = 5; echo $i--; echo $i;`,
	`$i = 5; echo --$i; echo $i;`,

	// Arrays, including the value semantics that make $a[0] = $a nest a copy
	// rather than build a cycle. Sharing the pointer instead made var_dump
	// recurse until it exhausted memory.
	`$a = [1,2,3]; var_dump($a);`,
	`$a = ["x"=>1,"y"=>2]; var_dump($a);`,
	`$a = [1,[2,3]]; var_dump($a);`,
	`$a = []; $a[] = 5; $a["k"] = 6; var_dump($a);`,
	`$a = [1,2]; $b = $a; $b[0] = 99; var_dump($a);`,
	`$a = array(); $a[0] = $a; var_dump($a);`,
	`$a = [1,2,3]; echo count($a);`,
	`$a = ["a"=>1,"b"=>2]; echo $a["b"];`,
	`echo strlen("hello");`,

	// Functions, with parameters bound by the callee's own RECV opcodes.
	`function f($x) { return $x * 2; } echo f(21);`,
	`function g($a, $b = 10) { return $a + $b; } echo g(1), g(1, 2);`,
	`function h() { return 7; } echo h();`,
	`function fact($n) { return $n < 2 ? 1 : $n * fact($n - 1); } echo fact(5);`,

	// Objects. Note the contrast with arrays: an object is a handle, so $b = $a
	// shares the instance and a mutation through $b is visible through $a.
	`class C { public $x = 1; } $o = new C(); var_dump($o);`,
	`class C { public $a = 1; public $b = "s"; public $c = [1,2]; } var_dump(new C());`,
	`class C { public $x; function __construct($v) { $this->x = $v; } } echo (new C(5))->x;`,
	`class C { function hi() { return "hello"; } } echo (new C())->hi();`,
	`class A { function f() { return "A"; } } class B extends A {} echo (new B())->f();`,
	`class P { public $v = 1; } class Q extends P { public $w = 2; } var_dump(new Q());`,
	`class A {} class B extends A {} var_dump((new B()) instanceof A);`,
	`class C { public $n = 3; function get() { return $this->n; } } echo (new C())->get();`,
	`class C {} echo get_class(new C());`,
	`class C { public $x = 1; } $a = new C(); $b = $a; $b->x = 9; echo $a->x;`,

	// Constant expressions the compiler could not fold.
	`enum A: string { case Case = "A::Case"; }
	 const A_name = A::Case->name; const A_value = A::Case->value;
	 var_dump(A_name, A_value, A::Case?->name);`,
	`const P = (42)->prop; const Q = (42)?->prop; var_dump(P, Q);`,
	`class B { public function __construct(public int $n = 7) {} }
	 function f($b = new B(3)) { var_dump($b->n); }
	 f(); f(new B());`,
	`const K = 5;
	 function h() { static $s = K + 1; $s++; var_dump($s); } h(); h();
	 class C { public function __construct(public int $n = 1) {} }
	 function g() { static $o = new C(5); $o->n++; var_dump($o->n); } g(); g();`,
	`class D { public $p = 1; } const R = (new D)->p; var_dump(R);`,

	`interface I { public const A = "A"; public const B = "B"; }
	 enum En: string implements I { case C = I::A; case D = self::B; }
	 var_dump(En::A, En::C->value, En::D->value, En::from("A"), En::tryFrom("Z"), En::cases());`,
	`enum Bad: int { case Bar = "bar"; }
	 try { var_dump(Bad::Bar); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { var_dump(Bad::Bar); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,

	`function f1($a = missing) { return $a; }
	 function f2($a = [missing]) { return $a[0]; }
	 foreach ([1, 2] as $n) {
	   try { $fn = "f$n"; var_dump($fn()); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 try { var_dump(Nope::A); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 class Has {} try { var_dump(Has::A); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,

	// Visibility as is_callable and the static-property opcodes see it.
	`class A { static protected function ma() {} static private function mp() {} }
	 class B1 extends A { static protected function ma() {} static protected function mp() {}
	   static protected function mb() {} }
	 class B2 extends A { static public function test() {
	   var_dump(is_callable("A::ma"), is_callable("A::mp"), is_callable("B1::ma"),
	            is_callable("B1::mp"), is_callable("B1::mb")); } }
	 var_dump(is_callable("B2::ma"), is_callable("B2::test"));
	 B2::test();`,
	`abstract class Ab { abstract static function bar(); }
	 interface If1 { static function bar(); }
	 abstract class Cc { static function foo() {} }
	 var_dump(is_callable(["Ab", "bar"]), is_callable("Ab::bar"), is_callable(["If1", "bar"]),
	          is_callable(["Cc", "foo"]), is_callable("Cc::foo"));
	 class Pv { private function f() {} static function __callStatic($n, $a) {} }
	 class Pw { private function f() {} function __call($n, $a) {} }
	 var_dump(is_callable([new Pv, "f"]), is_callable(["Pw", "f"]), is_callable([new Pw, "f"]));`,
	`class S { static function t() { var_dump(isset($this)); }
	   function i() { var_dump(isset($this)); } }
	 $b = new S(); S::t(); $b->t(); $b->i();`,
	`class Vis { private static $p = 7; protected static $q = 8; public static $r = 9;
	   static function ok() { var_dump(isset(Vis::$p), Vis::$p); } }
	 class VisSub extends Vis { static function t() { var_dump(isset(Vis::$q), Vis::$q); } }
	 var_dump(isset(Vis::$p));
	 $f = function() { var_dump(isset(Vis::$p)); }; $f();
	 Vis::ok(); VisSub::t(); var_dump(Vis::$r);
	 try { var_dump(Vis::$p); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 try { Vis::$q = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }`,

	`class Priv { private static $priv = 7; static function mk() { return static function() {
	   var_dump(isset(Priv::$priv), isset($this)); }; } }
	 $unscoped = static function () { var_dump(isset(Priv::$priv), isset($this)); };
	 $scoped = Priv::mk();
	 $unscoped(); $scoped();
	 ($unscoped->bindTo(null, null))(); ($scoped->bindTo(null, null))();
	 var_dump($unscoped->bindTo(new Priv, null));
	 ($unscoped->bindTo(null, "Priv"))(); ($scoped->bindTo(null, "Priv"))();`,
	`class Uses { private $v = 3; function mk() { return function() { var_dump($this->v); }; } }
	 $fn = (new Uses)->mk(); $fn();
	 var_dump($fn->bindTo(null, "static"));
	 ($fn->bindTo(new Uses, "static"))();
	 $plain = function() { var_dump(isset($this)); };
	 ($plain->bindTo(null, "static"))(); ($plain->bindTo(new Uses, "static"))();`,

	// Trait members belong to the composing class.
	`trait Ta { public $hello = "hello"; public const K = 42; }
	 trait Tb { private $world = "World!"; static $s = 1; }
	 class Comp { use Ta; use Tb; function t() { echo $this->hello, " ", $this->world, "\n"; } }
	 $c = new Comp(); $c->t();
	 var_dump($c, property_exists("Comp", "hello"), Comp::$s, get_object_vars($c),
	          Comp::K, defined("Ta::K"), defined("Comp::K"));
	 try { var_dump(Ta::K); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`var_dump(method_exists("Closure", "__invoke"), method_exists("Closure", "__INVOKE"),
	          method_exists("ArrayObject", "count"), method_exists("Exception", "getMessage"),
	          method_exists("Exception", "nope"), get_class_methods("Closure"));
	 $c = function(){}; var_dump(method_exists($c, "__invoke"), method_exists($c, "bindTo"));
	 class Par { static private function sb() {} private function pa() {} public function pc() {} }
	 class Chi extends Par { private function pa() {} }
	 var_dump(method_exists("Chi", "sb"), method_exists("Chi", "pa"), method_exists("Chi", "pc"));`,
	`class Orig {} class_alias("Orig", "Alias");
	 $a = new Orig; $b = new Alias;
	 var_dump($a == $b, $a === $b, $a instanceof $b, $a instanceof Orig, $a instanceof Alias,
	          $b instanceof Orig, $b instanceof Alias);`,

	// The member reflectors, and the attribute lists they hand out.
	`#[Attribute] class Tag { function __construct(public int $n = 0) {} }
	 class Sub { #[Tag(1)] public int $p = 5; #[Tag(2)] const K = "k";
	             #[Tag(3)] public function m(int $x): string { return "m$x"; }
	             private function hidden() {} }
	 #[Tag(4)] function tagged(int $y = 1): ?int { return $y; }
	 $c = new ReflectionClass("Sub");
	 var_dump($c->getMethod("m")->getName(), $c->getMethod("m")->getAttributes()[0]->getArguments());
	 var_dump($c->getProperty("p")->getValue(new Sub), $c->getProperty("p")->getType()->getName(),
	          $c->getProperty("p")->getType()->allowsNull(), $c->getProperty("p")->isPublic());
	 var_dump($c->getReflectionConstant("K")->getValue(),
	          $c->getReflectionConstant("K")->getAttributes()[0]->getArguments());
	 $f = new ReflectionFunction("tagged");
	 var_dump($f->getName(), $f->getNumberOfParameters(), $f->getNumberOfRequiredParameters(),
	          $f->getReturnType()->getName(), $f->getReturnType()->allowsNull(),
	          $f->getReturnType()->isBuiltin(), $f->getAttributes()[0]->getArguments());
	 $m = new ReflectionMethod("Sub", "m");
	 var_dump($m->invoke(new Sub, 7), $m->isStatic(), $m->isPublic(),
	          $m->getDeclaringClass()->getName());
	 var_dump((new ReflectionMethod("Sub::hidden"))->isPrivate());
	 var_dump($c->newInstanceWithoutConstructor() instanceof Sub, $c->getParentClass());
	 try { new ReflectionMethod("Sub", "nope"); }
	 catch (ReflectionException $e) { echo $e->getMessage(), "\n"; }`,
	// A method is named with its class in an arity error, which the bare
	// op_array name does not say.
	`class Arity { function __construct(string $a) {} function m(int $b) {} }
	 function loose(int $c) {}
	 try { new Arity; } catch (ArgumentCountError $e) { echo $e->getMessage(), "\n"; }
	 try { (new Arity("x"))->m(); } catch (ArgumentCountError $e) { echo $e->getMessage(), "\n"; }
	 try { loose(); } catch (ArgumentCountError $e) { echo $e->getMessage(), "\n"; }`,
	// Attributes are not objects until something asks for one: the arguments
	// stay unevaluated, and `newInstance` validates the attribute class's own
	// `#[Attribute]` before constructing anything.
	`#[Attribute]
	 class Mark { public function __construct(public int $n = 0, public ?string $s = null) {} }
	 #[Attribute(Attribute::TARGET_CLASS)] class Narrow {}
	 #[Mark(1, s: "x")] #[Narrow] class Subject {}
	 $r = new ReflectionClass("Subject");
	 var_dump($r->getName(), $r->getShortName(), $r->isInterface(), $r->isEnum());
	 foreach ($r->getAttributes() as $a) { var_dump($a->getName(), $a->getArguments()); }
	 var_dump($r->getAttributes("Mark")[0]->newInstance());
	 try { new ReflectionClass("NoSuchThing"); }
	 catch (ReflectionException $e) { echo $e->getMessage(), "\n"; }
	 try { new Attribute("nope"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 // A bad flags value is only refused where the attribute is instantiated.
	 var_dump((new Attribute(-1))->flags);
	 #[Attribute(-1)] class Bad {}
	 #[Bad] class Marked {}
	 try { (new ReflectionClass("Marked"))->getAttributes()[0]->newInstance(); }
	 catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	// A generator declared `function &gen()` yields the slot, so a
	// by-reference foreach writes back through it.
	`function &refgen(array &$a) { foreach ($a as $k => &$v) { yield $k => $v; } }
	 $arr = [1, 2, 3];
	 foreach (refgen($arr) as $k => &$v) { $v *= -1; }
	 unset($v);
	 echo implode(",", $arr), "\n";
	 // Without the & a by-reference loop over a generator is refused.
	 function plaingen() { yield 1; }
	 try { foreach (plaingen() as &$w) {} }
	 catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`$probe = [
 'ucfirst' => fn() => [ucfirst("hello"), ucfirst(""), ucfirst("ÉaB"), ucfirst("1a")],
 'lcfirst' => fn() => [lcfirst("HELLO"), lcfirst("")],
 'ucwords' => fn() => [ucwords("hello big world"), ucwords("a-b c", "-"), ucwords("hello\tthere")],
 'nl2br' => fn() => [nl2br("a\nb"), nl2br("a\r\nb"), nl2br("a\rb")],
 'stripos' => fn() => [stripos("Hello","L"), stripos("Hello","z"), stripos("aAaA","a",2)],
 'strripos' => fn() => [strripos("Hello","L"), strripos("aAaA","A")],
 'strncmp' => fn() => [strncmp("abc","abd",2), strncmp("abc","abd",3), strncmp("a","b",1)],
 'strncasecmp' => fn() => [strncasecmp("ABC","abd",2), strncasecmp("ABC","abd",3)],
 'strnatcmp' => fn() => [strnatcmp("img12","img10"), strnatcmp("img2","img10"), strnatcmp("a","a"), strnatcmp("x01","x1")],
 'strnatcasecmp' => fn() => [strnatcasecmp("IMG12","img10")],
 'strstr' => fn() => [strstr("user@example.com","@"), strstr("user@example.com","@",true), strstr("abc","z")],
 'stristr' => fn() => [stristr("USER@x","@"), stristr("Hello","ELL")],
 'strrchr' => fn() => [strrchr("a/b/c","/"), strrchr("abc","z")],
 'strpbrk' => fn() => [strpbrk("This is a test","st"), strpbrk("abc","z")],
 'substr_count' => fn() => [substr_count("aaa","a"), substr_count("abab","ab")],
 'substr_replace' => fn() => [substr_replace("Hello","p",1,3), substr_replace("Hello","X",-2), substr_replace("Hello","X",1,-1), substr_replace("Hello","X",10)],
 'substr_compare' => fn() => [substr_compare("Hello","lo",3), substr_compare("Hello","LO",3,2,true), substr_compare("Hello","xy",0,2)],
 'str_split' => fn() => [str_split("abcde",2), str_split(""), str_split("ab")],
 'chunk_split' => fn() => [chunk_split("abcdef",2,"-"), chunk_split("",2,"-")],
 'wordwrap' => fn() => [wordwrap("The quick brown fox",10,"\n",true), wordwrap("A very long woooooooooooord.",8,"\n",true), wordwrap("short",10)],
 'strtr3' => fn() => [strtr("abc","ab","xy"), strtr("abc","abc","x")],
 'strtr2' => fn() => [strtr("hi all",["hi"=>"hello","all"=>"world"]), strtr("ab",["a"=>"1","ab"=>"2"])],
 'bin2hex' => fn() => [bin2hex("abc"), bin2hex("")],
 'hex2bin' => fn() => [hex2bin("616263")],
 'md5' => fn() => [md5(""), md5("abc")],
 'sha1' => fn() => [sha1("abc")],
 'crc32' => fn() => [crc32("abc"), crc32("")],
 'base64' => fn() => [base64_encode("abc"), base64_decode("YWJj")],
 'addslashes' => fn() => [addslashes("O'Rei\"lly\\x")],
 'stripslashes' => fn() => [stripslashes("O\\'Rei\\\"lly")],
 'quotemeta' => fn() => [quotemeta("1+1=2. (really)")],
 'preg_quote' => fn() => [preg_quote("Hello [a](b)"), preg_quote("a/b","/")],
 'htmlspecialchars' => fn() => [htmlspecialchars("<a href='x'>&</a>")],
 'htmlspecialchars_decode' => fn() => [htmlspecialchars_decode("&lt;a&gt;&amp;&quot;&#039;")],
 'strip_tags' => fn() => [strip_tags("<p>Hello <b>world</b></p>")],
 'levenshtein' => fn() => [levenshtein("kitten","sitting"), levenshtein("",""), levenshtein("a","")],
 'similar_text' => fn() => [similar_text("World","word"), similar_text("Hello","Hello")],
 'number_format' => fn() => [number_format(1234.5678), number_format(1234.5678,2), number_format(1234.5678,2,",","."), number_format(-0.4), number_format(-1234.5,1), number_format(0)],
 'str_word_count' => fn() => [str_word_count("Hello big world"), str_word_count("Hello big",1)],
];
foreach ($probe as $label => $fn) {
  echo "### $label\n";
  try { var_dump($fn()); } catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
}`,
	`$probe = [
 'trig' => fn() => [sin(0.5), cos(0.5), tan(0.5), atan2(1,2), hypot(3,4)],
 'exp_log' => fn() => [exp(1), log(M_E), log(8,2), log10(1000), sqrt(16), sqrt(2)],
 'degrees' => fn() => [deg2rad(180), rad2deg(M_PI)],
 'fmod_fdiv' => fn() => [fmod(10,3), fmod(-10,3), fdiv(1,0), fdiv(-1,0), fdiv(0,0)],
 'nan' => fn() => [is_nan(NAN), is_nan(1.0), is_infinite(INF), is_finite(1.0), is_finite(NAN)],
 'ceil_floor' => fn() => [ceil(4.1), floor(4.9), ceil(-4.1), floor(-4.9), ceil(5), floor(5)],
 'round' => fn() => [round(3.4), round(3.5), round(-3.5), round(1.955,2), round(1.45,1), round(5), round(1234,-2), round(2.5), round(-0.5)],
 'pow' => fn() => [pow(2,10), pow(2,-1), pow(2.0,3), pow(-2,3), pow(2,0.5), pow(10,20)],
 'pi' => fn() => [pi()],
 'is_scalar' => fn() => [is_scalar(1), is_scalar("a"), is_scalar(null), is_scalar([]), is_scalar(1.5), is_scalar(true)],
 
 'dirname' => fn() => [dirname("/a/b/c"), dirname("/a/b/c",2), dirname("a"), dirname("/"), dirname("a/b/")],
 'basename' => fn() => [basename("/a/b/c.txt"), basename("/a/b/c.txt",".txt"), basename("/a/b/"), basename("c.txt",".txt")],
 'pathinfo' => fn() => [pathinfo("/www/dir/index.html")],
 'array_flip' => fn() => [array_flip(["a"=>1,"b"=>2]), array_flip([1,2,3])],
 'array_reverse' => fn() => [array_reverse([1,2,3]), array_reverse([1,2,3],true), array_reverse(["a"=>1,2,"b"=>3])],
 'array_search' => fn() => [array_search(2,[1,2,3]), array_search("2",[1,2,3]), array_search("2",[1,2,3],true), array_search(9,[1,2])],
 'array_sum' => fn() => [array_sum([1,2,3]), array_sum([1.5,2]), array_sum([]), array_sum(["1","2"])],
 'array_product' => fn() => [array_product([2,3,4]), array_product([]), array_product([2,0.5])],
 'array_combine' => fn() => [array_combine(["a","b"],[1,2])],
 'array_pad' => fn() => [array_pad([1,2],4,0), array_pad([1,2],-4,0), array_pad([1,2,3],2,0)],
 'array_chunk' => fn() => [array_chunk([1,2,3,4,5],2), array_chunk(["a"=>1,"b"=>2,"c"=>3],2,true)],
 'array_count_values' => fn() => [array_count_values(["a","b","a",1,1])],
 'array_column' => fn() => [array_column([["id"=>1,"n"=>"x"],["id"=>2,"n"=>"y"]],"n"), array_column([["id"=>1,"n"=>"x"],["id"=>2,"n"=>"y"]],"n","id"), array_column([["id"=>3,"n"=>"z"]],null,"id")],
 'array_is_list' => fn() => [array_is_list([]), array_is_list([1,2]), array_is_list([1=>1])],
 'array_key_edges' => fn() => [array_key_first(["a"=>1,"b"=>2]), array_key_last(["a"=>1,"b"=>2]), array_key_first([]), array_key_last([])],
 'array_diff' => fn() => [array_diff([1,2,3,4],[2,4]), array_diff(["a"=>1,"b"=>2],[1]), array_diff([0,"a"],["0"])],
 'array_intersect' => fn() => [array_intersect([1,2,3],[2,3,4]), array_intersect([1,2,3],[2,3],[3])],
 'array_diff_key' => fn() => [array_diff_key(["a"=>1,"b"=>2],["a"=>9])],
 'array_intersect_key' => fn() => [array_intersect_key(["a"=>1,"b"=>2],["a"=>9,"c"=>1])],
 'array_diff_assoc' => fn() => [array_diff_assoc(["a"=>1,"b"=>2],["a"=>1,"b"=>9])],
 'array_intersect_assoc' => fn() => [array_intersect_assoc(["a"=>1,"b"=>2],["a"=>1,"b"=>9])],
 'array_merge_recursive' => fn() => [array_merge_recursive(["a"=>[1]],["a"=>[2]]), array_merge_recursive(["a"=>1],["a"=>2]), array_merge_recursive([1],[2])],
];
foreach ($probe as $label => $fn) {
  echo "### $label\n";
  try { var_dump($fn()); } catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
}`,
	// A by-reference foreach keeps a live position in the array rather than a
	// list of the keys it started with: an element appended inside the body is
	// reached, one removed is gone, and rebuilding the array moves the position
	// with the elements. A by-value loop iterates a copy and sees none of it.
	`function fePrint(string $label, callable $fn) { echo "### $label\n"; $fn(); }
	 fePrint("append and unset", function () {
	   $a = ["foo"];
	   foreach ($a as $k => &$v) {
	     var_dump($k);
	     unset($a[$k]); $a[] = "foo";
	     if ($k === 5) break;
	   }
	 });
	 fePrint("unshift during", function () {
	   $a = [1, 2, 3];
	   foreach ($a as &$v) { echo $v, "\n"; if ($v == 2) array_unshift($a, 0, 0, 0); }
	   var_dump(count($a));
	 });
	 fePrint("append during", function () {
	   $a = [1, 2];
	   foreach ($a as &$v) { echo $v, "\n"; if ($v == 1) $a[] = 3; }
	 });
	 fePrint("by value is a copy", function () {
	   $a = [1, 2];
	   foreach ($a as $v) { echo $v, "\n"; if ($v == 1) $a[] = 3; }
	   var_dump(count($a));
	 });
	 fePrint("nested loops", function () {
	   $a = [1, 2];
	   foreach ($a as &$x) { foreach ($a as &$y) { echo "$x$y "; } }
	   echo "\n";
	 });`,
	// array_splice moves a live position too, and not by a fixed shift: one
	// inside the run it removed lands on the first element to survive it,
	// wherever the replacement put that, and one past the run moves by the
	// difference between the two lengths.
	`function spliceIn(string $label, int $at, int $off, int $len, array $rep = []) {
	   echo "### $label\n";
	   $done = 0; $a = [0, 1, 2, 3, 4];
	   foreach ($a as &$v) {
	     echo $v, "\n";
	     if (!$done && $v == $at) { $done = 1; array_splice($a, $off, $len, $rep); }
	   }
	   var_dump($a);
	 }
	 spliceIn("remove before", 3, 1, 2);
	 spliceIn("remove after", 0, 2, 2);
	 spliceIn("remove current", 2, 1, 3);
	 spliceIn("replace before", 3, 1, 2, ["x", "y", "z"]);
	 spliceIn("replace after", 0, 2, 2, ["x", "y", "z"]);
	 spliceIn("replace current", 2, 1, 3, ["x", "y", "z"]);
	 spliceIn("insert only", 1, 2, 0, ["x"]);
	 spliceIn("insert in front", 0, 0, 0, ["x"]);
	 spliceIn("remove everything", 1, 0, 5);
	 function spliceKept(string $label, int $at, int $off, int $len) {
	   echo "### $label\n";
	   $done = 0; $a = [0, 1, 2, 3, 4];
	   foreach ($a as &$v) {
	     echo $v, "\n";
	     if (!$done && $v == $at) { $done = 1; $taken = array_splice($a, $off, $len); var_dump($taken); }
	   }
	 }
	 spliceKept("kept return, remove after", 0, 2, 2);
	 spliceKept("kept return, remove current", 2, 1, 3);`,
	// A named argument no parameter claimed survives into the stack frame: a
	// variadic callee keeps it under its name, which a trace renders as
	// `name: value` and debug_backtrace reports under a string key. A name that
	// *did* find a parameter is rendered positionally, since that is where it
	// landed. An empty name is a name like any other.
	`function namedTrace($a, ...$rest) {
	   foreach (debug_backtrace() as $frame) {
	     foreach ($frame["args"] as $key => $value) { echo "  ", $key, "=", $value, "\n"; }
	   }
	   debug_print_backtrace();
	   throw new Exception("named");
	 }
	 try { namedTrace(1, 2, x: 3, y: 4); } catch (Exception $e) {
	   var_dump($e->getTrace()[0]["args"]);
	   echo $e->getTraceAsString(), "\n";
	 }
	 function namedOnly(...$rest) { debug_print_backtrace(); }
	 call_user_func_array("namedOnly", ["" => 1]);
	 namedOnly(only: "x");
	 function namedPositional($a, $b) { debug_print_backtrace(); }
	 namedPositional(b: 2, a: 1);`,
	// Reaching a name the class declares static through an instance does not
	// redirect the access: the object grows a dynamic property of its own and
	// the static keeps its value. PHP only says that what was written is not
	// what the class declares -- and it says it for a read, a write, a
	// compound, an increment, an unset and a reference, but not for isset or
	// empty, which ask without accessing.
	`class StatA { public static $x = 1; }
	 class StatBase { private static $x = 1; }
	 class StatB extends StatBase { public $x = 7; }
	 class StatC { private static $y = 1; function poke() { $this->y = 5; return $this->y; } }
	 $a = new StatA;
	 echo "-- read\n";     var_dump(@$a->x);
	 echo "-- write\n";    $a->x = 5;
	 echo "-- compound\n"; $a->x .= "z";
	 echo "-- incdec\n";   $a->x++;
	 echo "-- isset\n";    var_dump(isset($a->x), empty($a->x));
	 echo "-- unset\n";    unset($a->x);
	 echo "-- ref\n";      $r = &$a->x;
	 echo "-- static\n";   var_dump(StatA::$x, $a->x);
	 echo "-- shadowed\n"; $b = new StatB; $b->x = 9; var_dump($b->x);
	 echo "-- inside\n";   $c = new StatC; var_dump($c->poke());
	 var_dump(property_exists($a, "x"));
	 // A class with the accessors says nothing: they answer the access, so
	 // nothing falls through to a property of the instance's own.
	 class StatMagic {
	   public static $m = 1;
	   public function __set($n, $v) { echo "set $n\n"; }
	   public function __get($n) { echo "get $n\n"; return 9; }
	   public function __isset($n) { echo "isset $n\n"; return true; }
	   public function __unset($n) { echo "unset $n\n"; }
	 }
	 $g = new StatMagic;
	 $g->m = 5; var_dump($g->m, isset($g->m)); unset($g->m); var_dump(StatMagic::$m);`,
	// A string offset that starts with an integer and then stops being one is
	// usable, and PHP says so rather than refusing. What counts as "starts with
	// an integer" is whatever is_numeric_string read: leading whitespace is
	// allowed, trailing whitespace is not trailing data, and a prefix that
	// turns out to be a float is no offset at all.
	`$str = "The world is fun";
	 foreach (["7", "7.5", "  7", "  7  ", "7  ", "7str", "7.5str", "  7str",
	           "  7  str", "7e", "7e2", "7.", "0xC", "0b10", "-1x", "999999999999999999999x",
	           "x", "", " "] as $k) {
	   try { var_dump($str[$k]); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 // The offset is checked before the descent is refused, so an offset PHP
	 // only disapproves of still earns its warning ahead of the fatal.
	 $held = "aaa";
	 try { $held["2bbb"][] = 1; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { $r = &$held["2ccc"]; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 var_dump(isset($str["7str"]), isset($str["7"]));`,
	// The internal array pointer walks an object's property table too, and PHP
	// says that is on the way out. get_parent_class() takes an object or the
	// name of a class that exists -- its parameter is declared wider than that,
	// so the refusal is worded by the function rather than by the signature.
	// The deprecation itself is silenced here: the engine names the active
	// internal function in it, and under eval there is none, so it says "PHP
	// Request Startup" where a real file says "reset()". The .phpt corpus
	// checks the wording; this checks that the pointer walks the table.
	`$obj = (object) ["a" => "b", "c" => "d"];
	 var_dump(@reset($obj), @current($obj), @key($obj), @next($obj), @end($obj),
	          @prev($obj), @pos($obj), @current($obj));
	 class ParentA {}
	 class ChildA extends ParentA { function inside() { var_dump(get_parent_class()); } }
	 interface IfaceA {}
	 (new ChildA)->inside();
	 foreach ([new ChildA, new ParentA, "ChildA", "ParentA", "IfaceA", "",
	           "NoSuchClassHere", [], 1, null, true, 1.5] as $candidate) {
	   try { var_dump(get_parent_class($candidate)); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// Writing *through* an ArrayAccess offset reaches a copy: offsetGet hands
	// back a value, so the modification has no effect and PHP says so. A
	// reference is the class saying otherwise, and an object is one already.
	// What an append passes the accessor is its own question: a PHP class is
	// called with null, an engine array store is not called at all.
	`class Overloaded implements ArrayAccess {
	   private $held = [];
	   public function offsetExists($o): bool { return isset($this->held[$o]); }
	   #[\ReturnTypeWillChange] public function offsetGet($o) { return $this->held[$o] ?? null; }
	   public function offsetSet($o, $v): void { if ($o === null) { $this->held[] = $v; } else { $this->held[$o] = $v; } }
	   public function offsetUnset($o): void { unset($this->held[$o]); }
	 }
	 $over = new Overloaded;
	 $over["x"] = [1, 2];
	 $over["obj"] = new stdClass;
	 echo "-- nested\n"; $over["x"][] = 3;
	 echo "-- ref\n";    $ref = &$over["x"];
	 echo "-- append\n"; $over[]["k"] = 1;
	 echo "-- incdec\n"; $over["n"]++;
	 echo "-- object\n"; $over["obj"]->p = 1; var_dump($over["obj"]->p);
	 echo "-- store\n";
	 $store = new ArrayObject([1]);
	 try { $store[][5] = "v"; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { $held = &$store[]; var_dump($held); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 var_dump($store->getArrayCopy());
	 echo "-- string\n";
	 $text = "abc";
	 try { $held2 = &$text[]; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { $text[] = "d"; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { $text["1x"][] = "d"; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// A closure made from something that already exists is a handle on it, and
	// PHP will not move a handle: call() names a scope, so it refuses every
	// such closure, while bindTo alone only refuses an object the method is
	// not on. A closure that was written as one rebinds freely.
	`class BindTarget {}
	 class BindHolder { function m() { return 1; } static function s() { return 2; } }
	 function bindFn($x) { return $x; }
	 $handles = [
	   "from function" => Closure::fromCallable("bindFn"),
	   "from internal" => Closure::fromCallable("strrev"),
	   "from method"   => Closure::fromCallable([new BindHolder, "m"]),
	   "written"       => function () { return 3; },
	   "static"        => static function () { return 4; },
	 ];
	 foreach ($handles as $label => $handle) {
	   echo "### ", $label, "\n";
	   $bound = $handle->bindTo(new BindTarget);
	   var_dump($bound === null);
	   var_dump($handle->bindTo(null) === null);
	   try { var_dump($handle->call(new BindTarget)); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 var_dump(Closure::fromCallable([new BindHolder, "m"])->bindTo(new BindHolder) === null);`,
	// ReflectionFunction::getClosure hands back the same kind of handle, and a
	// function's statics stay shared with every other way of calling it.
	`function reflStatic($k) { static $seen = []; $seen[] = $k; return count($seen); }
	 var_dump(reflStatic(1));
	 var_dump((new ReflectionFunction("reflStatic"))->getClosure()(2));
	 var_dump(reflStatic(3));
	 class ReflParams {
	   public function taking(callable $c, int &$n, ?string $s = null, ...$rest) {}
	 }
	 $method = (new ReflectionClass("ReflParams"))->getMethod("taking");
	 foreach ($method->getParameters() as $p) {
	   echo $p->getName(), " pos=", $p->getPosition(),
	        " type=", $p->hasType() ? $p->getType()->getName() : "-",
	        " byref=", var_export($p->isPassedByReference(), true),
	        " variadic=", var_export($p->isVariadic(), true),
	        " optional=", var_export($p->isOptional(), true),
	        " null=", var_export($p->allowsNull(), true),
	        " class=", $p->getDeclaringClass()->getName(), "\n";
	 }
	 var_dump((new ReflectionFunction("reflStatic"))->getParameters()[0]->getDeclaringClass());`,
	// `#[\SensitiveParameter]` keeps an argument out of every backtrace: the
	// call really passed the value, and it is the rendering that must not show
	// it. A variadic parameter marks every argument that reached it, since
	// they are all the one declaration. The wrapper still holds the value --
	// getValue() is how a program entitled to it asks -- and hides it from
	// everything that walks properties.
	`function secretTaker($a, #[\SensitiveParameter] $b, $c) {
	   foreach (debug_backtrace() as $frame) {
	     foreach ($frame["args"] as $arg) { var_dump($arg); }
	   }
	   debug_print_backtrace();
	   throw new Exception("thrown");
	 }
	 class SecretHolder { function m(#[\SensitiveParameter] $p) { debug_print_backtrace(); } }
	 function secretRest($a, #[\SensitiveParameter] ...$rest) { debug_print_backtrace(); }
	 try { secretTaker(1, "hunter2", 3); } catch (Exception $e) { echo $e->getTraceAsString(), "\n"; }
	 (new SecretHolder)->m("secret");
	 secretRest(1, 2, 3);
	 $wrapped = new SensitiveParameterValue("held");
	 var_dump($wrapped->getValue(), $wrapped, (array) $wrapped, json_encode($wrapped));
	 var_export($wrapped); echo "\n";
	 print_r($wrapped); echo "\n";
	 // The attribute is readable through reflection, on the parameter and --
	 // for a promoted one -- on the property it also became.
	 $taking = (new ReflectionFunction("secretTaker"))->getParameters();
	 foreach ($taking as $p) {
	   echo $p->getName(), ": ", count($p->getAttributes()), "\n";
	 }
	 class Promoted { public function __construct(#[\SensitiveParameter] public int $n = 1) {} }
	 var_dump((new ReflectionParameter(["Promoted", "__construct"], "n"))->getAttributes()[0]->getName());
	 var_dump((new ReflectionProperty("Promoted", "n"))->getAttributes()[0]->getName());
	 var_dump((new ReflectionParameter(["Promoted", "__construct"], 0))->getName());
	 try { new ReflectionParameter(["Promoted", "__construct"], "nope"); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// A `never` function must not reach its end -- the compiler puts an opcode
	// where control would fall off, so arriving there is itself the error, and
	// the message names what the declaration was rather than what the check is.
	`function neverFn(): never { if (false) { throw new Exception("no"); } }
	 class NeverHolder { public function m(): never { if (false) { throw new Exception("no"); } } }
	 try { neverFn(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { (new NeverHolder)->m(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 function neverThrows(): never { throw new Exception("thrown"); }
	 try { neverThrows(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 // call_user_func_array's second argument is checked by the opcode the
	 // compiler folds the call into, in that call's own words.
	 foreach ([null, 1, "x", new stdClass] as $bad) {
	   try { call_user_func_array("strlen", $bad); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 // #[\Deprecated] is an attribute, but an attribute is an ordinary object:
	 // a program may build one, and its two properties are readonly.
	 $marker = new \Deprecated("gone", "1.0");
	 var_dump($marker->message, $marker->since);
	 var_dump((new \Deprecated())->message);
	 try { $marker->message = "other"; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { new \Deprecated([]); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { new \Deprecated("a", []); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// CallbackFilterIterator wraps another Iterator and drops what the callback
	// turns down. Every question goes to the inner iterator, and the two that
	// move keep moving until accept() agrees -- expressed through accept() so a
	// subclass that overrides it and ignores the callback still filters.
	`$source = new ArrayIterator([1, 2, 3, 4, 5]);
	 $even = new CallbackFilterIterator($source, fn($v) => $v % 2 === 0);
	 foreach ($even as $k => $v) { echo $k, "=", $v, "\n"; }
	 foreach ($even as $k => $v) { echo "again ", $k, "=", $v, "\n"; }
	 var_dump($even->getInnerIterator() === $source);
	 $none = new CallbackFilterIterator(new ArrayIterator([1, 3]), fn($v) => false);
	 foreach ($none as $v) { echo "unreachable\n"; }
	 var_dump(iterator_to_array($even));
	 // The callback sees the key and the iterator as well as the element.
	 $keys = new CallbackFilterIterator(new ArrayIterator(["a" => 1, "b" => 2]),
	   function ($v, $k, $it) { return $k === "b"; });
	 foreach ($keys as $k => $v) { echo $k, "->", $v, "\n"; }
	 // Nested filters ask each other the same questions.
	 $twice = new CallbackFilterIterator(
	   new CallbackFilterIterator(new ArrayIterator([1, 2, 3, 4, 5, 6]), fn($v) => $v % 2 === 0),
	   fn($v) => $v > 2);
	 foreach ($twice as $v) { echo "twice ", $v, "\n"; }
	 class AcceptsOdd extends CallbackFilterIterator {
	   public function accept(): bool { return $this->getInnerIterator()->current() % 2 === 1; }
	 }
	 foreach (new AcceptsOdd(new ArrayIterator([1, 2, 3]), fn($v) => false) as $v) { echo "odd ", $v, "\n"; }
	 try { new CallbackFilterIterator([1], fn() => true); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { new CallbackFilterIterator($source, "no_such_filter_fn"); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// IteratorIterator is the same wrapper with nothing filtering: it takes any
	// Traversable, unwraps an aggregate once at construction, and passes through
	// to the inner iterator anything it does not answer itself -- which is why
	// method_exists() on one comes out true for the inner class's methods.
	`$inner = new ArrayIterator(["a" => 1, "b" => 2]);
	 $plain = new IteratorIterator($inner);
	 foreach ($plain as $k => $v) { echo $k, "=", $v, "\n"; }
	 var_dump($plain->getInnerIterator() === $inner);
	 var_dump(method_exists($plain, "offsetGet"), method_exists($plain, "nosuch"));
	 var_dump($plain->offsetGet("b"), $plain->count());
	 function two_of_them() { yield 10; yield 20; }
	 $overGenerator = new IteratorIterator(two_of_them());
	 var_dump(iterator_to_array($overGenerator));
	 class Bag implements IteratorAggregate {
	   public function getIterator(): Iterator { echo "asked\n"; return new ArrayIterator([7, 8]); }
	 }
	 $overAggregate = new IteratorIterator(new Bag());
	 var_dump($overAggregate->getInnerIterator() === $overAggregate->getInnerIterator());
	 foreach ($overAggregate as $v) { echo "bag ", $v, "\n"; }
	 class Loud extends IteratorIterator {
	   public function rewind(): void { echo "rewound\n"; parent::rewind(); }
	 }
	 foreach (new Loud(new ArrayIterator([1])) as $v) { echo "loud ", $v, "\n"; }
	 try { new IteratorIterator([1]); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// EmptyIterator iterates nothing and holds nothing: it is never valid, and
	// asking what it is on is a BadMethodCallException rather than null.
	`$nothing = new EmptyIterator();
	 var_dump($nothing instanceof Iterator, $nothing->valid());
	 foreach ($nothing as $v) { echo "unreachable\n"; }
	 var_dump(iterator_to_array($nothing));
	 $nothing->rewind();
	 $nothing->next();
	 try { $nothing->current(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { $nothing->key(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// An Iterator method the engine drives from a foreach is a call like any
	// other, so it is a frame of its own in a backtrace, attributed to where the
	// loop was -- and the questions asked before it do not move that.
	`class Thrower implements Iterator {
	   public function current(): mixed { return 1; }
	   public function key(): mixed { throw new Exception("from key"); }
	   public function next(): void {}
	   public function rewind(): void {}
	   public function valid(): bool { return true; }
	 }
	 try { foreach (new Thrower() as $k => $v) { } }
	 catch (Throwable $t) { echo $t->getMessage(), "\n", $t->getTraceAsString(), "\n"; }`,
	// The other methods the engine reaches for on the program's behalf are
	// frames too, and each is named for the class that *declared* the body
	// rather than the object's -- an inherited __toString reports the parent.
	`class Base {
	   public function __toString(): string { throw new Exception("stringified"); }
	 }
	 class Derived extends Base {}
	 // The frames are printed without their file, which the engine names
	 // differently from the harness's temporary copy, and reached without a
	 // closure, whose own name carries one.
	 function report(Throwable $t) {
	   echo $t->getMessage(), "\n";
	   foreach ($t->getTrace() as $i => $frame) {
	     echo "#", $i, " ", $frame["class"] ?? "", $frame["type"] ?? "",
	       $frame["function"], "(", implode(", ", array_map(
	         fn($a) => is_object($a) ? get_class($a) : var_export($a, true),
	         $frame["args"] ?? [])), ")\n";
	   }
	 }
	 try { echo (string) new Derived(); } catch (Throwable $t) { report($t); }
	 class Offsets implements ArrayAccess {
	   public function offsetExists($o): bool { return true; }
	   public function offsetGet($o): mixed { throw new Exception("fetched"); }
	   public function offsetSet($o, $v): void { throw new Exception("stored"); }
	   public function offsetUnset($o): void {}
	 }
	 $offsets = new Offsets();
	 try { echo $offsets[7]; } catch (Throwable $t) { report($t); }
	 try { $offsets["k"] = 1; } catch (Throwable $t) { report($t); }
	 class Counts implements Countable {
	   public function count(): int { throw new Exception("counted"); }
	 }
	 try { count(new Counts()); } catch (Throwable $t) { report($t); }`,
	// `...` inside a constant expression is evaluated where the constant is,
	// not by the array-building opcodes: string keys are kept, integer keys are
	// renumbered, and there is nothing to iterate, so only an array will do.
	`class Spread {
	   public const PARTS = [1, 2, 3];
	   public const WHOLE = [0, ...self::PARTS, 4];
	   public const NAMED = ["a" => 1, ...["b" => 2], ...[9]];
	   public static $copied = [...self::PARTS];
	 }
	 var_dump(Spread::WHOLE, Spread::NAMED, Spread::$copied);
	 // Declared before what it refers to, so the compiler cannot fold it and
	 // the refusal happens where the constant is read.
	 class Late {
	   public const BAD = [...self::NOT_AN_ARRAY];
	   public const NOT_AN_ARRAY = 5;
	 }
	 try { var_dump(Late::BAD); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 const LOOSE = [...[1, 2], ...["k" => 3]];
	 var_dump(LOOSE);
	 function defaulted($x = [...LOOSE]) { return $x; }
	 var_dump(defaulted());
	 // Unpacking past the largest integer key has nowhere to put the element,
	 // in a constant expression and at runtime alike.
	 $runtime = [1, 2, 3];
	 try { var_dump([PHP_INT_MAX - 1 => 0, ...$runtime]); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 function full($x = [PHP_INT_MAX - 1 => 0, ...LOOSE]) {}
	 try { full(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// A constant expression that reaches a constant already being evaluated has
	// no bottom. Where the engine notices it is not where a reader would guess:
	// the fetch the *program* makes does not count as visiting, so the cycle is
	// caught one lap later, and the message names the fetch as it is written.
	`class Pair { public const A = self::B; public const B = self::A; }
	 try { var_dump(Pair::A); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 try { var_dump(Pair::B); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 class Ring { public const A = self::B; public const B = self::C; public const C = self::A; }
	 try { var_dump(Ring::A); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 class Tail { public const A = self::B; public const B = self::C; public const C = self::B; }
	 try { var_dump(Tail::A); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 class Off { public const A = [...self::C]; public const C = [...self::B]; public const B = [...Off::C]; }
	 try { var_dump(Off::A); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 class Itself { public const M = self::M; }
	 try { var_dump(Itself::M); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 class Across { public const N = Back::V; }
	 class Back { public const V = Across::N; }
	 try { var_dump(Across::N); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 // A constant named twice in one expression is not a cycle.
	 class Twice { public const ONE = 1; public const BOTH = [self::ONE, self::ONE]; }
	 var_dump(Twice::BOTH);`,
	// An initializer that is a single bare constant fetch is evaluated without a
	// frame of its own, while the same fetch with anything around it gets one.
	// The distinction is the engine's and holds for class constants and property
	// defaults alike -- it is not about which kind of declaration it is.
	`class BareConst { const C = BARE_MISSING_1; }
	 class WrappedConst { const C = BARE_MISSING_2 + 0; }
	 class BareStatic { public static $s = BARE_MISSING_3; }
	 class WrappedStatic { public static $s = [BARE_MISSING_4 => 1]; }
	 class BareProp { public $p = BARE_MISSING_5; }
	 class WrappedProp { public $p = [BARE_MISSING_6 => 1]; }
	 function frames(Throwable $t) {
	   echo $t->getMessage(), "\n";
	   foreach ($t->getTrace() as $i => $frame) { echo "  #", $i, " ", $frame["function"], "\n"; }
	 }
	 function bareConst() { BareConst::C; }
	 function wrappedConst() { WrappedConst::C; }
	 function bareProp() { new BareProp(); }
	 function wrappedProp() { new WrappedProp(); }
	 function wrappedStatic() { new WrappedStatic(); }
	 foreach (["bareConst", "wrappedConst", "bareProp", "wrappedProp", "wrappedStatic"] as $which) {
	   try { $which(); } catch (Throwable $t) { frames($t); }
	 }`,
	// Reaching a static property uses the class, which resolves every deferred
	// initializer it has -- not only the one being read. Probed: reading one
	// static property reports what the class's constants and *other* property
	// defaults report, constants first, and writing or asking isset() does the
	// same. The line each is reported on is the engine's own and differs from
	// php2go's, so only the messages are compared here.
	`set_error_handler(function ($n, $s) { echo "! ", $s, "\n"; return true; });
	 class Deferred {
	   public const K = [1][6];
	   public static $a = [1][5];
	   public static $b = 1;
	 }
	 echo "before\n";
	 var_dump(Deferred::$b);
	 echo "mid\n";
	 var_dump(Deferred::$a);
	 class Written { public static $a = [1][7]; }
	 echo "written\n";
	 Written::$a = 5;
	 var_dump(Written::$a);
	 class Asked { public static $a = [1][8]; }
	 echo "asked\n";
	 var_dump(isset(Asked::$a));`,
	// A constant expression is evaluated with the reported position moved to
	// where the initializer was *written*, and a class-constant reference inside
	// one moves it again to its own line -- so a multi-line initializer names the
	// line of the reference that failed, not the line the initializer starts on.
	// The class name in the diagnostic is the one written, not the one it
	// resolves to.
	`class Multi {
	   public const OK = 1;
	   public const BAD =
	     self::OK
	     + self::MISSING;
	 }
	 try { var_dump(Multi::BAD); }
	 catch (Throwable $t) { echo $t->getMessage(), " @", $t->getLine(), "\n"; }
	 class Named { public const BAD = Named::ALSO_MISSING; }
	 try { var_dump(Named::BAD); }
	 catch (Throwable $t) { echo $t->getMessage(), " @", $t->getLine(), "\n"; }
	 class Missing {
	   public const BAD =
	     1
	     + NoSuchClass::WHATEVER;
	 }
	 try { var_dump(Missing::BAD); }
	 catch (Throwable $t) { echo $t->getMessage(), " @", $t->getLine(), "\n"; }`,
	// Subscripting inside a constant expression. An object is refused outright,
	// before the index is even evaluated, because there is no way to call its
	// offsetGet from here -- and an object *as* the offset is the same TypeError
	// a runtime subscript earns, whether it is being read or used as a key in an
	// array literal.
	`enum Marker { case One; }
	 enum Indexed: int implements ArrayAccess {
	   case Only = 1;
	   public function offsetGet($k): mixed { return 42; }
	   public function offsetExists($k): bool { return true; }
	   public function offsetSet($k, $v): void {}
	   public function offsetUnset($k): void {}
	 }
	 function shown(callable $read) {
	   try { var_dump($read()); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 class OnObject { const X = Indexed::Only[0]; }
	 class OnObjectBadIndex { const X = Indexed::Only[NO_SUCH_CONSTANT]; }
	 class ObjectKey { const X = [Marker::One => 3]; }
	 class ObjectKeyLater { const X = [Marker::One => 1, 2]; }
	 class ObjectOffsetList { const X = [1, 2][Marker::One]; }
	 class ObjectOffsetMap { const X = ["a" => 1][Marker::One]; }
	 class ObjectOffsetString { const X = "abc"[Marker::One]; }
	 class StringOffset { const X = "abc"[1]; }
	 class MissingKey { const X = [1][5]; }
	 foreach (["OnObject", "OnObjectBadIndex", "ObjectKey", "ObjectKeyLater",
	           "ObjectOffsetList", "ObjectOffsetMap", "ObjectOffsetString",
	           "StringOffset", "MissingKey"] as $which) {
	   echo "-- ", $which, "\n";
	   shown(fn() => constant($which . "::X"));
	 }`,
	// An operation the language refuses is refused inside a constant expression
	// too, in the same words -- and the same check is what reports a non-numeric
	// string operand, which this path used to skip silently.
	`set_error_handler(function ($n, $s) { echo "! ", $s, "\n"; return true; });
	 class Refused { const X = [1, 2] - [0]; }
	 try { var_dump(Refused::X); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 class Negated { const X = -"abc"; }
	 try { var_dump(Negated::X); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 class Loose { const X = "5 apples" + 1; }
	 var_dump(Loose::X);
	 class Fine { const X = -3, Y = +3.5; }
	 var_dump(Fine::X, Fine::Y);`,
	// `?->` inside a constant expression short-circuits the rest of the chain,
	// not just its own step: a property, a subscript and the expression naming a
	// property are all skipped. The chain ends at the enclosing operator, so the
	// other side of a `+` still runs.
	`class Loud { public function __construct() { echo "built\n"; } }
	 const NS_A = (null)?->test;
	 var_dump(NS_A);
	 const NS_B = (null)?->test->test;
	 var_dump(NS_B);
	 const NS_C = (null)->test?->test;
	 var_dump(NS_C);
	 const NS_D = (null)?->test["test"];
	 var_dump(NS_D);
	 const NS_E = (null)["test"]?->test;
	 var_dump(NS_E);
	 const NS_F = (null)?->{new Loud};
	 var_dump(NS_F);
	 const NS_G = (null)?->test + (new Loud ? 1 : 0);
	 var_dump(NS_G);`,
	// A class constant's declared type is checked without coercion -- unlike a
	// parameter or a property, nothing converts, not even an object that can
	// render itself, whose __toString is not called at all. The one exception is
	// the widening the language always allows, int to float. `static` names the
	// class the constant was declared in.
	`class Renders { public function __toString(): string { echo "rendered\n"; return "S"; } }
	 define("AN_OBJECT", new Renders());
	 define("A_ROUND_FLOAT", 1.0);
	 define("A_NUMERIC_STRING", "5");
	 define("A_FRACTION", 1.5);
	 enum Other { case Case1; }
	 enum Own { case Case1; const static SELF_TYPED = Own::Case1; }
	 enum Foreign { const static SELF_TYPED = Other::Case1; }
	 class Widened { const float F = 1; }
	 class Refuses {
	   const string FROM_OBJECT = AN_OBJECT;
	   const int FROM_ROUND_FLOAT = A_ROUND_FLOAT;
	   const int FROM_NUMERIC_STRING = A_NUMERIC_STRING;
	   const int|string FROM_FRACTION = A_FRACTION;
	 }
	 foreach (["Widened::F", "Own::SELF_TYPED", "Foreign::SELF_TYPED",
	           "Refuses::FROM_OBJECT", "Refuses::FROM_ROUND_FLOAT",
	           "Refuses::FROM_NUMERIC_STRING", "Refuses::FROM_FRACTION"] as $which) {
	   echo "-- ", $which, "\n";
	   try { var_dump(constant($which)); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A parameter default the compiler could not fold has no value until the
	// call, so it is checked at the call like any argument -- in the caller's
	// mode, and reported as an argument rather than as whatever the body then
	// does with it. A default it *could* fold was accepted at compile time and
	// is not re-checked.
	`const A_STRING = "text";
	 const A_NULL = null;
	 function wants_int(int $a = A_STRING): int { echo "body ran\n"; return 1; }
	 function wants_nullable(?int $a = A_NULL): ?int { return $a; }
	 function wants_int_null(int $a = A_NULL): int { return 1; }
	 function wants_string(string $a = A_STRING): string { return $a; }
	 foreach (["wants_int", "wants_nullable", "wants_int_null", "wants_string"] as $which) {
	   echo "-- ", $which, "\n";
	   try { var_dump($which()); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A redeclared class constant's type is covariant: it may narrow the one it
	// overrides and may add one where there was none, but may not widen it or
	// drop it -- and an unresolved class name makes the types incompatible
	// rather than the question undecidable, which is not how a method's variance
	// check behaves.
	`class Wide {} class Narrow extends Wide {}
	 class HasWide { const ?Wide C = null; }
	 class NarrowsIt extends HasWide { const ?Narrow C = null; }
	 class Untyped { const C = 1; }
	 class AddsType extends Untyped { const int C = 1; }
	 class Anything { const mixed C = 1; }
	 class PinsIt extends Anything { const int C = 1; }
	 var_dump(NarrowsIt::C, AddsType::C, PinsIt::C);`,
	// Subscripting something that is not a container. isset() and empty() are
	// quiet about a missing array key but not about an object that cannot be
	// subscripted at all -- they refuse, in the same words a read does. A
	// resource is no more a container than a scalar is and earns the same
	// warning. And descending through offsetGet to unset something inside what
	// it returned reaches a copy, which the engine says before refusing.
	`class Offsets implements ArrayAccess {
	   public function offsetSet($o, $v): void { echo "set\n"; }
	   public function offsetGet($o): mixed { echo "get\n"; return 5; }
	   public function offsetUnset($o): void { echo "unset\n"; }
	   public function offsetExists($o): bool { echo "exists\n"; return true; }
	 }
	 function step(string $label, callable $f) {
	   echo "-- ", $label, "\n";
	   try { $f(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 $plain = new stdClass();
	 step("isset on plain object", function () use ($plain) { var_dump(isset($plain[4])); });
	 step("empty on plain object", function () use ($plain) { var_dump(empty($plain[4])); });
	 step("read on plain object", function () use ($plain) { var_dump($plain[4]); });
	 $overloaded = new Offsets();
	 step("isset on ArrayAccess", function () use ($overloaded) { var_dump(isset($overloaded[4])); });
	 step("nested unset on ArrayAccess", function () use ($overloaded) { unset($overloaded[4][0]); });
	 step("nested read on ArrayAccess", function () use ($overloaded) { var_dump($overloaded[4][0]); });`,
	// Neither ArrayIterator nor ArrayObject is final, so a subclass gets the
	// same store the parent has -- which is what makes the storage the parent
	// declares show up in a var_dump of the subclass. A method the subclass
	// *writes* still wins over the store: an inherited offsetSet reaches the
	// store, an overridden one does not. And extending one does not earn the
	// Serializable notice, because the engine class supplies the replacement
	// pair even though it has no body the harvest can see.
	`class Quiet extends ArrayObject {}
	 $quiet = new Quiet();
	 $quiet[] = "value";
	 var_dump($quiet, count($quiet), $quiet[0]);
	 class Talkative extends ArrayObject {
	   public function offsetSet($offset, $value): void { echo "kept out\n"; }
	 }
	 $talkative = new Talkative();
	 $talkative[] = "value";
	 var_dump($talkative);
	 class Walked extends ArrayIterator {}
	 foreach (new Walked(["a" => 1, "b" => 2]) as $k => $v) { echo $k, "=", $v, "\n"; }`,
	// A foreach over an Iterator or a generator hands the key through untouched:
	// null, a float, a bool, an array all arrive as themselves, and nothing
	// reports a conversion. Only a caller *building an array* converts, which is
	// where the conversion's own diagnostics belong -- iterator_to_array reports
	// the float, the loop that produced it does not.
	`class Keys implements Iterator {
	   private $i = 0;
	   private $keys;
	   public function __construct(array $keys) { $this->keys = $keys; }
	   public function current(): mixed { return $this->i; }
	   public function key(): mixed { return $this->keys[$this->i]; }
	   public function next(): void { $this->i++; }
	   public function rewind(): void { $this->i = 0; }
	   public function valid(): bool { return $this->i < count($this->keys); }
	 }
	 foreach (new Keys([null, 1.5, true, false, "s", 3, [1, 2]]) as $k => $v) { var_dump($k); }
	 function keyed() { yield null => 1; yield 1.5 => 2; yield true => 3; yield [1] => 4; }
	 foreach (keyed() as $k => $v) { var_dump($k); }
	 set_error_handler(function ($n, $s) { echo "! ", $s, "\n"; return true; });
	 var_dump(iterator_to_array(new Keys([null, 1.5]), true));
	 restore_error_handler();
	 // A key that is a reference to a variable that does not exist is null, not
	 // the empty string an array key would have made of it.
	 class ByRefKey extends ArrayIterator {
	   #[ReturnTypeWillChange]
	   public function &key() { return $missing; }
	 }
	 foreach (new ByRefKey([0]) as $k => $v) { var_dump($k); }`,
	// A WeakMap is keyed by objects, which no array key can hold. Iterating one
	// hands the object back as the key.
	`$map = new WeakMap();
	 $first = new stdClass();
	 $second = new stdClass();
	 $map[$first] = 1;
	 $map[$second] = "two";
	 var_dump(count($map), $map instanceof IteratorAggregate);
	 foreach ($map as $key => $value) { var_dump($key === $first || $key === $second, $value); }
	 var_dump(iterator_to_array($map, false));
	 // A by-reference loop over one binds its values, and the box stays -- an
	 // array element's flattens once the loop variable moves on, a WeakMap's
	 // does not, so var_dump keeps the marker on every entry it visited.
	 foreach ($map as $key => &$each) { $each = "written"; }
	 unset($each);
	 var_dump($map[$first], $map[$second]);
	 var_dump($map);`,
	// Both handler kinds are stacks, and installing null pushes onto them rather
	// than clearing them: restoring afterwards brings back what was underneath.
	// While the null is on top the default handling applies.
	`$first = function () { echo "first\n"; return true; };
	 $second = function () { echo "second\n"; return true; };
	 var_dump(set_error_handler($first) === null);
	 var_dump(set_error_handler($second) === $first);
	 var_dump(set_error_handler(null) === $second);
	 var_dump(restore_error_handler());
	 $undefined1++;
	 var_dump(restore_error_handler());
	 $undefined2++;
	 var_dump(restore_error_handler());
	 var_dump(restore_error_handler());
	 $undefined3++;`,
	// UnhandledMatchError names the subject the way the language writes a
	// literal, not the way a string cast would: a bool is named, a whole float
	// keeps its `.0`, and an array is named by its type the way an object is.
	`function unmatched($subject) {
	   try { match ($subject) { "never reached" => 1 }; }
	   catch (\UnhandledMatchError $t) { echo $t->getMessage(), "\n"; }
	 }
	 foreach ([true, false, null, 1, 1.5, 1.0, "s", [1, 2], new stdClass()] as $subject) {
	   unmatched($subject);
	 }`,
	// Reaching an instance method through a class name resolves as a callable
	// but cannot run: there is no instance to call it on. The one in scope will
	// do when it is one of that class, which is what makes the same callable
	// work from inside a method of it.
	`class Instance {
	   public function shout() { return "shouted"; }
	   public function viaString() { $c = "Instance::shout"; return $c(); }
	   public function viaArray() { $c = ["Instance", "shout"]; return $c(); }
	   public static function statically() { return "static"; }
	 }
	 // Deliberately untyped: a callable type declaration refuses one of these
	 // outright, which is a separate rule from what the call itself does.
	 function attempted($c) {
	   try { var_dump($c()); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 $fromString = "Instance::shout";
	 $fromArray = ["Instance", "shout"];
	 attempted($fromString);
	 attempted($fromArray);
	 attempted("Instance::statically");
	 var_dump(is_callable($fromString), is_callable($fromArray));
	 // Inside a method of the class is_callable() answers true, and a direct
	 // call is refused all the same -- but call_user_func lends its caller's
	 // instance, so the same callable runs there.
	 $inside = new Instance();
	 try { $inside->viaString(); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 try { $inside->viaArray(); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 class Lent {
	   public function inherited() { return "inherited this=" . get_class($this); }
	 }
	 class Borrows extends Lent {
	   public function direct() { $c = ["Lent", "inherited"]; return $c(); }
	   public function lent() { return call_user_func(["Lent", "inherited"]); }
	   public function lentArray() { return call_user_func_array(["Lent", "inherited"], []); }
	   public function lentString() { return call_user_func("Lent::inherited"); }
	 }
	 $borrows = new Borrows();
	 foreach (["direct", "lent", "lentArray", "lentString"] as $which) {
	   echo "-- ", $which, "\n";
	   try { var_dump($borrows->$which()); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 // Rendering an argument TypeError completes the "called in" clause with the
	 // site the function was defined at. getMessage() never shows it.
	 function typed(int $only) {}
	 try { typed("text"); }
	 catch (TypeError $t) { echo $t->getMessage(), "\n--\n", (string) $t, "\n"; }
	 class OwnTypeError extends TypeError {}
	 $subclassed = new OwnTypeError("f(): Argument #1 ($a) must be of type int, string given, called in x on line 1");
	 echo (string) $subclassed, "\n";`,
	// An accessor answers for a property the caller may not reach -- but only
	// while it can be entered. Inside __set already, writing the same name
	// reaches the declared property and is refused: the engine does not grow a
	// dynamic property alongside the one it just refused. A *protected* one
	// declared by an ancestor is reachable from that ancestor's body, so the
	// same write there succeeds.
	`class Accessors {
	   public function __set($name, $value) { $this->$name = $value; }
	   public function __get($name) { return $this->$name; }
	 }
	 class Holds extends Accessors { private $hidden; protected $shared; }
	 $holds = new Holds();
	 try { $holds->hidden = "written"; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { $holds->shared = "written"; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { var_dump($holds->hidden); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 var_dump($holds);`,
	// A refused *static* property names the class the reference resolves to,
	// not the one that declared it: self:: and static:: inside the subclass
	// report the subclass, parent:: and an explicit name report the ancestor.
	`class Owner { private static $own = 1; protected static $shared = 2; }
	 class Below extends Owner {
	   public function viaSelf() { return self::$own; }
	   public function viaParent() { return parent::$own; }
	   public function viaName() { return Owner::$own; }
	   public function viaStatic() { return static::$own; }
	   public function written() { self::$own = 5; }
	   public function asked() { return isset(self::$own); }
	 }
	 class Elsewhere {
	   public function viaName() { return Owner::$own; }
	   public function protectedOne() { return Owner::$shared; }
	 }
	 foreach ([[new Below(), "viaSelf"], [new Below(), "viaParent"], [new Below(), "viaName"],
	           [new Below(), "viaStatic"], [new Below(), "written"], [new Below(), "asked"],
	           [new Elsewhere(), "viaName"], [new Elsewhere(), "protectedOne"]] as $pair) {
	   echo $pair[1], " on ", get_class($pair[0]), " -> ";
	   try { var_dump($pair[0]->{$pair[1]}()); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A constant expression comparing an object with a string renders it through
	// __toString, and the declared return type is enforced there as anywhere
	// else -- so a default value can raise before the body it belongs to runs.
	`class Silent { public function __toString() {} }
	 function defaulted($y = new Silent() > "") { echo "body ran\n"; }
	 try { defaulted(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 function alsoDefaulted($y = new Silent() < "") { echo "body ran\n"; }
	 try { alsoDefaulted(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 function equal($y = new Silent() == "") { echo "body ran\n"; }
	 try { equal(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// Unpacking a Traversable takes its keys as they are, and only an int or a
	// string is a key: nothing is converted into shape, so a generator yielding
	// null or a float is refused rather than quietly landing on "" or 1.
	`function keyed($k) { yield $k => 1; }
	 function unpacked(...$args) { return count($args); }
	 foreach ([null, 1.5, true, [1], new stdClass(), "s", 3] as $key) {
	   echo "-- ", gettype($key), "\n";
	   try { var_dump([...keyed($key)]); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	   try { var_dump(unpacked(...keyed($key))); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A class reference has to be a string or an object; anything else is
	// refused before the name is looked up and before the rest of the call site
	// is even read.
	`function named($reference) {
	   try { $reference::whatever(); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 foreach ([null, 1, 1.5, true, [1], "NoSuchClass"] as $reference) { named($reference); }
	 try { $missing::$alsoMissing(); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 // A constant array literal has the same next-key limit a runtime one has.
	 function overflowing($x = [PHP_INT_MAX => 42, "appended"]) {}
	 try { overflowing(); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// Abandoning a generator part-way runs its finally blocks, and one that
	// throws replaces whatever abandoned it -- so an unusable key reports the
	// finally's exception rather than the key error that stopped the unpack.
	`function refusedThenThrows() {
	   try { yield null => 0; } finally { throw new Exception("from finally"); }
	 }
	 try { var_dump([...refusedThenThrows()]); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 function refusedQuietly() {
	   try { yield null => 0; } finally { echo "cleaned up\n"; }
	 }
	 try { var_dump([...refusedQuietly()]); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// Returning by reference: only something with a slot behind it goes back as
	// one. A variable, an array element and a property do; a literal, an
	// expression, an assignment's result and a by-*value* call's result do not,
	// and each earns the notice. A by-reference call's result does have a slot,
	// and passes through quietly.
	`$shared = 1;
	 function &plainVariable() { global $shared; return $shared; }
	 function &assignmentResult() { global $shared; return $shared = 2; }
	 function &byReferenceCall() { return plainVariable(); }
	 function &literalValue() { return 42; }
	 function &computed() { global $shared; return $shared + 1; }
	 function &element() { global $list; return $list[0]; }
	 function &member() { global $held; return $held->p; }
	 function byValue() { return 5; }
	 function &byValueCall() { return byValue(); }
	 $list = [1];
	 $held = new stdClass();
	 $held->p = 3;
	 foreach (["plainVariable", "assignmentResult", "byReferenceCall", "literalValue",
	           "computed", "element", "member", "byValueCall"] as $which) {
	   echo "-- ", $which, "\n";
	   $got = $which();
	   var_dump($got);
	 }`,
	// A parameter that wants a slot cannot be given a value. The refusal is at
	// the send, before the body runs, and names the parameter.
	`function wantsSlot(&$slot) { $slot = 5; return $slot; }
	 $holder = 1;
	 $named = "wantsSlot";
	 $lambda = function (&$slot) { $slot = 7; return $slot; };
	 function attemptedSend(callable $call, ...$args) {
	   try { var_dump($call(...$args)); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 try { var_dump(wantsSlot(1)); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { var_dump($named(1)); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { var_dump($named($holder)); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { var_dump($named($holder + 1)); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 var_dump($holder);
	 // extract() takes its array by *prefer*-reference, so a temporary is fine.
	 var_dump(extract(["extracted" => 9]), $extracted);`,
	// A call's result slot is a temporary, not a name: a reference one call left
	// there is replaced by the next, not written through. Two by-reference
	// functions called in turn through the *same* opline is what tells them
	// apart -- the loop below reuses one call site for both.
	`$kept = 1;
	 function &returnsSlot() { global $kept; return $kept; }
	 function &returnsThrough() { return returnsSlot(); }
	 function &returnsValue() { return 42; }
	 $received = null;
	 foreach (["returnsThrough", "returnsValue"] as $which) { $received = $which(); }
	 var_dump($kept, $received);`,
	// The foreach protocol is split: the reset rewinds and asks valid(), and
	// current() and key() belong to the *fetch*. A loop with no key variable
	// never asks for one at all -- so a key() that would raise is never called.
	`class Announces implements Iterator {
	   private $i = 0;
	   public function current(): mixed { echo "current\n"; return $this->i; }
	   public function key(): mixed { echo "key\n"; return $this->i; }
	   public function next(): void { echo "next\n"; $this->i++; }
	   public function rewind(): void { echo "rewind\n"; $this->i = 0; }
	   public function valid(): bool { echo "valid\n"; return $this->i < 2; }
	 }
	 foreach (new Announces() as $v) { echo "body ", $v, "\n"; }
	 echo "--\n";
	 foreach (new Announces() as $k => $v) { echo "body ", $k, "=", $v, "\n"; }
	 echo "--\n";
	 class NeverAsked implements Iterator {
	   public $n = 0;
	   public function valid(): bool { return $this->n < 3; }
	   public function current(): mixed { return $this->n; }
	   public function next(): void { $this->n++; }
	   public function key(): mixed { }
	   public function rewind(): void { $this->n = 0; }
	 }
	 foreach (new NeverAsked() as $item) { echo $item, "\n"; }
	 try { foreach (new NeverAsked() as $k => $item) { echo $k, "\n"; } }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// A cloned Closure runs the same body with the same bindings -- a by-
	// reference capture stays shared -- but it is a distinct object, and a
	// Closure compares only by identity: two are never `==` unless they are the
	// same one. Every other engine class compares by properties as usual.
	`$captured = 1;
	 $original = function ($add) use (&$captured) { return $captured + $add; };
	 $copy = clone $original;
	 var_dump(get_class($copy), is_callable($copy));
	 echo $original(10), " ", $copy(10), "\n";
	 $captured++;
	 echo $original(10), " ", $copy(10), "\n";
	 var_dump($original == $copy, $original === $copy, $original == $original);
	 $alike = function ($add) use (&$captured) { return $captured + $add; };
	 var_dump($original == $alike);
	 function generated() { yield 1; }
	 var_dump(generated() == generated(), new ArrayObject([1]) == new ArrayObject([1]));
	 // A first-class callable made from something that is *already* a Closure
	 // hands back that very object; one made from a method makes a new one each
	 // time.
	 $viaInvoke = $original->__invoke(...);
	 echo $viaInvoke(5), "\n";
	 var_dump($original(...) === $original, $original->__invoke(...) === $original,
	   Closure::fromCallable($original) === $original);
	 class Doubles { public function twice($x) { return $x * 2; } }
	 $doubles = new Doubles();
	 $bound = $doubles->twice(...);
	 $boundCopy = clone $bound;
	 echo $bound(4), " ", $boundCopy(4), "\n";
	 var_dump($doubles->twice(...) === $doubles->twice(...));`,
	// A `?->` chain that short-circuits yields whatever the thing it was part of
	// needed: false inside isset(), true inside empty(), null on its own. The
	// two quiet forms do not report the subject either, so
	// `isset($undefined?->x)` is silent where `$undefined?->x` warns. A chain of
	// several links is decided by its *last* step, not its first.
	`class Middle { public $inner; }
	 class Inner { public $leaf = "leaf"; }
	 var_dump(isset($missing?->x));
	 var_dump(empty($missing?->x));
	 var_dump($absent?->x);
	 var_dump($alsoAbsent?->x ?? "fallback");
	 var_dump(isset($chain?->x->y));
	 var_dump(empty($chain?->x->y));
	 $nulled = null;
	 var_dump(isset($nulled?->x), empty($nulled?->x), $nulled?->x);
	 $held = new Middle();
	 var_dump(isset($held?->inner->leaf), empty($held?->inner->leaf));
	 $held->inner = new Inner();
	 var_dump(isset($held?->inner->leaf), empty($held?->inner->leaf));`,
	// newInstance() on an attribute of an *engine* class runs that class's own
	// constructor: Attribute is annotated with itself, and leaving the object
	// unconstructed left its typed $flags with no value at all.
	`$reflected = new ReflectionClass(Attribute::class);
	 foreach ($reflected->getAttributes() as $attribute) {
	   var_dump($attribute->getName(), $attribute->getArguments());
	   $instance = $attribute->newInstance();
	   var_dump(get_class($instance), $instance->flags === Attribute::TARGET_CLASS);
	 }`,
	// A private property of an *ancestor* is not part of the object's visible
	// table from anywhere else, so writing that name makes a property alongside
	// it and the ancestor's declared type does not apply. Exception::$string is
	// private and typed, which is what makes the shape observable -- and a
	// constructor that writes its own line keeps it, zero included.
	`#[AllowDynamicProperties]
	 class Shadows extends Exception {
	   public function __construct() {
	     $this->string = null;
	     $this->line = 0;
	   }
	 }
	 $shadowing = new Shadows();
	 var_dump($shadowing->getLine(), $shadowing->getMessage());
	 try { throw $shadowing; } catch (Throwable $t) { var_dump(get_class($t), $t->getLine()); }
	 class Ordinary extends Exception {}
	 try { throw new Ordinary("plain"); }
	 catch (Throwable $t) { var_dump($t->getMessage(), $t->getLine() > 0); }`,
	// `Foo::class` written out is folded by the compiler, but the dynamic form
	// reaches the constant fetch and has to answer for itself: it is the class's
	// own name, in its declared spelling, and the match is exact -- `::{'CLASS'}`
	// is an undefined constant.
	`class Named {} class Renamed extends Named {}
	 $which = "class";
	 var_dump(Named::{$which}, Renamed::{$which});
	 $byName = "Named";
	 var_dump($byName::{$which});
	 $instance = new Renamed();
	 var_dump($instance::{$which});
	 try { var_dump(Named::{"CLASS"}); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// A constant the engine has deprecated reports when it is *used*. defined()
	// asks whether the name exists without using it, and says nothing; constant()
	// and the bare name both report. Which constants carry the flag is harvested
	// from the build, not written here.
	`set_error_handler(function ($n, $s) { echo "! ", $s, "\n"; return true; });
	 var_dump(defined("E_STRICT"));
	 var_dump(constant("E_STRICT"));
	 var_dump(E_STRICT);
	 var_dump(E_STRICT | 0);
	 var_dump(E_ALL, defined("E_ALL"), E_NOTICE);`,
	// Static syntax reaching an instance method needs an instance to call it on,
	// and the one in scope only counts when it belongs to that class. Inside a
	// method of C, `self::m()`, `C::m()` and even `$obj::m()` all run with
	// $this; from a static method, from another class, or at top level the
	// engine refuses -- and an *inaccessible* method goes to __callStatic
	// instead of being refused at all.
	`class Above { public function inherited() { return "inherited"; } }
	 class Below extends Above {
	   private function hidden() { return "hidden"; }
	   protected function shielded() { return "shielded"; }
	   public function open() { return "open " . (isset($this) ? get_class($this) : "none"); }
	   public function fromInstance() { return self::open(); }
	   public function byName() { return Below::open(); }
	   public function byObject() { $o = $this; return $o::open(); }
	   public function upward() { return parent::inherited(); }
	   public static function fromStatic() { return Below::open(); }
	   public static function __callStatic($name, $args) { return "callStatic " . $name; }
	   public function __call($name, $args) { return "call " . $name; }
	 }
	 class Elsewhere { public function reach() { return Below::open(); } }
	 $below = new Below();
	 function attempt(callable $what) {
	   try { var_dump($what()); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 attempt(fn() => $below->fromInstance());
	 attempt(fn() => $below->byName());
	 attempt(fn() => $below->byObject());
	 attempt(fn() => $below->upward());
	 attempt(fn() => (new Elsewhere())->reach());
	 attempt(fn() => Below::fromStatic());
	 attempt(fn() => Below::open());
	 attempt(fn() => $below::open());
	 attempt(fn() => $below::hidden());
	 attempt(fn() => $below::shielded());
	 attempt(fn() => $below->hidden());`,
	// A float string offset: a read or a write reports the *cast*, isset() and
	// empty() report the precision loss instead -- spelled at full precision,
	// the way var_dump spells a float -- and `??` reports nothing at all.
	`set_error_handler(function ($n, $s) { echo "! ", $s, "\n"; return true; });
	 $text = "abcdef";
	 echo "-- read\n";     $read = $text[M_PI];
	 echo "-- isset\n";    var_dump(isset($text[M_PI]));
	 echo "-- empty\n";    var_dump(empty($text[M_PI]));
	 echo "-- coalesce\n"; $coalesced = $text[M_PI] ?? null;
	 echo "-- write\n";    $text[M_PI] = "z";
	 echo "-- array\n";    $list = [1, 2, 3]; $fromList = $list[M_PI] ?? null;
	 var_dump($read, $coalesced, $text);`,
	// `self::class` and `__CLASS__` inside a *trait's* attribute cannot be folded
	// when the trait is compiled -- the answer belongs to whichever class
	// composes it -- so they arrive unfolded and are resolved when the attribute
	// is read. A class's own method has them folded already.
	`#[Attribute]
	 class Records { public function __construct(...$args) {} }
	 trait Composed {
	   #[Records(self::class)] public function viaSelf() {}
	   #[Records(__CLASS__)] public function viaMagic() {}
	   #[Records(self::MARK)] public function viaConstant() {}
	 }
	 class Composer { use Composed; const MARK = "marked"; }
	 class Direct { #[Records(self::class, self::MARK)] public function own() {} const MARK = "own"; }
	 $composer = new ReflectionClass("Composer");
	 foreach (["viaSelf", "viaMagic", "viaConstant"] as $which) {
	   echo $which, ": ";
	   var_dump($composer->getMethod($which)->getAttributes()[0]->getArguments());
	 }
	 $direct = new ReflectionClass("Direct");
	 var_dump($direct->getMethod("own")->getAttributes()[0]->getArguments());`,
	// `static $x` rebinds the name to the static's box, so whatever the local
	// held loses its last holder there -- and a destructor that throws is thrown
	// *at that point*, inside whatever try is open, not at shutdown.
	`class Noisy {
	   public function __construct(public string $tag) {}
	   public function __destruct() { throw new Exception("gone " . $this->tag); }
	 }
	 function rebinds() {
	   $held = new Noisy("local");
	   static $held;
	   return "reached";
	 }
	 try { var_dump(rebinds()); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 echo "after\n";`,
	// Dumping a closure shows its static-variable table: the `use` captures --
	// a by-reference one keeping its marker -- and any `static $x` it declares,
	// which reads as null until the closure has actually run. An initializer
	// that threw leaves the static uninitialized, so the next call retries it.
	`// The opening line carries the object handle and the next five carry a path,
	 // both of which the harness spells differently; the rest is the point.
	 function shown($subject) {
	   ob_start();
	   var_dump($subject);
	   $text = ob_get_clean();
	   foreach (explode("\n", $text) as $at => $line) {
	     if ($at > 5) { echo $line, "\n"; }
	   }
	 }
	 $byValue = 1;
	 $byReference = "two";
	 $capturing = function ($a, $b = 2, ...$rest) use ($byValue, &$byReference) {};
	 shown($capturing);
	 $deferred = function () { static $later = LATER_ON; };
	 shown($deferred);
	 try { $deferred(); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 shown($deferred);
	 define("LATER_ON", "arrived");
	 $deferred();
	 shown($deferred);`,
	// Which property a name means is decided by the scope reading it: inside A,
	// `$this->prop` is A's private one even on an instance of a B that declares
	// its own. php2go still keeps a single slot per name, so the two share
	// storage -- what this case pins down is the *access* rule, not the storage.
	`class Holder {
	   private int $slot = 42;
	   public function through() {
	     $alias =& $this->slot;
	     unset($this->slot);
	     $alias = "rebound";
	     return $alias;
	   }
	 }
	 class Shadowing extends Holder { private $slot; }
	 var_dump((new Shadowing())->through());`,
	// A method named at run time is read up to the first NUL byte, which __call
	// and __callStatic can see -- and an *empty* name is a legal one, so
	// "forwards to an accessor" cannot be told by the name being non-empty.
	`class Answers {
	   public static function __callStatic($name, $args) {
	     var_dump("static", strlen($name), $name, $args);
	   }
	   public function __call($name, $args) {
	     var_dump("instance", strlen($name), $name, $args);
	   }
	 }
	 $className = "Answers";
	 $instance = new Answers();
	 foreach (["", "\0", "\0after", "before\0after"] as $method) {
	   $className::$method("one");
	   $instance->$method();
	 }`,
	// `[&$x]` binds the variable's slot rather than reading it, so an $x that
	// does not exist yet is created silently -- the way `$y = &$x` creates one,
	// and without the "undefined variable" a read would report.
	`$madeByReference = [&$neverAssigned];
	 var_dump($madeByReference, $neverAssigned);
	 $keyed = ["first" => 1, "second" => &$alsoNew];
	 var_dump($keyed, isset($alsoNew));`,
	// The deprecated Serializable interface writes its own payload, tagged `C:`
	// rather than `O:`, and reading one back builds the object without its
	// constructor and hands it the bytes. __serialize wins when a class has
	// both, and a serialize() that answers null writes a plain null.
	`set_error_handler(function ($n, $s) { echo "! ", substr($s, 0, 40), "\n"; return true; });
	 class Writes implements Serializable {
	   public $held = 1;
	   public function serialize() { return "payload"; }
	   public function unserialize($data) { echo "read ", $data, "\n"; $this->held = 99; }
	 }
	 class WritesNothing implements Serializable {
	   public function serialize() { return null; }
	   public function unserialize($data) {}
	 }
	 class PrefersModern implements Serializable {
	   public function serialize() { return "old"; }
	   public function unserialize($data) {}
	   public function __serialize(): array { return ["new" => 1]; }
	   public function __unserialize(array $data): void {}
	 }
	 $written = serialize(new Writes());
	 var_dump($written);
	 var_dump(unserialize($written));
	 var_dump(serialize(new WritesNothing()));
	 var_dump(serialize(new PrefersModern()));`,
	// `global $$name` and `global ${expr}` are compiled as a pair of fetches by
	// name and an ASSIGN_REF, not as the BIND_GLOBAL the written-out form gets.
	// The box handed out belongs to the *name*, so the rebinding has to reach
	// the name rather than stop at the temporary holding it.
	`function named() { echo "asked\n"; return "shared"; }
	 function viaLiteral() { global $shared; return $shared; }
	 function viaVariable() { $which = "shared"; global $$which; return $shared; }
	 function viaFolded() { global ${"shared"}; return $shared; }
	 function viaCall() { global ${named()}; return $shared; }
	 function writesBack() { $which = "shared"; global $$which; $shared = "written"; }
	 $shared = 42;
	 var_dump(viaLiteral(), viaVariable(), viaFolded(), viaCall());
	 writesBack();
	 var_dump($shared);`,
	// A get hook runs at the turn its property comes up in a foreach, not when
	// the loop starts: a hook that prints interleaves with the body rather than
	// emitting every line first.
	`class Announced {
	   public $plain = "plain";
	   private $_backing = "hooked";
	   public $first { get { echo "get first\n"; return $this->_backing . "-1"; } }
	   public $second { get { echo "get second\n"; return $this->_backing . "-2"; } }
	 }
	 foreach (new Announced() as $name => $value) { echo "body ", $name, " = ", $value, "\n"; }`,
	// Copying an array carries a reference two or more names hold across as the
	// same slot -- writing through either is visible in both -- and flattens
	// one that is down to its last holder. A reference into the structure being
	// copied is held by that structure whatever else is going on.
	`$holder = ["x", "y"];
	 $alias = &$holder[1];
	 $copy = $holder;
	 var_dump($holder, $copy);
	 $copy[1] = "changed";
	 var_dump($holder[1], $alias);
	 unset($alias);
	 $later = $holder;
	 var_dump($later);
	 function &cyclic() { $inner = []; $inner[] = &$inner; return $inner; }
	 $cycle = cyclic();
	 $cycleCopy = $cycle;
	 $cycleCopy[0] = 123;
	 print_r($cycle); print_r($cycleCopy);
	 // A nested array is a value and copies all the way down; a reference to
	 // one is a slot and does not.
	 $nested = [["a"], ["b"]];
	 $deep = $nested;
	 $deep[0][0] = "mutated";
	 var_dump($nested[0][0], $deep[0][0]);
	 $shared = [["a"]];
	 $into = &$shared[0];
	 $sharedCopy = $shared;
	 $sharedCopy[0][0] = "reached";
	 var_dump($shared[0][0]);
	 // The functions that rebuild an array follow the same rule.
	 $list = ["p", "q", "r"];
	 $kept = &$list[1];
	 var_dump(array_values($list));
	 var_dump(array_reverse($list));
	 var_dump(array_reverse($list, true));
	 array_values($list)[1] = "no effect";
	 var_dump($list[1]);`,
	// An ArrayIterator built over an *object* is that object's property table
	// rather than a copy of it: writing through the iterator reaches the
	// object, count() reports the properties, and a by-reference loop binds the
	// property's own slot -- which carries the property's declared type, so a
	// write through the loop variable is checked against it.
	`class Backed { public string $foo = "bar"; public $loose = 1; }
	 $subject = new Backed;
	 $walk = new ArrayIterator($subject);
	 var_dump(count($walk), $walk["foo"]);
	 $walk["loose"] = 99;
	 var_dump($subject->loose);
	 foreach ($walk as $k => &$slot) { $slot = 42; }
	 var_dump($subject);
	 try { foreach ($walk as &$slot2) { $slot2 = []; } }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 var_dump($subject);
	 // An array argument is still copied, so writing through the iterator does
	 // not reach the caller's array.
	 $plain = ["x" => 1];
	 $copied = new ArrayIterator($plain);
	 $copied["x"] = 2;
	 var_dump($plain["x"], $copied["x"]);
	 // And an aggregate over an object works the same way through getIterator.
	 class BackedAggregate implements IteratorAggregate {
	   public function __construct(public string $held = "start") {}
	   public function getIterator(): Traversable { return new ArrayIterator($this); }
	 }
	 $aggregate = new BackedAggregate;
	 foreach ($aggregate as $k => &$slot3) { $slot3 = "moved"; }
	 var_dump($aggregate);`,
	// An ArrayAccess destructures through offsetGet, and a by-reference target
	// binds what the accessor returned -- which is a slot only when the class
	// declared `function &offsetGet`. Anything else is a value, and binding to
	// it reaches a copy, which PHP says rather than refuses.
	`class SlotStore implements ArrayAccess {
	   private $held = [];
	   function __construct(array $a) { $this->held = $a; }
	   function &offsetGet($k): mixed { return $this->held[$k]; }
	   function offsetSet($k, $v): void { $this->held[$k] = $v; }
	   function offsetExists($k): bool { return isset($this->held[$k]); }
	   function offsetUnset($k): void { unset($this->held[$k]); }
	 }
	 class ValueStore implements ArrayAccess {
	   private $held = [5, 6];
	   function offsetGet($k): mixed { return $this->held[$k]; }
	   function offsetSet($k, $v): void { $this->held[$k] = $v; }
	   function offsetExists($k): bool { return isset($this->held[$k]); }
	   function offsetUnset($k): void {}
	 }
	 $slots = new SlotStore([1, 2]);
	 list(&$first, $second) = $slots;
	 var_dump($slots, $first, $second);
	 $first = 99;
	 var_dump($slots);
	 $values = new ValueStore;
	 list(&$one, $two) = $values;
	 var_dump($one, $two);
	 $one = 77;
	 var_dump($values);`,
	// A by-reference generator yields the slot itself, so a by-reference loop
	// over it writes back -- and the slot keeps its mark afterwards, because
	// the generator's frame ending is not the loop losing its hold on it.
	`function &yieldSlot(array &$source) { yield $source[0]; }
	 $backing = [1, 2, 3];
	 foreach (yieldSlot($backing) as &$slot) { $slot *= -1; }
	 var_dump($backing);
	 function &yieldTwo(array &$source) { yield $source[0]; yield $source[2]; }
	 $second = [1, 2, 3];
	 foreach (yieldTwo($second) as &$slot2) { $slot2 = "seen"; }
	 var_dump($second);
	 // A generator that is not declared by reference cannot be looped that way.
	 function plainYield() { yield 1; }
	 try { foreach (plainYield() as &$nope) {} }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }`,
	// A generator abandoned part-way through still runs its finally blocks:
	// PHP resumes it one last time to unwind it, and nothing can catch that --
	// only the finallys on the way out see it. One that finished, or never
	// started, has nothing left to unwind.
	`function guarded() {
	   try {
	     try { yield 1; yield 2; } finally { echo "inner finally\n"; }
	   } catch (Exception $e) { echo "not reached\n"; }
	   finally { echo "outer finally\n"; }
	   echo "not reached either\n";
	 }
	 echo "-- abandoned at the first yield\n";
	 guarded()->current();
	 gc_collect_cycles();
	 echo "-- abandoned after one step\n";
	 $held = guarded();
	 $held->current();
	 $held->next();
	 unset($held);
	 gc_collect_cycles();
	 echo "-- run to the end\n";
	 foreach (guarded() as $value) { echo $value, "\n"; }
	 echo "-- never started\n";
	 $never = guarded();
	 unset($never);
	 gc_collect_cycles();
	 // A generator still being delegated to is held by the delegator, however
	 // little else names it.
	 echo "-- delegated\n";
	 function delegating() { yield from guarded(); }
	 $outer = delegating();
	 $outer->current();
	 gc_collect_cycles();
	 echo "still here\n";
	 var_dump($outer->current());
	 unset($outer);
	 gc_collect_cycles();
	 echo "done\n";`,
	// A `return` inside a finally abandons the exception that finally was
	// entered with -- and only that one. A return from a *nested* finally emits
	// one discard per block it leaves, innermost first, and the block outside
	// still has its own exception on the way out.
	`function unwound($shape) {
	   echo "== ", $shape, "\n";
	   try {
	     try { throw new Exception("one"); }
	     finally {
	       if ($shape === "throw in finally") { throw new Exception("two"); }
	       if ($shape === "return in finally") { try {} finally { return 42; } }
	       if ($shape === "return then throw") {
	         try { try {} finally { return 42; } } finally { throw new Exception("two"); }
	       }
	       if ($shape === "nested try throws") { try { throw new Exception("two"); } finally {} }
	       if ($shape === "plain return") { return 7; }
	     }
	   } catch (Exception $e) {
	     do { echo $e->getMessage(), "\n"; $e = $e->getPrevious(); } while ($e);
	   }
	   return "fell through";
	 }
	 foreach (["throw in finally", "return in finally", "return then throw",
	           "nested try throws", "plain return", "none"] as $shape) {
	   var_dump(unwound($shape));
	 }
	 // Three deep, to check the discards pair with the right blocks.
	 function deeplyUnwound() {
	   try { throw new Exception("outer"); }
	   finally {
	     try {
	       try { try {} finally { return "returned"; } } finally { echo "middle finally\n"; }
	     } finally { throw new Exception("inner"); }
	   }
	 }
	 try { var_dump(deeplyUnwound()); }
	 catch (Exception $e) { do { echo $e->getMessage(), "\n"; $e = $e->getPrevious(); } while ($e); }`,
	// call_user_func and call_user_func_array differ in what they do with a
	// reference. The plain one takes its arguments through its own by-value
	// parameters, so a callee that wanted a slot gets a copy and is told. The
	// array form hands the elements over as they are, so an element that is
	// already a reference stays one -- which is the only way a by-reference
	// parameter is reached this way at all.
	`function bumps(&$n) { $n++; }
	 $closure = function (&$n) { $n++; };
	 $plain = 1; call_user_func("bumps", $plain); var_dump($plain);
	 $byRef = 1; call_user_func_array("bumps", [&$byRef]); var_dump($byRef);
	 $byVal = 1; call_user_func_array("bumps", [$byVal]); var_dump($byVal);
	 $held = 1; $list = [&$held]; call_user_func_array("bumps", $list); var_dump($held);
	 // An engine function's by-reference parameter is named by the harvest, so
	 // it earns the same complaint.
	 call_user_func_array("str_replace", ["a", "b", "banana", new stdClass]);
	 // Passing a real reference earns no complaint. What str_replace then
	 // writes into it is a separate gap, so this only checks the silence.
	 $counted = 0;
	 call_user_func_array("str_replace", ["a", "b", "banana", &$counted]);
	 echo "no warning above\n";
	 // Calling __invoke by name is the closure's own call written the long
	 // way, and method names are case-insensitive.
	 $one = 1; $closure->__invoke($one); var_dump($one);
	 $two = 1; $closure->__INVOKE($two); var_dump($two);`,
	// An engine interface's return types are *tentative*: types the engine
	// means to enforce eventually. A class that does not match one is
	// deprecated rather than refused, and can say it knows by putting
	// #[\ReturnTypeWillChange] on the method. Matching exactly says nothing,
	// and the parameters are enforced whatever the return type does.
	`class Untyped implements ArrayAccess {
	   public function offsetExists($o) { return true; }
	   public function offsetGet($o) { return 1; }
	   public function offsetSet($o, $v) {}
	   public function offsetUnset($o) {}
	 }
	 echo "-- silenced\n";
	 class Silenced implements ArrayAccess {
	   #[\ReturnTypeWillChange] public function offsetExists($o) { return true; }
	   #[\ReturnTypeWillChange] public function offsetGet($o) { return 1; }
	   #[\ReturnTypeWillChange] public function offsetSet($o, $v) {}
	   #[\ReturnTypeWillChange] public function offsetUnset($o) {}
	 }
	 echo "-- exact\n";
	 class Exactly implements ArrayAccess {
	   public function offsetExists($o): bool { return true; }
	   public function offsetGet($o): mixed { return 1; }
	   public function offsetSet($o, $v): void {}
	   public function offsetUnset($o): void {}
	 }
	 echo "-- narrowed\n";
	 class Narrowed implements Countable {
	   public function count(): int { return 1; }
	 }
	 echo "-- widened\n";
	 class Widened implements Countable {
	   public function count(): mixed { return 1; }
	 }
	 var_dump(count(new Narrowed));
	 echo "done\n";`,
	// A variance check resolves the *narrower* side's class names whatever the
	// wider side names -- `object` accepts any class, but the engine still
	// looks the class up, and a name that does not resolve leaves the relation
	// undecided rather than accepted. `mixed` and an identical pair short
	// circuit before any lookup, which is why neither reports a missing class.
	`class VarianceParent {
	   function wide(): object {}
	   function anything(): mixed {}
	   function same(): VarianceParent {}
	 }
	 // Only shapes that link: one that does not is a fatal, and the oracle
	 // cannot be asked about those.
	 $shapes = [
	   "mixed <- undeclared" => "class V2 extends VarianceParent { function anything(): AlsoNot {} }",
	   "identical"           => "class V3 extends VarianceParent { function same(): VarianceParent {} }",
	   "object <- declared"  => "class V5 extends VarianceParent { function wide(): VarianceParent {} }",
	   "object <- nullable"  => "class V6 extends VarianceParent { function wide(): VarianceParent {} }",
	 ];
	 foreach ($shapes as $label => $source) {
	   echo "== ", $label, "\n";
	   try { eval($source); echo "linked\n"; }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A class whose dependencies cannot be obtained is not declared: PHP
	// unlinks it, so the same declaration may be attempted again. A name the
	// compiler bound itself is taken before any of this, which is why only a
	// conditional declaration can be retried.
	`for ($attempt = 0; $attempt < 3; $attempt++) {
	   try {
	     class Retried extends NeverDeclared {}
	     echo "linked\n";
	   } catch (Throwable $t) {
	     echo $attempt, ": ", get_class($t), ": ", $t->getMessage(), "\n";
	   }
	 }
	 var_dump(class_exists("Retried", false));
	 // The successful case is not exercised here: a second attempt after one
	 // that linked is a fatal redeclaration, which the oracle cannot be asked
	 // about. The corpus covers it.
	 echo "done\n";`,
	// Reading an undefined variable warns once and yields null. Passing that
	// null to a parameter does not warn a second time inside the callee, and an
	// append on an ArrayAccess names no offset -- so the accessors are called
	// with null, the same thing `$o[] = x` passes offsetSet.
	`function takesOne($arg) { var_dump($arg); }
	 takesOne($neverSet);
	 takesOne(arg: $alsoNeverSet);
	 function takesDefault($arg = "fallback") { var_dump($arg); }
	 takesDefault($stillNotSet);
	 takesDefault();
	 class Appended implements ArrayAccess {
	   private $held = [];
	   public function offsetExists($o): bool { echo "exists "; var_dump($o); return true; }
	   #[\ReturnTypeWillChange] public function offsetGet($o) { echo "get "; var_dump($o); return 100; }
	   public function offsetSet($o, $v): void { echo "set "; var_dump($o); var_dump($v); }
	   public function offsetUnset($o): void {}
	 }
	 $target = new Appended;
	 var_dump($target[] += 5);
	 $target[] = "plain";
	 var_dump($target["named"] += 1);`,
	// `xor` evaluates both sides -- it has an opcode rather than the jumps
	// `&&` and `||` compile to. Unpacking something that is not a Traversable
	// is a TypeError in a call and a plain Error in an array literal, with the
	// same wording. A string offset can be assigned but not compound-assigned.
	`var_dump(true xor false, 1 xor 1, "a" xor "", null xor []);
	 function counts(...$rest) { return count($rest); }
	 foreach ([null, 1, "x", 1.5, true] as $bad) {
	   try { var_dump(counts(...$bad)); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	   try { var_dump([...$bad]); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 foreach ([true, 1, 1.5] as $scalar) {
	   try { $held = $scalar; $held[0] .= "x"; }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 $text = "abc";
	 try { $text[0] .= "x"; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 $text[0] = "z"; var_dump($text);
	 foreach ([null, 1, "x", new stdClass] as $notArray) {
	   try { var_dump(array_key_exists("a", $notArray)); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 echo "done\n";`,
	// error_get_last answers about what happened, not about what was printed:
	// a diagnostic `@` suppressed is still the last error.
	`@$neverDefined;
	 $last = error_get_last();
	 var_dump($last["type"], $last["message"]);
	 error_clear_last();
	 var_dump(error_get_last());
	 @strlen(...[]);
	 var_dump(error_get_last() === null);`,
	// forward_static_call is call_user_func with the late static binding kept:
	// `static::` inside the callee still means what the *calling* method's did.
	// It refuses outside a class, where there would be no binding to forward.
	// And a static property is part of the class, so unset() has nothing to
	// take away and says so.
	`class ForwardBase {
	   static function who() { return static::class; }
	   static function joined(...$rest) { return static::class . ":" . implode(",", $rest); }
	   static function viaPair() { return forward_static_call(["ForwardBase", "who"]); }
	   static function viaName() { return forward_static_call("ForwardBase::who"); }
	   static function viaArray() { return forward_static_call_array(["ForwardBase", "joined"], [1, 2]); }
	   static function plain() { return call_user_func(["ForwardBase", "who"]); }
	 }
	 class ForwardChild extends ForwardBase {}
	 var_dump(ForwardChild::viaPair(), ForwardChild::viaName(),
	          ForwardChild::viaArray(), ForwardChild::plain());
	 try { forward_static_call("strtoupper", "ab"); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 // The array form has no such check in the engine and simply calls.
	 var_dump(forward_static_call_array("strtoupper", ["ab"]));
	 class StaticHolder { public static $held = 1; }
	 try { unset(StaticHolder::$held); }
	 catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 var_dump(StaticHolder::$held);`,
	// An internal function reports the wrong number of arguments before it
	// looks at any of them, and which of the three wordings it uses depends on
	// which bound was missed -- with the count singular only when it is one.
	`$calls = [["strlen", []], ["strlen", ["a", "b"]], ["str_replace", ["a"]],
	           ["array_slice", [[1], 0, 1, true, 9]], ["in_array", [1]],
	           ["implode", []], ["array_merge", []], ["str_repeat", ["a"]],
	           ["ucfirst", ["a", "b"]], ["array_keys", [[1], 1, true, 9]]];
	 foreach ($calls as [$fn, $args]) {
	   try { var_dump(call_user_func_array($fn, $args)); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A by-reference parameter reached from inside an internal function gets a
	// copy: there are no caller slots in a call assembled in C. array_walk is
	// the exception, because it hands its callback the element itself.
	`function refPair(&$a, &$b) { return $a + $b; }
	 function refScale(&$v, $k) { $v *= 10; }
	 function refCmp(&$x, &$y) { return $x <=> $y; }
	 var_dump(array_reduce([1, 2, 3], "refPair", 0));
	 $items = [1, 2];
	 array_walk($items, "refScale");
	 var_dump($items);
	 $pairs = [3, 1, 2];
	 usort($pairs, "refCmp");
	 var_dump($pairs);
	 call_user_func_array("str_replace", ["a", "b", "banana", new stdClass]);
	 call_user_func_array("str_replace", ["a", "b", "banana", "count" => new stdClass]);
	 echo "done\n";`,
	// An internal function's arguments are checked by the same rule a user
	// function's are, and the rule is about the file the *call* is in: under
	// strict_types nothing converts, and null stops being the special case that
	// only earns a deprecation.
	`declare(strict_types=1);
	 foreach ([1, "1", 1.0, true, null, []] as $v) {
	   foreach (["strlen", "ord", "abs", "str_repeat"] as $fn) {
	     try {
	       var_dump($fn === "str_repeat" ? str_repeat("a", $v) : $fn($v));
	     } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	   }
	 }`,
	// A by-reference parameter is bound rather than converted, but its declared
	// type is still checked -- and a variable that does not exist yet is null
	// for the purpose of that check, not an empty slot that satisfies anything.
	`function refArray(array &$r) { $r[] = 1; }
	 function refInt(int &$n) { $n++; }
	 $cases = ["scalar" => 1, "string" => "3", "array" => [7]];
	 foreach ($cases as $label => $value) {
	   foreach (["refArray", "refInt"] as $fn) {
	     $held = $value;
	     try { $fn($held); var_dump($label, $held); }
	     catch (Throwable $t) { echo get_class($t), ": ", explode(", called in", $t->getMessage())[0], "\n"; }
	   }
	 }
	 try { refArray($neverSet); } catch (Throwable $t) { echo get_class($t), ": ", explode(", called in", $t->getMessage())[0], "\n"; }
	 var_dump(isset($neverSet));`,
	// call_user_func_array with an array_slice for its argument list is folded
	// into a single opcode that slices while it sends, so the check array_slice
	// would have made has to happen there instead -- including its wording.
	`$list = [1, 2, 3];
	 foreach ([[], 2.0, null, "2", -1, 99, true] as $len) {
	   try { call_user_func_array("var_dump", array_slice($list, 0, $len)); }
	   catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	   echo "--\n";
	 }
	 function sliceSeen(...$got) { echo "seen ", count($got), ": ", implode(",", $got), "\n"; }
	 call_user_func_array("sliceSeen", array_slice($list, 2, null));
	 call_user_func_array("sliceSeen", array_slice($list, 5, null));
	 call_user_func_array("sliceSeen", array_slice($list, 1, 0));`,
	// A `callable` parameter is checked by looking the callable up, so a name
	// that resolves to nothing is refused where the call would have been. An
	// internal function spells out why the callback is not one; a user function
	// only reports the type it wanted.
	`function cbTakes(callable $c) { echo "ok\n"; }
	 function cbAny(...$rest) { return 0; }
	 class CbHolder { public function m() {} private function hidden() {} }
	 $bad = ["cbAny", "no_such_function_here", [new CbHolder, "nope"],
	         [new CbHolder, "hidden"], [1, 2, 3], ["CbHolder", "m"], null];
	 foreach ($bad as $candidate) {
	   try { cbTakes($candidate); } catch (Throwable $t) { echo get_class($t), ": ", explode(", called in", $t->getMessage())[0], "\n"; }
	   try { var_dump(array_map($candidate, [1])); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	   try { $sorted = [2, 1]; usort($sorted, $candidate); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 var_dump(is_callable("no_such_function_here"), is_callable("cbAny"));`,
	// #[\Deprecated] is an object: using what it marks constructs one, and the
	// constructor's `?string` parameters are checked like any others. Without
	// strict_types an int or float coerces; an array or an object never does.
	// An enum case in a stack trace is written as the case, not as an object.
	`enum DepEnum { case One; }
	 #[\Deprecated(1234)] function depInt() {}
	 #[\Deprecated(1.5)] function depFloat() {}
	 #[\Deprecated(null)] function depNull() {}
	 #[\Deprecated(message: "m", since: 2)] function depSince() {}
	 #[\Deprecated([])] function depArray() {}
	 #[\Deprecated(DepEnum::One)] function depCase() {}
	 function depTry(string $label, callable $fn) {
	   echo "### $label\n";
	   try { $fn(); } catch (Throwable $t) {
	     echo get_class($t), ": ", $t->getMessage(), "\n";
	     foreach ($t->getTrace() as $i => $frame) {
	       echo $i, ": ", $frame["function"], "\n";
	     }
	   }
	 }
	 foreach (["int" => "depInt", "float" => "depFloat", "null" => "depNull",
	           "since" => "depSince", "array" => "depArray", "case" => "depCase"] as $l => $fn) {
	   depTry($l, $fn);
	 }`,
	// SensitiveParameterValue holds one value and shows nothing: every path
	// that walks an object's properties sees an empty table, while reflection,
	// which reads the slot rather than the table, still finds it. Hiding it is
	// the whole purpose of the class.
	`$sv = new SensitiveParameterValue("secret");
	 var_dump($sv);
	 print_r($sv); echo "\n";
	 echo var_export($sv, true), "\n";
	 var_dump((array) $sv, json_encode($sv), get_object_vars($sv));
	 foreach ($sv as $k => $v) { echo "saw $k\n"; }
	 var_dump($sv->getValue());
	 $clone = clone $sv;
	 var_dump($clone->getValue(), $clone == $sv, $clone === $sv);
	 var_dump((new SensitiveParameterValue(null))->getValue());
	 var_dump((new SensitiveParameterValue([1, 2]))->getValue());
	 $reflected = (new ReflectionClass($sv))->getProperty("value");
	 var_dump($reflected->getValue($sv));`,
	// A Traversable spreads with its keys, so a generator or iterator can
	// supply named arguments too -- and a by-reference parameter reached that
	// way is passed by value, with a warning, because a Traversable yields
	// values rather than slots.
	`function travTarget($a = "a", $b = "b", $c = "c") { echo "a=$a b=$b c=$c\n"; }
	 function travRef($a = null, &$b = null) { $b++; }
	 function travTry(string $label, callable $fn) {
	   echo "### $label\n";
	   try { $fn(); } catch (Throwable $t) { echo "! ", $t->getMessage(), "\n"; }
	 }
	 $probe = [
	   'all named'   => function () { travTarget(...new ArrayIterator(["c" => "C", "a" => "A"])); },
	   'mixed'       => function () { travTarget(...new ArrayIterator(["A", "c" => "C"])); },
	   'out of order' => function () { travTarget(...new ArrayIterator(["A", "b" => "B", "C"])); },
	   'by reference' => function () { $a = ["b" => 0]; travRef(...$a); var_dump($a); },
	   'traversable ref' => function () { travRef(...new ArrayIterator(["b" => 0])); },
	 ];
	 foreach ($probe as $label => $fn) { travTry($label, $fn); }`,
	// The same call written two ways: unqualified inside a namespace the
	// compiler cannot fold call_user_func_array, and the named keys have to
	// survive the Go-side path just as they do through the opcodes.
	`namespace CufaNs;
	 $target = function ($a = "a", $b = "b", $c = "c") { echo "a=$a b=$b c=$c\n"; };
	 $variadic = function (...$args) { \var_dump($args); };
	 \call_user_func_array($target, ["A", "c" => "C"]);
	 call_user_func_array($target, ["A", "c" => "C"]);
	 call_user_func_array($variadic, ["A", "c" => "C"]);
	 try { call_user_func_array($target, ["d" => "D"]); }
	 catch (\Throwable $t) { echo $t->getMessage(), "\n"; }
	 try { call_user_func_array($target, ["c" => "C", "A"]); }
	 catch (\Throwable $t) { echo $t->getMessage(), "\n"; }`,
	// A string key in an unpacked array names a parameter -- and *occupies*
	// its slot, so a positional argument after it takes the next one along.
	// Across two spreads the ordering rule starts again; within one array it
	// does not.
	`function namedTarget($a = 1, $b = 2, $c = 3) { return "$a|$b|$c"; }
	 function namedTry(string $label, callable $fn) {
	   echo "### $label\n";
	   try { var_dump($fn()); } catch (Throwable $t) { echo "! ", $t->getMessage(), "\n"; }
	 }
	 $probe = [
	   'spread named'    => function () { return namedTarget(...["b" => 20]); },
	   'spread mixed'    => function () { return namedTarget(...[10, "c" => 30]); },
	   'spread twice'    => function () { return namedTarget(...["b" => 20], ...[7]); },
	   'positional then' => function () { return namedTarget(9, ...["c" => 3]); },
	   'overwrites'      => function () { return namedTarget(10, ...["a" => 99]); },
	   'unknown name'    => function () { return namedTarget(...["zz" => 1]); },
	   'out of order'    => function () { return namedTarget(...["b" => 20, 5]); },
	   'cufa named'      => function () { return call_user_func_array("namedTarget", ["b" => 20]); },
	   'cufa mixed'      => function () { return call_user_func_array("namedTarget", [10, "c" => 30]); },
	   'cufa unknown'    => function () { return call_user_func_array("namedTarget", ["zz" => 1]); },
	   'cufa order'      => function () { return call_user_func_array("namedTarget", ["b" => 20, 5]); },
	 ];
	 foreach ($probe as $label => $fn) { namedTry($label, $fn); }`,
	// A compound assignment to a static property reads before it writes, faces
	// the property's declared type, and reports an uninitialized typed one.
	`class StatOp { public static $n = 1; public static $s = "a";
	                public static int $t; private static $p = 5; }
	 StatOp::$n += 4; StatOp::$s .= "b"; StatOp::$n **= 2;
	 var_dump(StatOp::$n, StatOp::$s, StatOp::$n <<= 1);
	 try { StatOp::$t += 1; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 try { StatOp::$p .= "x"; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }`,
	// A trait alias is a name the class has, so get_class_methods lists it --
	// and the order is the engine's: a class's own aliases sit right after its
	// own methods, each trait's right after that trait's.
	`trait AliasP { public function m() { return "P"; } }
	 trait AliasC { use AliasP { m as fromParent; } public function m() { return "C"; } }
	 class AliasNested { use AliasC; }
	 trait AliasD { public function d() {} }
	 class AliasDirect { use AliasD { d as renamed; } }
	 class AliasVis { use AliasD { d as protected quiet; }
	                  static function inside() { return get_class_methods("AliasVis"); } }
	 print_r(get_class_methods("AliasNested"));
	 print_r(get_class_methods("AliasDirect"));
	 print_r(get_class_methods("AliasVis"));
	 print_r(AliasVis::inside());
	 $o = new AliasNested;
	 var_dump($o->m(), $o->fromParent());`,
	// get_class_vars reports the class's variables: a property whose hooks give
	// it no backing store is not one, and a backed hooked property is listed
	// with its declared default rather than what its get hook would answer.
	`class VarsC {
	   public $a = 42;
	   public $b = 42 { get => $this->b * 2; }
	   public $c { get => 42; }
	   public int $d;
	   private $e = 1;
	   public static $f = 2;
	 }
	 var_dump(get_class_vars(VarsC::class));
	 var_dump(get_object_vars(new VarsC));`,
	// A closure cannot take an engine class as its scope: there is no PHP body
	// for its private members to belong to. Only an *explicit* scope is
	// refused -- bindTo with no second argument keeps the closure's own.
	`class BindMine { public $p = 1; }
	 $bindFn = function () { return $this->p ?? "none"; };
	 var_dump($bindFn->call(new BindMine));
	 var_dump($bindFn->call(new stdClass));
	 var_dump($bindFn->call(new ArrayObject));
	 var_dump(Closure::bind($bindFn, new stdClass, stdClass::class));
	 var_dump($bindFn->bindTo(new stdClass) !== null);
	 var_dump(Closure::bind($bindFn, new BindMine, BindMine::class) !== null);`,
	// call_user_func hands its arguments over by value, so a callee that wanted
	// a slot gets a copy -- and PHP says so once per such parameter. Written
	// unqualified inside a namespace the compiler cannot fold the call, and the
	// callee is reached a different way; the report is the same either way.
	`namespace CufNs;
	 function takesRef(&$ref) { $ref = 24; }
	 $a = 42; \call_user_func(__NAMESPACE__ . "\\takesRef", $a); \var_dump($a);
	 $b = 42; call_user_func(__NAMESPACE__ . "\\takesRef", $b); \var_dump($b);
	 $c = 42; \call_user_func_array(__NAMESPACE__ . "\\takesRef", [$c]); \var_dump($c);`,
	// A method's static variable is filed under the scope that declared the
	// method, which the file and function name alone cannot express: two
	// classes in one file may both have a `c`, and a trait's one op_array is
	// shared by every class that composes it.
	`class StatBase { static function c() { static $n = 0; return ++$n; } }
	 class StatSub extends StatBase {}
	 class StatOver extends StatBase { static function c() { static $n = 100; return ++$n; } }
	 trait StatT { static function c() { static $n = 0; return ++$n; } }
	 class StatU1 { use StatT; }
	 class StatU2 { use StatT; }
	 trait StatT2 { function i() { static $m = 0; return ++$m; } }
	 class StatW1 { use StatT2; }
	 class StatW2 extends StatW1 {}
	 function statPlain() { static $p = 0; return ++$p; }
	 echo "inherited: ", StatBase::c(), StatSub::c(), StatBase::c(), "\n";
	 echo "override: ", StatOver::c(), StatBase::c(), "\n";
	 echo "trait: ", StatU1::c(), StatU2::c(), StatU1::c(), "\n";
	 $w1 = new StatW1; $w2 = new StatW2;
	 echo "trait inherited: ", $w1->i(), $w2->i(), $w1->i(), "\n";
	 echo "function: ", statPlain(), statPlain(), "\n";`,
	// A receiver that arrived in a temporary has no other name, so the call is
	// what lets go of it -- and its handle goes to the next `new`.
	`class RecvH { public function __construct(public int $x = 0) {} public function id() { return $this->x; } }
	 var_dump((new RecvH(1))->id());
	 var_dump(new RecvH(2));
	 var_dump((new ReflectionClass(RecvH::class))->getName());
	 var_dump(new RecvH(3));`,
	// The engine hands a freed object's handle to the next `new`, and a program
	// can see that -- with no destructor anywhere. An object dropped by a call
	// that has returned is freed there, error or not.
	`class HandleP { public function __construct(public int $x = 0) {} }
	 var_dump(new HandleP);
	 $kept = new HandleP(1);
	 var_dump($kept);
	 var_dump(new HandleP(2));
	 var_dump($kept);
	 $obj = (object) [1, 2];
	 var_dump((array) (object) $obj);
	 var_dump($obj);
	 class HandleDtor { function __destruct() { throw new Error("gone"); } }
	 try { call_user_func(new HandleDtor); } catch (Throwable $t) { echo "caught ", $t->getMessage(), "\n"; }
	 echo "still running\n";
	 $f = new Fiber(function (): void {
	   try { echo "fiber\n"; Fiber::suspend(); } finally { echo "finally\n"; }
	 });
	 $f->start();
	 unset($f);
	 echo "after unset\n";`,
	// readonly names the class that *declared* the property, not the instance's
	// own, and protected(set) is measured against the first declaration exactly
	// as reading a protected property is.
	`abstract class RoP { public function __construct(public readonly int $v) {} }
	 class RoA extends RoP {}
	 class RoB extends RoP { static function poke(RoA $o) { $o->v = 5; } }
	 abstract class AsymP { protected(set) int $n = 0; }
	 class AsymA extends AsymP { protected(set) int $n = 1; }
	 class AsymB extends AsymP { static function set(AsymA $o) { $o->n = 7; return $o->n; } }
	 class AsymUnrelated { protected(set) int $n = 3; }
	 class AsymC extends AsymP { static function poke(AsymUnrelated $o) { $o->n = 8; } }
	 function visTry(string $label, callable $fn) {
	   echo "### $label\n";
	   try { var_dump($fn()); } catch (Throwable $t) { echo "! ", $t->getMessage(), "\n"; }
	 }
	 function visRoWrite()   { RoB::poke(new RoA(1)); }
	 function visRoRead()    { return (new RoA(2))->v; }
	 function visAsymSet()   { return AsymB::set(new AsymA); }
	 function visAsymOut()   { $o = new AsymA; $o->n = 9; return $o->n; }
	 function visAsymOther() { AsymC::poke(new AsymUnrelated); }
	 foreach (["readonly from sibling" => "visRoWrite", "readonly read" => "visRoRead",
	           "protected(set) sibling" => "visAsymSet", "protected(set) outside" => "visAsymOut",
	           "protected(set) stranger" => "visAsymOther"] as $l => $fn) {
	   visTry($l, $fn);
	 }`,
	// A protected member is measured against the *first* class to declare it,
	// not the last: redeclaring it in a subclass does not narrow who may see
	// it, so two children of one parent can reach each other's. A private
	// declaration in between ends the chain and starts a new scope.
	`abstract class ProtP { protected $shared = 0; protected static $st = 0; }
	 class ProtA extends ProtP { protected $shared = 1; protected $own = 9;
	                             protected static $st = 1; }
	 class ProtB extends ProtP {
	   protected $shared = 2;
	   protected static $st = 2;
	   static function shared($c) { return $c->shared; }
	   static function own($c) { return $c->own; }
	   static function stat() { return ProtA::$st; }
	 }
	 class ProtUnrelated { protected $shared = 3; protected static $st = 3; }
	 class PrivP { private $p = 1; }
	 class PrivA extends PrivP { protected $p = 2; }
	 class PrivB extends PrivP { protected $p = 3; static function read($c) { return $c->p; } }
	 function protTry(string $label, callable $fn) {
	   echo "### $label\n";
	   try { var_dump($fn()); } catch (Throwable $t) { echo "! ", $t->getMessage(), "\n"; }
	 }
	 function protSibling()   { return ProtB::shared(new ProtA); }
	 function protOwn()       { return ProtB::own(new ProtA); }
	 function protStatic()    { return ProtB::stat(); }
	 function protStranger()  { return ProtB::shared(new ProtUnrelated); }
	 function protPrivBreak() { return PrivB::read(new PrivA); }
	 foreach (["sibling" => "protSibling", "own" => "protOwn", "static" => "protStatic",
	           "stranger" => "protStranger", "private in between" => "protPrivBreak"] as $l => $fn) {
	   protTry($l, $fn);
	 }`,
	// An array literal is assembled in a temporary, and its elements are only
	// in it until it lands somewhere. A constructor running for a later element
	// must not see the earlier ones destroyed -- and the literal must not
	// outlive the statement that consumed it either.
	`class LitD {
	   public $n;
	   function __construct($n) { $this->n = $n; echo "ctor $n\n"; }
	   function __destruct() { echo "dtor {$this->n}\n"; }
	 }
	 function litBuild() {
	   echo "-- building\n";
	   $ar = [new LitD(1), new LitD(2), new LitD(3)];
	   echo "-- built\n";
	   unset($ar);
	   echo "-- unset\n";
	 }
	 function litTakes(array $a) { echo "-- inside\n"; }
	 function litPass() { litTakes([new LitD(4)]); echo "-- returned\n"; }
	 function litNest() { $n = [new LitD(5), [new LitD(6)]]; echo "-- nested\n"; unset($n); echo "-- gone\n"; }
	 litBuild();
	 litPass();
	 litNest();
	 echo "-- end\n";`,
	// A property default the compiler could not fold runs in a frame of its
	// own. A class constant does not get one, and a parameter default is
	// reported against the function it belongs to.
	`class CeStatic { public static $s = [CE_MISSING_A => 1]; }
	 class CeConst { const C = CE_MISSING_B; }
	 class CeProp { public $p = [CE_MISSING_C => 1]; }
	 function ceParam($x = [CE_MISSING_D => 1]) {}
	 function ceShow(Throwable $t) {
	   echo $t->getMessage(), "\n";
	   foreach ($t->getTrace() as $i => $frame) {
	     echo $i, ": ", $frame["function"], "\n";
	   }
	 }
	 function ceRun(string $label, callable $fn) {
	   echo "### $label\n";
	   try { $fn(); } catch (Throwable $t) { ceShow($t); }
	 }
	 function ceMakeStatic() { new CeStatic; }
	 function ceReadConst() { CeConst::C; }
	 function ceMakeProp() { new CeProp; }
	 function ceCallParam() { ceParam(); }
	 foreach (["static property" => "ceMakeStatic", "class constant" => "ceReadConst",
	           "instance property" => "ceMakeProp", "parameter default" => "ceCallParam"] as $l => $fn) {
	   ceRun($l, $fn);
	 }`,
	// array_unique's second argument picks the comparison, and the default is
	// SORT_STRING. SORT_REGULAR is the loose one, which is the only mode that
	// tells two objects of the same class apart by their contents.
	`enum UniqE: string { case A = "a"; case B = "b"; }
	 class UniqC { public $x; function __construct($x) { $this->x = $x; } }
	 var_dump(array_unique([UniqE::A, UniqE::B, UniqE::A], SORT_REGULAR));
	 var_dump(array_unique([new UniqC(1), new UniqC(2), new UniqC(1)], SORT_REGULAR));
	 var_dump(array_unique([1, "1", 2, "2.0", 2.0], SORT_REGULAR));
	 var_dump(array_unique(["10", "9", "1e1"], SORT_NUMERIC));
	 var_dump(array_unique([1, "1", 2, "2.0", 2.0], SORT_STRING));
	 var_dump(array_unique([1, "1", 2, 2, "a", "a"]));`,
	// A diagnostic raised part-way through a write can reach a user error
	// handler that throws, and then the write does not happen: the engine
	// abandons the opcode where the throw occurred rather than finishing it.
	// $GLOBALS is not an array access, so a missing name there is reported in
	// the global wording -- and the read-modify-write form reads first.
	`set_error_handler(function ($n, $m) { throw new Exception($m); });
	 $arr = [];
	 try { $arr[0] .= "xyz"; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
	 var_dump($arr);
	 try { $arr["key"] .= "xyz"; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
	 var_dump($arr);
	 class UndefObj {}
	 $o = new UndefObj;
	 try { $o->missing .= "xyz"; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
	 var_dump($o);
	 try { $GLOBALS["nowhere"] .= "xyz"; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
	 restore_error_handler();
	 $GLOBALS["somewhere"] = "set";
	 var_dump($GLOBALS["somewhere"]);`,
	// The engine's tables are insertion-ordered and that order is reported:
	// classes, interfaces and functions all come back in declaration order,
	// never alphabetised.
	`class OrdZeta {}
	 class OrdAlpha {}
	 interface OrdIface {}
	 class OrdMid implements OrdIface {}
	 function ordZfunc() {}
	 function ordAfunc() {}
	 print_r(array_slice(get_declared_classes(), -3));
	 print_r(array_slice(get_declared_interfaces(), -1));
	 print_r(array_values(array_filter(get_defined_functions()["user"],
	   fn($n) => str_starts_with($n, "ord"))));`,
	// get_class_methods lists what the calling scope can reach, in the order
	// the methods were declared -- the constructor is not special and does not
	// come first.
	`class GcmX {
	   public function zz() {}
	   public function aa() {}
	   protected function prot() {}
	   private function priv() {}
	   public function __construct() {}
	   static public function stat() {}
	 }
	 class GcmY extends GcmX {
	   private function own() {}
	   static public function inside() {
	     var_dump(get_class_methods("GcmX"), get_class_methods("GcmY"));
	   }
	 }
	 var_dump(get_class_methods("GcmX"), get_class_methods("GcmY"));
	 GcmY::inside();
	 var_dump(get_class_methods(new GcmX));`,
	// `@` restores the level it saved only when the script has not raised it
	// meanwhile, and "raised" is measured against the levels nothing can hide:
	// error_reporting(0) inside a suppressed call is restored afterwards,
	// error_reporting(E_ALL) is not. An exception on the way out changes
	// neither answer.
	`error_reporting(E_ALL);
	 function silencePeek() { return error_reporting(); }
	 var_dump(@silencePeek());
	 function silenceZero() { error_reporting(0); }
	 @silenceZero(); var_dump(error_reporting());
	 error_reporting(E_ALL);
	 function silenceRaise() { error_reporting(E_ALL & ~E_NOTICE); }
	 @silenceRaise(); var_dump(error_reporting());
	 error_reporting(E_ALL);
	 function silenceZeroThrow() { error_reporting(0); throw new Exception(); }
	 try { @silenceZeroThrow(); } catch (Exception $e) {} var_dump(error_reporting());
	 error_reporting(E_ALL);
	 function silenceRaiseThrow() { error_reporting(E_ALL & ~E_NOTICE); throw new Exception(); }
	 try { @silenceRaiseThrow(); } catch (Exception $e) {} var_dump(error_reporting());`,
	// A class with __toString implements Stringable whether it said so or not,
	// including when the method comes from a trait -- which the engine only
	// settles at link time, after the compiler has been and gone.
	`trait StrT { public function __toString(): string { return "ok"; } }
	 trait StrT2 { use StrT; }
	 class StrC { use StrT; }
	 class StrC2 { use StrT2; }
	 class StrD { public function __toString(): string { return "d"; } }
	 class StrE extends StrD {}
	 foreach (["StrC", "StrC2", "StrD", "StrE", "StrT"] as $name) {
	   echo "### $name\n";
	   var_dump(is_subclass_of($name, "Stringable", true), class_implements($name));
	 }
	 var_dump(new StrC instanceof Stringable, new StrC2 instanceof Stringable);`,
	// `$o->p = &$v` is an expression, and it answers with the property. The
	// case where the old value's destructor writes through the very box being
	// bound lives in Zend/tests/gh10168 -- the engine will not eval it.
	`class RefBox { public $value; public ?int $typed = null; }
	 $o = new RefBox(); $v = 7; $w = "8";
	 var_dump($o->value = &$v);
	 var_dump($o->typed = &$w);
	 $u = new RefBox();
	 var_dump($u->missing = &$v);
	 var_dump($o, $u, $v, $w);`,
	// `@` is not a flag beside error_reporting -- it *is* error_reporting,
	// dropped to the levels nothing can hide. So a call to error_reporting()
	// inside a suppressed expression un-suppresses the rest of it, and the
	// silence is not restored on the way out.
	`error_reporting(E_ALL & ~E_DEPRECATED);
	 function silence1() { echo $s1; error_reporting(E_ALL); echo $s2; }
	 echo "### changed inside\n";
	 @silence1();
	 var_dump(error_reporting());
	 echo "### unchanged inside\n";
	 function silence2() { echo $s3; }
	 @silence2();
	 var_dump(error_reporting());
	 echo "### thrown out of\n";
	 function silence3() { echo $s4; throw new Exception("x"); }
	 try { @silence3(); } catch (Throwable $t) {}
	 var_dump(error_reporting());
	 echo $s5;`,
	// Binding a reference to a value the program cannot name is legal and
	// pointless -- the box has nowhere to write back to -- and PHP says so.
	// Such a box has one name once bound, so it is not one var_dump marks.
	`function refByVal() { return 2; }
	 function refByRef() { static $x = 1; return $x; }
	 function refTaker(&$v) { $v = 9; }
	 class RefHolder { public $p; }
	 echo "### variable, by value\n";      $a = &refByVal();
	 echo "### variable, by reference\n";  $b = &refByRef();
	 echo "### property\n";                $o = new RefHolder; $o->p = &refByVal(); var_dump($o);
	 echo "### element\n";                 $c = [1]; $c[0] = &refByVal(); var_dump($c);
	 echo "### passed, by value\n";        refTaker(refByVal());
	 echo "### passed, by reference\n";    refTaker(refByRef());
	 echo "### a real second name\n";      $d = 1; $e = [&$d]; var_dump($e);
	 echo "### prefer-ref takes a value\n"; extract(refByVal() === 2 ? ["k" => 1] : []); var_dump($k);`,
	// A callback the engine invokes has its own stack frame. Called from inside
	// an internal function it has no file and no line, which is what
	// `[internal function]` means; an error handler is called *at* the
	// diagnostic, so its frame names that place instead. The file each frame
	// names is left out -- the harness runs from a temporary path -- but which
	// frames exist, and which of them have a file at all, is the whole point.
	`function traceBoom() { throw new Exception("B"); }
	 function showTrace(Throwable $t) {
	   foreach ($t->getTrace() as $i => $frame) {
	     echo $i, ": ", $frame["function"], " args=", count($frame["args"] ?? []),
	          " file=", isset($frame["file"]) ? "yes" : "no", "\n";
	   }
	 }
	 function viaMap()  { array_map("traceBoom", [1]); }
	 function viaWalk() { $a = [1]; array_walk($a, "traceBoom"); }
	 function viaCuf()  { call_user_func("traceBoom"); }
	 foreach (["viaMap", "viaWalk", "viaCuf"] as $entry) {
	   echo "### $entry\n";
	   try { $entry(); } catch (Throwable $t) { showTrace($t); }
	 }`,
	// An exception thrown inside an error handler carries the handler's own
	// frame, and that frame does name a place in the script.
	`function throwingHandler($n, $m) { throw new Exception("H"); }
	 set_error_handler("throwingHandler");
	 function handlerBoom() { echo $undefinedHere; }
	 try { handlerBoom(); }
	 catch (Throwable $t) {
	   echo get_class($t), ": ", $t->getMessage(), "\n";
	   foreach ($t->getTrace() as $i => $frame) {
	     echo $i, ": ", $frame["function"], " args=", count($frame["args"] ?? []),
	          " file=", isset($frame["file"]) ? "yes" : "no", "\n";
	   }
	 }`,
	// Unsetting an offset of something that is not a container removes nothing,
	// but the engine does not let every shape pass in silence -- and the nested
	// form creates nothing on the way down, so a null stays null.
	`set_error_handler(function ($c, $m) { echo "E: $m\n"; return true; });
	 foreach ([null, false, true, 1, 3.14, "", "s", [], [[1]], new stdClass] as $i => $v) {
	   foreach ([1, 2] as $depth) {
	     echo "### $i ", gettype($v), " depth $depth\n";
	     $x = $v;
	     try {
	       if ($depth === 1) { unset($x[0]); } else { unset($x[0][0]); }
	     } catch (Throwable $t) { echo "! ", $t->getMessage(), "\n"; }
	     var_dump($x);
	   }
	 }
	 unset($gone); unset($gone[0]);
	 unset($gone2); unset($gone2[0][0]);`,
	// Which refusal a string offset earns depends on the *outer* index: a legal
	// one picks out a byte and the complaint is that a byte is not an array,
	// while an illegal one never gets that far.
	`$s = "test";
	 foreach ([["a", "b"], [0, 0], [0, "b"], ["a", 0], [1, 1], ["1", "1"]] as $pair) {
	   $x = $s;
	   try { unset($x[$pair[0]][$pair[1]]); }
	   catch (Throwable $t) { echo json_encode($pair), " -> ", $t->getMessage(), "\n"; }
	 }`,
	// Resolving a callback can raise a diagnostic, and a user error handler can
	// turn that into a throw. The resolution never finishes, so call_user_func
	// reports the empty reason it started with and the throw becomes the cause.
	`set_error_handler(function () { throw new Exception("H"); });
	 class CbSelf {
	   public static function y() { echo "y\n"; }
	   public function test() { call_user_func("self::y"); }
	 }
	 try { (new CbSelf)->test(); }
	 catch (Throwable $t) {
	   echo get_class($t), ": ", $t->getMessage(), "\n";
	   echo "previous: ", get_class($t->getPrevious()), ": ", $t->getPrevious()->getMessage(), "\n";
	 }`,
	// A float too large for an int64 wraps modulo 2^64 rather than collapsing
	// to zero or saturating -- the engine reduces it and reads the result as
	// signed. A float that came from a *string* is the one that saturates.
	`var_dump((int)1e30, (int)-1e30, (int)(PHP_INT_MAX+1), (int)NAN, (int)INF,
	          (int)1e19, (int)-1e19, (int)9.9e18, (int)1.5, (int)-1.5,
	          (int)"1.0E+121", intval(1e30), intdiv(7, 2), 1 % 1.5);`,
	// The same conversion decides whether an array key lost precision, and the
	// number in that message is spelled at full precision -- the way var_dump
	// spells one, not the way echo does.
	`set_error_handler(function ($c, $m) { echo "E: $m\n"; return true; });
	 $a = [];
	 foreach ([PHP_INT_MAX + 1, 1e30, -1e30, NAN, INF, 1.0, 2.5, 0.1, 1e100] as $k) {
	   $a[$k];
	 }
	 $b = []; $b[1.7] = 1; $b[2.0] = 2; var_dump($b);`,
	// A property with a get hook is *read* before a reference binding to it is
	// refused; one with only a set hook has nothing to read and says something
	// else entirely.
	`class HookP1 { public $p = 0 { &get { echo "amp-get\n"; return $this->p; } } }
	 class HookP2 { public $p = 0 { get { echo "plain-get\n"; return $this->p; } } }
	 class HookP3 { public $p = 0 { set { echo "set-only\n"; $this->p = $value; } } }
	 $r = 1;
	 foreach (["HookP1", "HookP2", "HookP3"] as $class) {
	   echo "### $class\n";
	   $o = new $class;
	   try { $o->p = &$r; } catch (Throwable $t) { echo "! ", $t->getMessage(), "\n"; }
	 }`,
	// A static's box always holds a value, so an initializer that read
	// something absent warns once and stores null -- later reads of the name
	// are silent, and later calls skip the initializer entirely.
	`function staticSelf() { static $a = $a; var_dump($a); $b = $a; }
	 staticSelf(); staticSelf();
	 function staticBare() { static $c; var_dump($c); $c = 1; }
	 staticBare(); staticBare();`,
	// Every way the enum form of a serialized value can go wrong is reported
	// before the offset is, and *where* it is reported from differs: a name the
	// reader cannot make sense of fails at the value's own start, one it
	// resolved and then found no case for fails at the end of what it read.
	`enum SerE { case A; const K = SerE::A; }
	 class SerNotEnum {}
	 set_error_handler(function ($n, $m) {
	   echo "W: ", str_replace(["PHP Request Startup: ", "unserialize(): "], "", $m), "\n";
	   return true;
	 });
	 foreach (['E:8:"Nope:Bar";', 'E:14:"SerNotEnum:Bar";', 'E:11:"SerNotEnum:Bar";', 's:3:"abcd";', 's:2:"ab"x', 'E:7:"SerEFoo";',
	           'E:6:"SerE:K";', 'E:6:"SerE:Z";', 'E:6:"SerE:A";'] as $text) {
	   echo "### ", $text, " (", strlen($text), " bytes)\n";
	   var_dump(unserialize($text));
	 }`,
	// Running out of input is a different failure from finding a byte that
	// cannot be read: the first is blamed on the end of the text, the second on
	// where the value it belongs to started. A count larger than the braces
	// hold is a third, and says so outright.
	`set_error_handler(function ($n, $m) {
	   echo "W: ", str_replace(["PHP Request Startup: ", "unserialize(): "], "", $m), "\n";
	   return true;
	 });
	 foreach (['a:2:{i:0;O:9:"000000000":10000000', 'a:2:{i:0;i:1;', 'a:1:{i:0;x:1;}',
	           'i:1;junk', 'a:3:{i:0;i:1;}', 's:5:"ab";', 'O:3:"Foo"', 'i:1;'] as $text) {
	   echo "### ", $text, " (", strlen($text), " bytes)\n";
	   var_dump(unserialize($text));
	 }`,
	// `self`, `parent` and `static` in a callable forward the late static
	// binding; naming the class outright does not. And the deprecation is for a
	// keyword that resolved -- one that names nothing is not a callable at all,
	// so there is nothing to call deprecated.
	`class LsbA { public static function who() { echo get_called_class(), "\n"; } }
	 class LsbB extends LsbA {
	   public static function forwards() {
	     call_user_func("parent::who");
	     call_user_func(["parent", "who"]);
	     call_user_func("self::who");
	     call_user_func(["static", "who"]);
	   }
	   public static function doesNot() {
	     call_user_func(["LsbB", "parent::who"]);
	     call_user_func("LsbA::who");
	   }
	 }
	 class LsbC extends LsbB {}
	 LsbC::forwards();
	 LsbC::doesNot();
	 class NoParent { public function h() { var_dump(is_callable(["parent", "h"])); } }
	 (new NoParent)->h();
	 function lsbFree() { var_dump(is_callable(["self", "x"])); }
	 lsbFree();`,
	// Rebinding a typed property to a different box takes the type off the old
	// one: a name still holding it is an ordinary reference from there on.
	`class RebindT { public int $x = 0; public $y = 1;
	                 public static int $sx = 0; public static $sy = 1; }
	 $probe = [
	   'instance' => function () { $t = new RebindT; $r = &$t->x; $t->x = &$t->y;
	                               $r = "foo"; return [$r, $t->x, $t->y]; },
	   'static'   => function () { $r = &RebindT::$sx; RebindT::$sx = &RebindT::$sy;
	                               $r = "foo"; return [$r, RebindT::$sx]; },
	   'still typed' => function () { $t = new RebindT; $r = &$t->x; $r = "z"; },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "### $label\n";
	   try { var_dump($fn()); } catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// `$a[] .= x` appends, and the element it appends to is new -- there is no
	// key to have missed, so nothing is reported and the read half sees null.
	// A false that grows into an array is deprecated whichever form does it.
	`$probe = [
	   'append concat'  => function () { $a = [1]; $a[] .= "x"; return $a; },
	   'append add'     => function () { $a = [5]; $a[] += 3; return $a; },
	   'false appends'  => function () { $a = false; $a[] = 1; return $a; },
	   'false keyed'    => function () { $a = false; $a["k"] = 2; return $a; },
	   'false compound' => function () { $a = false; $b = 42; $a[] &= $b; return $a; },
	   'null appends'   => function () { $a = null; $a[] = 4; return $a; },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "### $label\n";
	   try { var_dump($fn()); } catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A `callable` type hint resolves the callable rather than merely checking
	// its shape, so it reports the same deprecations call_user_func would.
	`class CbA { public static function foo() {} }
	 class CbB extends CbA {
	   public function takes(callable $c) {}
	   public function run() {
	     foreach (['self::foo', ['self', 'foo'], ['parent', 'foo'], ['static', 'foo'],
	               ['CbB', 'self::foo'], [$this, 'CbA::foo'], 'CbA::foo'] as $c) {
	       $this->takes($c);
	     }
	   }
	 }
	 (new CbB)->run();
	 echo "done\n";`,
	// A reference bound to a typed property carries the property's type, and
	// the two wordings are not interchangeable: making the binding reports a
	// write to the property, writing through an established one reports a write
	// to the reference the property holds.
	`class RefT { public int $i = 0; public static int $s = 0; }
	 $probe = [
	   'bind bad, instance' => function () { $x = "q"; $o = new RefT; $o->i = &$x; },
	   'bind bad, static'   => function () { $x = "q"; RefT::$s = &$x; },
	   'bind coerces'       => function () { $y = "7"; $o = new RefT; $o->i = &$y; return [$o->i, $y]; },
	   'write through, obj' => function () { $y = "7"; $o = new RefT; $o->i = &$y; $y = "z"; },
	   'write through, sta' => function () { $y = "7"; RefT::$s = &$y; $y = "z"; },
	   'foreach by ref'     => function () { $o = new RefT; foreach ($o as $k => &$v) { $v = []; } },
	   'dumped as a ref'    => function () { $o = new RefT; return [&$o->i]; },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "### $label\n";
	   try { var_dump($fn()); } catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A typed static property with no default holds nothing, exactly as an
	// instance one does -- but the engine names it as a *static* property when
	// it refuses the read, and isset() still answers without raising. Handing
	// one out by reference is refused too, unless the type takes null.
	`class StaticT { public static int $s; public static ?int $n; public static $u;
	                 public int $i; public ?int $m; }
	 $probe = [
	   'isset'        => fn() => [isset(StaticT::$s), empty(StaticT::$s), StaticT::$u],
	   'read'         => fn() => StaticT::$s,
	   'in an expr'   => fn() => StaticT::$s + 1,
	   'step'         => function () { return StaticT::$s++; },
	   'by reference' => function () { $r = &StaticT::$s; return $r; },
	   'nullable ref' => function () { $r = &StaticT::$n; return [$r, StaticT::$n]; },
	   'instance ref' => function () { $o = new StaticT; $r = &$o->i; return $r; },
	   'nullable inst'=> function () { $o = new StaticT; $r = &$o->m; return [$r, $o]; },
	   'then assign'  => function () { StaticT::$s = 3; return StaticT::$s; },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "### $label\n";
	   try { var_dump($fn()); } catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A read-modify-write reads before it writes, so an uninitialized typed
	// property has no value to step from: the engine reports that rather than
	// stepping from null. unset() is what hands the name to __get instead.
	`class RmwC { public int $c = 1; public $d; }
	 class RmwD { public int $c; public function __get($n) { echo "get $n\n"; return 5; }
	              public function __set($n, $v) { echo "set $n=", var_export($v, true), "\n"; } }
	 $probe = [
	   'never assigned' => function () { $o = new RmwC; unset($o->c); return $o->c++; },
	   'pre-increment'  => function () { $o = new RmwC; unset($o->c); return ++$o->c; },
	   'compound'       => function () { $o = new RmwC; unset($o->c); return $o->c .= "x"; },
	   'declared, magic'=> function () { $o = new RmwD; return $o->c++; },
	   'unset, magic'   => function () { $o = new RmwD; unset($o->c); return $o->c++; },
	   'untyped'        => function () { $o = new RmwC; return $o->d++; },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "### $label\n";
	   try { var_dump($fn()); } catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// Every route that creates a property the class never declared earns the
	// 8.2 deprecation, not just a plain assignment: a step, a compound
	// assignment and a write-mode descent all make one.
	`class DynP {}
	 #[AllowDynamicProperties] class DynQ {}
	 foreach (["DynP", "DynQ", "stdClass"] as $class) {
	   echo "### $class\n";
	   $a = new $class; $a->n++; $a->n++;
	   $b = new $class; $b->n .= "z";
	   $c = new $class; $c->n[] = 1; $c->n[] = 2;
	   $d = new $class; $r = &$d->n;
	   $e = new $class; unset($e->n["k"]);
	   var_dump($a, $b, $c, $d, $e);
	 }`,
	// A class that declares no properties has nothing to defer, so making it
	// lazy hands back an ordinary object: no initializer runs and var_dump
	// prints no label. An initialized proxy's state is the instance's, which
	// is what (array) and get_mangled_object_vars read without initializing.
	`class LazyNone {} class LazySub extends stdClass {}
	 class LazyHeld { public int $a; public function __construct() { $this->a = 1; } }
	 foreach (["LazyNone", "LazySub"] as $class) {
	   $r = new ReflectionClass($class);
	   var_dump($r->newLazyGhost(function ($o) { echo "ghost initializer\n"; }));
	   var_dump($r->newLazyProxy(function ($o) use ($class) { echo "proxy initializer\n"; return new $class; }));
	 }
	 $r = new ReflectionClass(LazyHeld::class);
	 $p = $r->newLazyProxy(fn($o) => new LazyHeld);
	 var_dump((array)$p, get_mangled_object_vars($p));
	 $p->a = 7;
	 var_dump((array)$p, get_mangled_object_vars($p));`,
	// INF, -INF and NAN print in capitals everywhere except printf, which
	// spells the last one "NaN"; both spellings are PHP's own.
	`foreach ([INF, -INF, NAN, 1.0] as $f) {
	   var_dump($f); echo (string)$f, "\n"; var_export($f); echo "\n";
	   printf("%s|%f|%e|%d\n", $f, $f, $f, $f);
	   var_dump((int)$f, (bool)$f, json_encode([$f]));
	 }`,
	// Natural order: a leading zero at the very start of the string is skipped,
	// but one anywhere else makes its digit run compare left to right.
	`foreach ([["x01","x1"],["0001","1"],["img12","img10"],["img2","img10"],
	           ["a1","a01"],["1.5","1.10"],["a 1","a1"],["v1.0","v1.0.1"]] as $p) {
	   var_dump(strnatcmp($p[0], $p[1]), strnatcasecmp($p[0], $p[1]));
	 }`,
	`interface I1 {} interface I2 extends I1 {}
trait T1 {} trait T2 {}
class A implements I2 { use T1; }
class B extends A { use T2; }
abstract class C {} final class D {}
enum E { case X; }

echo "### declared_traits\n";
var_dump(get_declared_traits());
echo "### declared_classes tail\n";
$c = get_declared_classes();
var_dump(array_slice($c, -5));
echo "### declared_interfaces has\n";
$i = get_declared_interfaces();
var_dump(in_array("I1",$i), in_array("I2",$i), in_array("Traversable",$i));
echo "### spl_object_id\n";
$a = new A; $b = new B;
var_dump(spl_object_id($a) > 0, spl_object_id($a) === spl_object_id($a), spl_object_id($a) !== spl_object_id($b));
var_dump(strlen(spl_object_hash($a)), spl_object_hash($a) === spl_object_hash($a));
echo "### spl_object_id error\n";
try { spl_object_id("x"); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
echo "### is_subclass_of\n";
var_dump(is_subclass_of($b,"A"), is_subclass_of($a,"A"), is_subclass_of($a,"I1"),
         is_subclass_of("B","A"), is_subclass_of("B","A",false), is_subclass_of($a,"Nope"));
echo "### is_a\n";
var_dump(is_a($a,"A"), is_a($a,"I1"), is_a("A","A"), is_a("A","A",true), is_a($b,"B"));
echo "### class_implements\n";
var_dump(class_implements("A"));
echo "### class_parents\n";
var_dump(class_parents($b), class_parents("A"));
try { class_parents(1); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), chr(10); }
echo "### class_uses\n";
var_dump(class_uses($b), class_uses("A"));
echo "### defined_functions\n";
function myfn() {}
$d = get_defined_functions();
var_dump(array_keys($d), in_array("myfn", $d["user"]), in_array("strlen", $d["internal"]));`,
	`echo "### defined_vars-fn\n";
function f($a, $b) { $c = 3; var_dump(get_defined_vars()); $d = 4; }
f(1, 2);
echo "### defined_vars-global\n";
$g1 = 1; $g2 = "x";
$vars = get_defined_vars();
var_dump(isset($vars["g1"]), isset($vars["g2"]), $vars["g1"]);
echo "### extract\n";
function e() { $a = 0; $b = 0; var_dump(extract(["a"=>1,"b"=>2]), $a, $b); }
e();
echo "### extract-skip\n";
function es() { $a = 9; $b = 0; var_dump(extract(["a"=>1,"b"=>2], EXTR_SKIP), $a, $b); }
es();
echo "### extract-if-exists\n";
function ee() { $a = 9; var_dump(extract(["a"=>1,"zz"=>2], EXTR_IF_EXISTS), $a); }
ee();
echo "### extract-prefix\n";
function ep() { $p_a = 0; $a = 9; var_dump(extract(["a"=>1], EXTR_PREFIX_ALL, "p"), $p_a, $a); }
ep();
echo "### extract-type\n";
function et() { try { extract("x"); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; } }
et();
echo "### extract-invalid\n";
function ei() { var_dump(extract(["1bad"=>1, "ok"=>2] + ["ok"=>2])); }
ei();

echo "### extract-newname";
echo chr(10);
function en() { var_dump(extract(["fresh"=>7])); $n = "fresh"; var_dump($$n, isset($$n), get_defined_vars()); }
en();
echo "### varvar";
echo chr(10);
function vv() { $n = "made"; $$n = 5; var_dump($$n, isset($$n), compact("made")); unset($$n); var_dump(isset($$n)); }
vv();`,
	`$base = ob_get_level();
$r = [];
ob_start(); echo "a"; $r['clean'] = ob_clean(); $r['lvl'] = ob_get_level() - $base;
$r['after'] = ob_get_contents(); echo "b"; $r['got'] = ob_get_clean();
ob_start(); ob_start(); echo "inner"; $r['flush'] = ob_flush();
$r['lvl2'] = ob_get_level() - $base; $r['after2'] = ob_get_contents();
$r['inner'] = ob_get_clean(); $r['outer'] = ob_get_clean();
$r['lvl3'] = ob_get_level() - $base;
echo "### all\n"; var_dump($r);`,
	`echo "### reduce\n";
var_dump(array_reduce([1,2,3], fn($c,$v) => $c + $v),
         array_reduce([1,2,3], fn($c,$v) => $c + $v, 10),
         array_reduce([], fn($c,$v) => $c + $v),
         array_reduce(["a","b"], fn($c,$v) => $c . $v, ""));
echo "### walk\n";
$a = [1,2,3];
var_dump(array_walk($a, function(&$v, $k) { $v = $v * 10 + $k; }), $a);
$b = ["x"=>1,"y"=>2];
var_dump(array_walk($b, function(&$v, $k, $extra) { $v = "$k:$v:$extra"; }, "E"), $b);
echo "### walk-novalue\n";
$c = [1,2];
var_dump(array_walk($c, function($v, $k) { }), $c);
echo "### walk-type\n";
try { $s = "no"; array_walk($s, fn($v) => $v); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
echo "### walk-throw\n";
$d = [1,2,3];
try { array_walk($d, function(&$v,$k) { if ($k == 1) throw new Exception("stop"); $v = 9; }); }
catch (Throwable $t) { echo $t->getMessage(), "\n"; }
var_dump($d);
echo "### reduce-throw\n";
try { array_reduce([1,2], function($c,$v) { throw new Exception("r"); }); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }`,
	`function t() {
  $x = 1;
  foreach ([["extract",["a"=>1]],["compact","x"],["get_defined_vars",null],["func_get_args",null],
            ["func_num_args",null],["func_get_arg",0]] as [$name,$arg]) {
    echo "### $name\n";
    $f = $name;
    try { $arg === null ? $f() : $f($arg); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
    try { $arg === null ? call_user_func($name) : call_user_func($name, $arg); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
  }
}
t();
echo "### this_in_extract\n";
class C { public $p = 1; function m() { try { var_dump(extract(["this"=>1,"a"=>2])); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; } var_dump($a ?? "unset"); } }
(new C)->m();
echo "### extract-this-global\n";
function g() { var_dump(extract(["this"=>5])); }
g();
echo "### called_class\n";
class P { static function who() { return get_called_class(); } function w2() { return get_called_class(); } }
class Q extends P {}
var_dump(P::who(), Q::who(), (new Q)->w2());
try { get_called_class(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
echo "### parent_class\n";
var_dump(get_parent_class(new Q), get_parent_class("Q"), get_parent_class(new P));
class R extends P { function pc() { return get_parent_class(); } }
var_dump((new R)->pc());`,
	`function a() { $a = 0; $b = 0; try { var_dump(extract(["a"=>2,"this"=>1,"b"=>3])); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; } var_dump($a, $b); }
echo "### order\n"; a();
class C { function m() { $x = 0; try { var_dump(extract(["x"=>9,"this"=>1])); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; } var_dump($x); } }
echo "### inclass\n"; (new C)->m();
echo "### global\n";
function g() { try { var_dump(extract(["this"=>5])); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; } }
g();`,
	// strcasecmp and strncasecmp are PHP's own loop and return the *difference*
	// between the first pair of folded bytes that disagree, not its sign. Only
	// a common prefix falls back to a three-way length comparison. strcmp goes
	// through memcmp instead, whose magnitude is the C library's business, so
	// it is not covered here.
	`$pairs = [["a","d"],["d","a"],["abc","abd"],["abc","abc"],["a","ab"],["ab","a"],
	           ["A","a"],["",""],["","a"],["Z","a"],["abcd","abZZ"],["aef","dfsgbdf"],
	           ["qwe","qwer"],["qwErtY","qwerty"]];
	 foreach ($pairs as [$x, $y]) {
	   var_dump(strcasecmp($x,$y), strncasecmp($x,$y,2), strnatcmp($x,$y),
	            substr_compare($x,$y,0,null,true));
	 }
	 foreach ([["strncmp",-1],["strncasecmp",-1]] as [$fn,$len]) {
	   try { $fn("a","b",$len); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }
	 }
	 foreach ([fn() => substr_count("abc",""), fn() => str_split("abc",0),
	           fn() => chunk_split("abc",0), fn() => wordwrap("abc",5,""),
	           fn() => wordwrap("abc",0,"\n",true), fn() => array_chunk([1],0),
	           fn() => array_combine([1],[1,2]), fn() => stripos("abc","a",99),
	           fn() => random_int(5,1), fn() => array_is_list("x"),
	           fn() => substr_compare("a","b",5), fn() => substr_compare("abc","b",0,-1)] as $try) {
	   try { $try(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// #[\Override] is checked when the class is linked. An abstract trait
	// method, an interface method and a protected parent method are all things
	// to override; a concrete trait method and a private parent method are not.
	// The failing cases are `.phpt` material: a link-time fatal bails the
	// engine out before php2eval can capture anything.
	`trait A1 { public abstract function t(): void; }
	 class C1 { use A1; #[\Override] public function t(): void {} }
	 interface I1 { public function i(): void; }
	 class C2 implements I1 { #[\Override] public function i(): void {} }
	 class P3 { protected function p(): void {} }
	 class C3 extends P3 { #[\Override] public function p(): void {} }
	 abstract class P4 { abstract public function __construct(); }
	 trait T4 { #[\Override] public function __construct() {} }
	 class C4 extends P4 { use T4; }
	 class C5 implements IteratorAggregate {
	   #[\Override] public function getIterator(): Traversable { yield from []; } }
	 // An ancestor's implementation may itself have come from a trait, which
	 // still satisfies an abstract method a later trait introduces.
	 trait T6 { public abstract function test(); }
	 trait T7 { public function test() {} }
	 class A7 { use T7; }
	 class B7 extends A7 { use T6; }
	 echo "ok\n";`,
	// A #[\Deprecated] message that names its own constant reports it once
	// rather than forever, and defined() does not report at all -- it asks
	// whether the constant exists, not for its value.
	//
	// A message naming a *different* deprecated constant reports that one too,
	// and first; it is left out here because the engine attributes it to the
	// inner constant's declaration line rather than to the read site, and the
	// harness compares line numbers. The `.phpt` cases cover it.
	`class Clazz {
	   #[\Deprecated(self::TEST)] public const TEST = "from itself";
	   #[\Deprecated] public const TEST4 = 4;
	   #[\Deprecated] function __call(string $n, array $a) { return "called $n"; }
	   #[\Deprecated("due to some reason")]
	   static function __callStatic(string $n, array $a) { return "static $n"; }
	 }
	 Clazz::TEST;
	 var_dump(defined('Clazz::TEST4'), constant('Clazz::TEST4'));
	 // __call and __callStatic are reported against the name the program wrote.
	 $c = new Clazz();
	 var_dump($c->anything(), Clazz::other());
	 // A callback is a call, so the deprecation is reported there too.
	 #[\Deprecated("via callback")] function cbfn($x) { return $x; }
	 var_dump(array_map('cbfn', [1]));
	 var_dump(call_user_func('cbfn', 2));`,
	// The link-time checks are mostly negative -- ambiguous inherited
	// constants, narrowed access, a bare Traversable, an enum with state -- and
	// each of those is a fatal the engine bails out on before php2eval can
	// capture anything, so they are `.phpt` material. What belongs here is the
	// other side: every legal shape a too-eager check would refuse.
	`echo "### constants\n";
interface I1 { const X = 1; public const Y = 2; }
interface I2 extends I1 {}
class C1 implements I1, I2 { const X = 99; }
var_dump(C1::X, C1::Y);
class P1 { const Z = 5; }
class C2 extends P1 implements I1 { const Z = 6; }
var_dump(C2::Z, C2::X);
echo "### visibility\n";
interface IV { public function m(); }
abstract class AV { protected function p() {} private function q() {} }
class CV extends AV implements IV { public function m() {} public function p() {} public function q() {} }
var_dump((new CV) instanceof IV);
echo "### traversable\n";
abstract class AT implements Traversable {}
interface IT extends Traversable {}
class CT extends AT implements Iterator {
  public function current(): mixed { return 1; } public function key(): mixed { return 0; }
  public function next(): void {} public function rewind(): void {} public function valid(): bool { return false; }
}
var_dump((new CT) instanceof Traversable);
class CA implements IteratorAggregate { public function getIterator(): Traversable { yield 1; } }
foreach (new CA as $v) { var_dump($v); }
echo "### enum\n";
enum E1: string { case A = 'a'; const K = 'k'; public static function make(): self { return self::A; }
  public function label(): string { return $this->name; } }
var_dump(E1::A, E1::make(), E1::A->label(), E1::K, E1::from('a'), E1::A instanceof BackedEnum);
enum E2 { case B; use TE; }
trait TE { public function __invoke() { return "inv"; } public static function st() { return "st"; } }
var_dump(E2::B, (E2::B)(), E2::st(), E2::B instanceof UnitEnum);
echo "### instantiate\n";
foreach (['E1', 'IV', 'AV', 'TE'] as $n) {
  try { new $n(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
}
echo "### end\n";`,
	// Fibers. A generator suspends at a `yield` in its own body; a fiber
	// suspends wherever it happens to be, however deep, which is why it is a
	// goroutine with a strict handoff rather than a saved program counter.
	`echo "### roundtrip\n";
$f = new Fiber(function (string $a, int $b): string {
  echo "in: $a $b\n";
  $x = Fiber::suspend("s1");
  echo "resumed: $x\n";
  $y = Fiber::suspend("s2");
  echo "resumed: $y\n";
  return "returned";
});
var_dump($f->isStarted(), $f->isSuspended(), $f->isRunning(), $f->isTerminated());
var_dump($f->start("hello", 7));
var_dump($f->isStarted(), $f->isSuspended(), $f->isTerminated());
var_dump($f->resume("r1"));
var_dump($f->resume("r2"));
var_dump($f->isTerminated(), $f->isSuspended(), $f->getReturn());
echo "### deep\n";
function deep(int $n) { if ($n === 0) { return Fiber::suspend("bottom"); } return deep($n - 1) . "|$n"; }
$g = new Fiber(fn() => deep(4));
var_dump($g->start(), $g->resume("up"), $g->getReturn());
echo "### nested\n";
$outer = new Fiber(function () {
  $inner = new Fiber(function () { Fiber::suspend("inner"); return "inner-done"; });
  var_dump($inner->start());
  var_dump(Fiber::getCurrent() !== null);
  Fiber::suspend("outer");
  var_dump($inner->resume(null), $inner->getReturn());
  return "outer-done";
});
var_dump($outer->start());
var_dump($outer->resume(null), $outer->getReturn());
echo "### throw-in\n";
$h = new Fiber(function () {
  try { Fiber::suspend("wait"); } catch (Exception $e) { echo "caught: ", $e->getMessage(), "\n"; return "after"; }
  return "no";
});
var_dump($h->start());
var_dump($h->throw(new Exception("injected")), $h->getReturn());
echo "### throw-out\n";
$i = new Fiber(function () { Fiber::suspend("x"); throw new RuntimeException("escaped"); });
$i->start();
try { $i->resume(null); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
var_dump($i->isTerminated());
try { $i->getReturn(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
echo "### errors\n";
try { Fiber::suspend(1); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
$j = new Fiber(fn() => 1);
try { $j->resume(1); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
try { $j->getReturn(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
$j->start();
try { $j->start(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
var_dump($j->getReturn());
try { new FiberError; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
var_dump(Fiber::getCurrent());
echo "### object-callable\n";
class Runner { public function __invoke($v) { return Fiber::suspend($v) . "!"; } }
$k = new Fiber(new Runner);
var_dump($k->start("obj"), $k->resume("back"), $k->getReturn());
echo "### end\n";`,
	// A by-reference generator may only yield something there is a reference
	// to, and says so rather than refusing.
	`function &gen() { $v = 1; yield $v; yield 2; yield $v + 1; }
	 foreach (gen() as $x) { var_dump($x); }`,
	// Lazy objects. A ghost is initialized in place and stops being lazy; a
	// proxy stays a wrapper and forwards to the instance its factory returned,
	// which is why an initialized proxy still prints as one. Everything that
	// can observe state has to ask for it first -- reading, writing, isset,
	// unset, a method call, foreach, clone.
	`class C { public $a; public int $b = 1;
  public function __construct(int $a) { $this->a = $a; $this->b = 2; }
  public function m() { return "m:" . $this->a; } }
$r = new ReflectionClass(C::class);
$make = fn(bool $proxy) => $proxy
  ? $r->newLazyProxy(function ($o) { return new C(1); })
  : $r->newLazyGhost(function ($o) { $o->__construct(1); });
foreach ([false, true] as $proxy) {
  echo "### ", $proxy ? "proxy" : "ghost", "\n";
  $o = $make($proxy);
  var_dump($r->isUninitializedLazyObject($o));
  var_dump($o);
  echo "-- read\n"; $x = $make($proxy); var_dump($x->a, $x);
  echo "-- write\n"; $w = $make($proxy); $w->a = 9; var_dump($w->a);
  echo "-- isset\n"; $i = $make($proxy); var_dump(isset($i->a), $r->isUninitializedLazyObject($i));
  echo "-- method\n"; $m = $make($proxy); var_dump($m->m());
  echo "-- foreach\n"; $fo = $make($proxy); foreach ($fo as $k => $v) { echo "$k="; var_dump($v); }
  echo "-- clone\n"; $c = $make($proxy); $cl = clone $c; var_dump($cl);
  echo "-- initialize\n"; $z = $make($proxy); var_dump($r->initializeLazyObject($z) instanceof C, $r->isUninitializedLazyObject($z));
  echo "-- unset\n"; $u = $make($proxy); unset($u->a); var_dump($u);
}
echo "### end\n";`,
	// Which operations produce a lazy object's state and which merely report
	// what it has. The split is not obvious and is not guessable: the (array)
	// cast, get_mangled_object_vars and var_dump read the property table as it
	// stands, while get_object_vars, json_encode, serialize, var_export and
	// foreach all make the object produce one first.
	`class C { public int $a; public function __construct() { echo "ctor\n"; $this->a = 1; } }
$r = new ReflectionClass(C::class);
$make = fn() => $r->newLazyGhost(function ($o) { echo "init\n"; $o->__construct(); });
$try = function (string $name, callable $use) use ($make, $r) {
  echo "### $name\n";
  $o = $make();
  $use($o);
  var_dump($r->isUninitializedLazyObject($o));
};
$try('array-cast', function ($o) { var_dump((array)$o); });
$try('mangled', function ($o) { var_dump(get_mangled_object_vars($o)); });
$try('var-dump', function ($o) { var_dump($o); });
$try('object-vars', function ($o) { var_dump(get_object_vars($o)); });
$try('json', function ($o) { var_dump(json_encode($o)); });
$try('serialize', function ($o) { var_dump(serialize($o)); });
$try('var-export', function ($o) { var_export($o); echo "\n"; });
$try('foreach', function ($o) { foreach ($o as $k => $v) { echo "$k=$v\n"; } });
$try('read', function ($o) { var_dump($o->a); });
echo "### end\n";`,
	// A dynamic property may take the name of a private one it cannot see, and
	// then there are two: the engine keys the private `\0Owner\0name` always,
	// so a plain `name` written from outside is a different entry. Inside the
	// declaring class the name still means the private one, and the table keeps
	// the private where it was declared.
	`class SP2 {
 private $only = "priv";
 private $other = "other";
 public function inside() {
   $out = [];
   foreach ($this as $k => $v) { $out[] = "$k=$v"; }
   return [$this->only, $out, get_object_vars($this)];
 } }
#[\AllowDynamicProperties] class SC2 extends SP2 { public $after = "after"; }
$o = new SC2;
$o->only = "dynamic";
var_dump($o->only);
[$inside, $walk, $vars] = $o->inside();
var_dump($inside, $walk, $vars);
var_dump(get_object_vars($o));
foreach (get_mangled_object_vars($o) as $k => $v) {
  printf("%s => %s\n", str_replace("\0", "|", $k), $v);
}
var_dump($o);
print_r($o);
echo "### with hooks in the way\n";
class HP { private $shadowed = "A"; private $hooked { get => "H"; }
 public function walk() { $out = []; foreach ($this as $k => $v) { $out[] = "$k=$v"; } return $out; } }
#[\AllowDynamicProperties] class HC extends HP {}
$h = new HC;
$h->shadowed = "Global";
var_dump($h->walk(), get_object_vars($h), $h->shadowed);
echo "### end\n";`,
	// ReflectionClass lists a class's properties in *declaration* order within
	// each level, which is not the order the object's slots are in: a static
	// property has no slot among the instance ones and still appears where it
	// was written. And the two lazy writers name the class that *declared* a
	// static or virtual property, while the dynamic refusal -- which has no
	// declaration to name -- uses the reflector's own class.
	`#[\AllowDynamicProperties] class OA {
 private $priv = "privA"; protected $prot = "prot"; public $pubA = "pubA";
 public static $stat = "stat";
 public $noDefault; public string $typed; public $plain;
 public $virt { get { return "v"; } set ($value) {} } }
class OB extends OA { private $priv = "privB"; public $pubB = "pubB"; }
$r = new ReflectionClass(OB::class);
foreach ($r->getProperties() as $p) {
  printf("%s %s static=%d\n", $p->getName(), $p->getDeclaringClass()->getName(), $p->isStatic());
}
echo "### refusals name the declaring class\n";
$obj = $r->newLazyGhost(function () {});
foreach (["stat", "virt", "pubA"] as $name) {
  foreach (["skipLazyInitialization", "setRawValueWithoutLazyInitialization"] as $method) {
    try {
      $p = $r->getProperty($name);
      $method === "skipLazyInitialization" ? $p->$method($obj) : $p->$method($obj, "x");
      printf("%s on %s: ok\n", $method, $name);
    } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
  }
}
try { (new ReflectionProperty(new OB, "grown"))->skipLazyInitialization($obj); }
catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
echo "### end\n";`,
	// What ReflectionClass sees of a class's properties. An ancestor's private
	// one is not there at all -- the same test guards hasProperty, getProperty,
	// getProperties and ReflectionProperty's own constructor -- and the order is
	// the class's own declarations first, then the nearest parent's, then
	// further up, which is the opposite of how the slots are laid out.
	`class PB { private $b; protected $p; private static $sp; public $pub; }
class PC extends PB { public $a; }
$r = new ReflectionClass("PC");
foreach ($r->getProperties() as $prop) {
  printf("%s %s\n", $prop->getName(), $prop->getDeclaringClass()->getName());
}
var_dump($r->hasProperty("b"), $r->hasProperty("sp"), $r->hasProperty("p"));
try { $r->getProperty("b"); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
try { new ReflectionProperty("PC", "b"); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
var_dump((new ReflectionProperty("PB", "b"))->getName());
echo "### levels\n";
class QA { public $a1; public $a2; }
class QB extends QA { public $b1; }
class QC extends QB { public $c1; }
foreach ((new ReflectionClass("QC"))->getProperties() as $prop) {
  printf("%s %s\n", $prop->getName(), $prop->getDeclaringClass()->getName());
}
echo "### trait\n";
trait QT { public $t1; }
class QD { use QT; public $d1; }
foreach ((new ReflectionClass("QD"))->getProperties() as $prop) {
  printf("%s %s\n", $prop->getName(), $prop->getDeclaringClass()->getName());
}
echo "### end\n";`,
	// A lazy proxy is asked about the object that holds the state, not about
	// the stand-in: ReflectionProperty's three lazy methods each open by
	// following an *initialized* proxy to its instance, and the instance may
	// itself be a proxy, so it is a loop rather than one hop. Property access
	// goes the same way.
	`class LP { public $a = 1; public $b;
 public function __construct() { $this->a = 2; } }
$r = new ReflectionClass(LP::class);
$outer = $r->newLazyProxy(function () { return new LP(); });
$r->initializeLazyObject($outer);
$r->getProperty("a")->skipLazyInitialization($outer);
var_dump($outer->a, $r->getProperty("a")->isLazy($outer));
echo "### nested\n";
$real = new LP();
$nested = $r->newLazyProxy(function () use ($real) { return $real; });
$r->initializeLazyObject($nested);
$r->resetAsLazyProxy($real, function () { return new LP(); });
$r->initializeLazyObject($real);
$r->getProperty("a")->skipLazyInitialization($nested);
var_dump($nested->a, $nested->b);
echo "### untyped-empty\n";
class LU { public function __construct(public $u, public int $t) {} }
$ur = new ReflectionClass(LU::class);
$lazy = $ur->newLazyProxy(function () { return new LU(1, 2); });
var_dump($ur->isUninitializedLazyObject($lazy));
echo "### end\n";`,
	// What a lazy initializer is allowed to hand back. A ghost fills itself in,
	// so anything but null is refused and the initialization is reverted -- the
	// object is lazy again. A proxy must hand back a compatible instance, and
	// "compatible" is counted rather than searched for: the engine compares the
	// number of entries in `properties_info`, so a subclass that *overrides* an
	// inherited property adds nothing and is fine, while one that declares a
	// new name is not.
	`class LB { public int $b; public function __construct() { $this->b = 1; }
 public function __destruct() {} }
class LC extends LB {}
class LD extends LB { public int $b; }
class LE extends LB { public function __destruct() {} }
class LF extends LB { public int $extra = 0; }
$r = new ReflectionClass(LC::class);
echo "### ghost must return null\n";
$ghost = $r->newLazyGhost(function ($o) { $o->__construct(); return new stdClass; });
try { $ghost->b; } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
var_dump($r->isUninitializedLazyObject($ghost));
$fine = $r->newLazyGhost(function ($o) { $o->__construct(); });
var_dump($fine->b);
$bare = $r->newLazyGhost(function ($o) { $o->__construct(); return; });
var_dump($bare->b);
echo "### proxy compatibility\n";
$cases = [
  "same" => [LC::class, new LC],
  "parent instance" => [LC::class, new LB],
  "override only" => [LD::class, new LB],
  "adds a property" => [LF::class, new LB],
  "overrides __destruct" => [LE::class, new LB],
  "not an instance" => [LC::class, new stdClass],
];
foreach ($cases as $label => [$class, $instance]) {
  $proxy = (new ReflectionClass($class))->newLazyProxy(function () use ($instance) { return $instance; });
  try { printf("%s: %d\n", $label, $proxy->b); }
  catch (Throwable $e) { printf("%s: %s: %s\n", $label, $e::class, $e->getMessage()); }
}
echo "### proxy must return an object, and not a lazy one\n";
$nul = $r->newLazyProxy(function () { return null; });
try { $nul->b; } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
$self = $r->newLazyProxy(function ($o) { return $o; });
try { $self->b; } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
echo "### end\n";`,
	// resetAsLazy* walks the *reflected* class's slots, so resetting an
	// instance of a subclass through a parent's reflector leaves what the
	// subclass declared alone -- while a dynamic property, which has a slot in
	// neither, is cleared with the rest.
	`class RA { public $a; }
class RB extends RA { public $b; }
class RC extends RA { public $a; public $c; }
$r = new ReflectionClass(RA::class);
echo "### subclass property survives\n";
$o = new RB; $o->a = 1; $o->b = 2;
$r->resetAsLazyGhost($o, function () { echo "init\n"; });
var_dump($o->b, $r->isUninitializedLazyObject($o));
var_dump($o);
var_dump($o->a);
var_dump($o);
echo "### an override is the parent's slot\n";
$o = new RC; $o->a = 1; $o->c = 2;
$r->resetAsLazyGhost($o, function () { echo "init\n"; });
var_dump($o->c);
var_dump($o->a);
var_dump($o);
echo "### a dynamic property is cleared\n";
#[\AllowDynamicProperties] class RD { public $d; }
$o = new RD; $o->d = 1; $o->grown = 2;
$rd = new ReflectionClass(RD::class);
$rd->resetAsLazyGhost($o, function () { echo "init\n"; });
// Not var_dump($o) while it is still lazy: the count the engine prints for
// a lazy object is written as a wildcard in the corpus too, being unstable.
var_dump($rd->isUninitializedLazyObject($o));
var_dump($o->d, isset($o->grown));
var_dump($o);
echo "### end\n";`,
	// Laziness is per property, not per object. skipLazyInitialization gives one
	// property its declared default and setRawValueWithoutLazyInitialization
	// gives it a value outright; either way touching *that* property no longer
	// initializes, and once every declared property has a value the object is
	// not lazy at all. A ghost's initializer starts from the declared defaults,
	// which is why an untyped property it never assigns comes out null.
	`class C { public $a; public int $b = 1; public int $c;
  public function __construct(int $a) { $this->a = $a; $this->b = 2; $this->c = 3; } }
$r = new ReflectionClass(C::class);
$ghost = fn() => $r->newLazyGhost(function ($o) { echo "init\n"; $o->__construct(1); });
$proxy = fn() => $r->newLazyProxy(function ($o) { echo "init\n"; return new C(1); });
foreach (['ghost' => $ghost, 'proxy' => $proxy] as $kind => $make) {
  echo "### skip-$kind\n";
  $o = $make();
  foreach (['a','b','c'] as $n) { $r->getProperty($n)->skipLazyInitialization($o); }
  var_dump($r->isUninitializedLazyObject($o));
  var_dump($o);
  var_dump($o->a, $o->b);
  try { var_dump($o->c); } catch (Error $e) { echo $e->getMessage(), "\n"; }
  var_dump($o);
  echo "### rawvalue-$kind\n";
  $p = $make();
  $r->getProperty('a')->setRawValueWithoutLazyInitialization($p, "raw");
  var_dump($r->isUninitializedLazyObject($p), $p->a);
  var_dump($p);
  var_dump($p->b);
  echo "### partial-$kind\n";
  $q = $make();
  $r->getProperty('b')->skipLazyInitialization($q);
  var_dump($q->b);
  var_dump($r->isUninitializedLazyObject($q));
  var_dump($q->a);
  var_dump($q);
}
echo "### defaults\n";
class D { public $u; public int $t = 5; public function __construct() { $this->t = 9; } }
$rd = new ReflectionClass(D::class);
$d = $rd->newLazyGhost(function ($o) { $o->__construct(); });
var_dump($d->t, $d);
echo "### end\n";`,
	// `$a[k] = &$x` has to rebind the *element*, not the temporary the write
	// fetch handed back. Without that the element keeps its old box and the
	// assignment does nothing -- which also made a self-referential array
	// impossible to build.
	`echo "### existing\n";
$x = [1,2]; $y = 5; $x[0] = &$y; $y = 9; var_dump($x[0], $y);
echo "### append\n";
$p = [1]; $q = 7; $p[] = &$q; $q = 8; var_dump($p);
echo "### nested\n";
$m = [[1]]; $n = 3; $m[0][0] = &$n; $n = 4; var_dump($m);
echo "### end\n";`,
	// A container that reaches itself prints *RECURSION* rather than being
	// followed. var_dump does not put its & marker on that line: the marker
	// belongs to a value it printed, and this is a refusal to print one.
	`echo "### ref-cycle\n";
$a = array(array(1));
$a[0][] =& $a[0];
$a[0][] =& $a[0];
$a[0][0] = 2;
var_dump($a);
echo "### direct\n";
$b = [1];
$b[] = &$b;
var_dump($b);
echo "### object\n";
class C { public $self; }
$o = new C; $o->self = $o;
var_dump($o);
echo "### print_r\n";
$c = [1]; $c[] = &$c;
print_r($c);
echo "\n### end\n";`,
	// serialize records references as well as objects, and writes a repeated
	// one as `R:<first position>;`. That is what terminates a cycle built out
	// of `$a[] = &$a`, which has no objects in it at all.
	`$a = [1]; $a[] = &$a;
echo serialize($a), "\n";
$b = 5; $c = [&$b, &$b];
echo serialize($c), "\n";
$d = [1,2]; $e = [&$d, &$d, 3];
echo serialize($e), "\n";
$f = []; $f['self'] = &$f;
echo serialize($f), "\n";
$g = 1; $h = ['x' => &$g, 'y' => 2];
echo serialize($h), "\n";
$rt = [1]; $rt[] = &$rt;
$back = unserialize(serialize($rt));
var_dump(count($back), gettype($back[1]), count($back[1]));`,
	// serialize numbers every value it writes and remembers where it first
	// wrote each object, so a repeated one comes out as `r:<position>;`. That
	// is what lets a self-referential object round-trip -- and what stops the
	// writer recursing forever. Keys are not values and take no position.
	`$a = new stdClass; $a->self = $a;
echo serialize($a), "\n";
$b = new stdClass; $b->x = 1; $b->self = $b; $b->y = 2;
echo serialize($b), "\n";
$c = new stdClass; $d = new stdClass; $c->d = $d; $c->d2 = $d;
echo serialize($c), "\n";
$e = new stdClass; $e->arr = [1, $e, 2];
echo serialize($e), "\n";
$f = [1,2]; $f[] = &$f;
echo serialize([$d, $d, 1]), "\n";
$g = new stdClass; $h = [$g]; $g->list = $h;
echo serialize($g), "\n";
echo serialize([[$g], $g]), "\n";
$r1 = new stdClass; $r1->self = $r1;
$back = unserialize(serialize($r1));
var_dump($back === $back->self);
$r2 = new stdClass; $r2->a = new stdClass; $r2->b = $r2->a;
$back2 = unserialize(serialize($r2));
var_dump($back2->a === $back2->b, $back2->a !== $r2->a);`,
	// `&__get` hands back the property's own slot, so a write through the
	// fetch reaches the object and there is nothing to warn about. A plain
	// __get hands back a value: the engine warns, and the write really is lost,
	// which only holds if what the caller modifies is a copy.
	//
	// An ArrayObject keeps its elements in an internal store, which var_dump
	// shows under the private name the engine gives it.
	`echo "### by-ref-get\n";
class C { public $stack = []; public function __set($n, $v) { $this->stack[$n] = $v; }
  public function &__get($n) { return $this->stack[$n]; } }
$c = new C;
$c->box = [];
$c->box[0] = 1;
$c->box[1] = 2;
var_dump($c->box, $c->stack);
echo "### by-value-get\n";
class V { public $stack = []; public function __set($n, $v) { $this->stack[$n] = $v; }
  public function __get($n) { return $this->stack[$n]; } }
$v = new V;
$v->box = [];
$v->box[0] = 1;
var_dump($v->box);
echo "### object-get\n";
class O { public $held; public function __get($n) { return $this->held; } }
$o = new O; $o->held = new stdClass;
$o->any->p = 5;
var_dump($o->held);
echo "### arrayobject\n";
$ao = new ArrayObject([1,2]);
var_dump($ao);
$ao[] = 3;
var_dump(count($ao), $ao);
echo "### end\n";`,
	// An object a statement builds and hands straight to a call dies when the
	// call returns -- the temporary that held it has no other reader. What the
	// call *returns* does not: it is live but unnamed until the caller stores
	// it, so it is held aside while the question is asked.
	`class D { public $n; function __construct($n) { $this->n = $n; } function __destruct() { echo "D{$this->n}\n"; } }
echo "### argument\n";
function take($x) { echo "in take\n"; }
take(new D(1));
echo "after\n";
echo "### in-array-literal\n";
function takes(array $a) { echo "in takes\n"; }
takes([new D(2)]);
echo "after\n";
echo "### kept\n";
function keep($x) { return $x; }
$k = keep(new D(3));
echo "still here\n";
$k = null;
echo "after\n";
echo "### builtin\n";
var_dump(in_array(1, [new D(4)], true));
echo "after\n";
echo "### nested\n";
function outer($x) { return "o"; }
var_dump(outer(new D(5)));
echo "after\n";
echo "### end\n";`,
	// A destructor runs where the last name for the object went away -- an
	// unset, an assignment over it, a call returning -- and what it throws is
	// thrown there, so the try that is open at that point catches it.
	//
	// The gc flag is recorded and readable through ini_get/ini_set even though
	// php2go has no automatic cycle collector for it to switch off.
	`class T { public $n; function __construct($n) { $this->n = $n; } function __destruct() { throw new Exception("d{$this->n}"); } }
echo "### unset\n";
try { $a = new T(1); unset($a); echo "unreached\n"; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### assign\n";
try { $b = new T(2); $b = null; echo "unreached\n"; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### frame\n";
function make() { $c = new T(3); }
try { make(); echo "unreached\n"; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### finally\n";
function inner() { try { throw new Exception("first"); } finally { try { $d = new T(4); unset($d); } catch (Exception $e) { echo "inner caught ", $e->getMessage(), "\n"; } } }
try { inner(); } catch (Exception $e) { echo "outer caught ", $e->getMessage(), "\n"; }
echo "### gcflags\n";
var_dump(gc_enabled());
gc_disable(); var_dump(gc_enabled(), ini_get('zend.enable_gc'));
gc_enable(); var_dump(gc_enabled(), ini_get('zend.enable_gc'));
ini_set('zend.enable_gc', '0'); var_dump(gc_enabled());
ini_set('zend.enable_gc', '1'); var_dump(gc_enabled());
var_dump(ini_get('some.unknown.setting'));
var_dump(ini_get('include_path'));
echo "### end\n";`,
	// ini_get_all exposes the linked build's sorted registry, including nullable
	// values, exact extension ownership, access masks and request-local changes.
	// An installed extension with no directives is distinct from a missing one.
	`$all = ini_get_all(null, false);
echo "all=", count($all), "\n";
foreach (["arg_separator.output", "fiber.stack_size", "include_path", "zend.enable_gc"] as $name) {
  var_dump($name, $all[$name]);
}
$standard = ini_get_all("standard", true);
echo "standard=", count($standard), "\n";
var_dump($standard["default_socket_timeout"]);
var_dump(ini_get_all("json", false));
var_dump(ini_get_all("Standard", false));
ini_set("url_rewriter.tags", "a=href");
var_dump(ini_get_all("standard", true)["url_rewriter.tags"]);
ini_restore("url_rewriter.tags");`,
	// The public syslog trio retains libc's process-global identity from open to
	// close. syslog.filter is the one mutable setting its byte path consumes;
	// invalid filter names are refused without changing the active value.
	`set_error_handler(function ($level, $message) {
  echo "diagnostic:", $level, ":", $message, "\n";
  return true;
});
var_dump(openlog("php2go-differential", LOG_PID, LOG_USER));
var_dump(syslog(LOG_DEBUG, "php2go syslog differential"));
var_dump(closelog(), closelog());
var_dump(openlog(prefix: "php2go-named", flags: 0, facility: LOG_LOCAL0));
var_dump(syslog(priority: LOG_DEBUG, message: "one\ntwo\0tail"));
var_dump(closelog());
foreach (["all", "no-ctrl", "ascii", "raw", "ALL", "", 1, null] as $filter) {
  var_dump(ini_set("syslog.filter", $filter), ini_get("syslog.filter"));
}
ini_restore("syslog.filter");
var_dump(ini_get("syslog.filter"));
try { openlog([], 0, LOG_USER); } catch (Throwable $error) {
  echo get_class($error), ":", $error->getMessage(), "\n";
}
try { syslog(LOG_DEBUG, []); } catch (Throwable $error) {
  echo get_class($error), ":", $error->getMessage(), "\n";
}
restore_error_handler();`,
	// proc_nice is a direct nice(3) boundary. Zero exercises success without
	// mutating the test process's priority; scalar coercion, named arguments and
	// type/arity errors remain owned by the harvested signature.
	`function nice_case(string $label, callable $call): void {
  echo $label, ":";
  try { var_dump($call()); } catch (Throwable $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
  }
}
nice_case("zero", fn() => proc_nice(0));
nice_case("false", fn() => proc_nice(false));
nice_case("float", fn() => proc_nice(0.9));
nice_case("string", fn() => proc_nice("0"));
nice_case("named", fn() => proc_nice(priority: 0));
nice_case("array", fn() => proc_nice([]));
nice_case("arity", fn() => proc_nice());`,
	// ext/standard's four one-shot process functions share popen(3), but expose
	// its bytes differently: shell_exec returns them whole, exec trims records
	// into an appended array, system writes records, and passthru writes raw
	// blocks. Exit status and binary NUL bytes survive every applicable path.
	`function command_error(string $label, callable $call): void {
  echo $label, ":";
  try { $call(); } catch (Throwable $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
  }
}
$lines = "printf 'a\\nb  \\n\\nlast\\t \\n'";
$binary = "printf '\\000A\\nB\\000'";
var_dump(shell_exec(":"), shell_exec("printf no-newline"));
var_dump(bin2hex(shell_exec($binary)));

$output = ["seed"];
$code = 99;
var_dump(exec($lines, $output, $code), $output, $code);
$output = "replace-me";
var_dump(exec(":", $output, $code), $output, $code);
$output = [];
var_dump(bin2hex(exec($binary, $output, $code)), array_map("bin2hex", $output), $code);
$code = 99;
var_dump(exec(command: "printf named", result_code: $code), $code);

ob_start();
$code = 99;
$last = system($lines, $code);
$printed = ob_get_clean();
var_dump(bin2hex($printed), $last, $code);
ob_start();
$code = 99;
$return = passthru("printf raw; exit 7", $code);
$printed = ob_get_clean();
var_dump($printed, $return, $code);

$long_command = "printf '" . str_repeat("x", 5000) . "'";
$output = [];
$last = exec($long_command, $output, $code);
var_dump(strlen($last), count($output), strlen($output[0]), $code);

foreach (["shell_exec", "exec", "system", "passthru"] as $function) {
  command_error($function . "-empty", fn() => $function(""));
  command_error($function . "-nul", fn() => $function("x\0y"));
}`,
	// popen retains libc's live FILE*/child pair behind an ordinary PHP stream.
	// Reads and writes use the pipe descriptor, while fclose, pclose and request
	// shutdown all release it through pclose so the child status is not lost.
	`function pipe_error(string $label, callable $call): void {
  echo $label, ":";
  try { $call(); } catch (Throwable $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
  }
}
$pipe = popen("printf 'a\\nb\\000tail'; exit 7", "r");
var_dump(stream_get_meta_data($pipe));
var_dump(fread($pipe, 2), fgets($pipe), fread($pipe, 8192), feof($pipe));
var_dump(pclose(stream: $pipe), is_resource($pipe), get_debug_type($pipe));

$path = sys_get_temp_dir() . "/php2go-popen-differential-" . getmypid();
@unlink($path);
$pipe = popen(command: "cat > " . escapeshellarg($path), mode: "wb");
var_dump(stream_get_meta_data($pipe));
var_dump(fwrite($pipe, "z\0"), fwrite($pipe, "a\n"), fflush($pipe));
var_dump(pclose($pipe), bin2hex(file_get_contents($path)));
unlink($path);

foreach (["r", "rb", "w", "wb", "br"] as $mode) {
  $pipe = popen(":", $mode);
  var_dump(stream_get_meta_data($pipe)["mode"], pclose($pipe));
}
set_error_handler(function ($level, $message) {
  echo "diagnostic:", $level, ":", $message, "\n";
  return true;
});
foreach (["rbb", "", "b"] as $mode) {
  var_dump(popen(":", $mode));
}
restore_error_handler();

foreach (["rw", "rbt", "R"] as $mode) {
  pipe_error("mode-" . bin2hex($mode), fn() => popen(":", $mode));
}
$pipe = popen("", "r");
var_dump(fread($pipe, 8192), pclose($pipe));
pipe_error("nul-command", fn() => popen("x\0y", "r"));

$pipe = popen("exit 9", "r");
var_dump(fclose($pipe), is_resource($pipe), get_debug_type($pipe));
$file = fopen("data:text/plain,x", "r");
var_dump(pclose($file), is_resource($file));
$directory = opendir(sys_get_temp_dir());
var_dump(pclose($directory), is_resource($directory));
$context = stream_context_create();
pipe_error("context", fn() => pclose($context));`,
	// get_headers is the ext/standard facade over URL stream wrappers. Its HTTP
	// path is covered with a live loopback server in bundlebuild; keep wrapper
	// classification and argument diagnostics in the ordinary engine oracle.
	`function get_headers_error(string $label, callable $call): void {
  echo $label, ":";
  try { var_dump($call()); } catch (Throwable $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
  }
}
set_error_handler(function ($level, $message) {
  echo "diagnostic:", $level, ":", $message, "\n";
  return true;
});
foreach ([
  "data" => "data:text/plain,x",
  "php" => "php://memory",
  "glob" => "glob:///tmp/php2go-get-headers-*",
  "file" => "file:///tmp/php2go-get-headers-missing",
  "unknown" => "php2go-unknown://host/path",
] as $label => $url) {
  echo $label, ":";
  var_dump(get_headers($url));
}
restore_error_handler();
get_headers_error("empty", fn() => get_headers(""));
get_headers_error("nul", fn() => get_headers("http://host/\0path"));
get_headers_error("scalar-context", fn() => get_headers("data:text/plain,x", false, 1));
$file = fopen("data:text/plain,x", "r");
get_headers_error("stream-context", fn() => get_headers("data:text/plain,x", false, $file));
fclose($file);`,
	// Metadata accepts every live php_stream, including directory streams, but
	// rejects scalar, closed and stream-context resources before introspection.
	// The historical alias keeps its own name in those TypeErrors.
	`function metadata_error(string $label, callable $call): void {
  echo $label, ":";
  try { var_dump($call()); } catch (Throwable $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
  }
}
foreach ([false, true, null, 1, "x", [], new stdClass] as $index => $value) {
  foreach (["stream_get_meta_data", "socket_get_status"] as $function) {
    metadata_error($function . "-" . $index, fn() => $function($value));
  }
}
$closed = fopen("data:text/plain,x", "r");
fclose($closed);
$context = stream_context_create();
foreach (["stream_get_meta_data", "socket_get_status"] as $function) {
  metadata_error($function . "-closed", fn() => $function($closed));
  metadata_error($function . "-context", fn() => $function($context));
}
$directory = opendir(sys_get_temp_dir());
var_dump(stream_get_meta_data($directory));
while (readdir($directory) !== false) {}
var_dump(socket_get_status($directory));
rewinddir($directory);
var_dump(stream_get_meta_data($directory));
closedir($directory);`,
	// Collecting. php2go has no refcounts, so it answers "is this still
	// reachable" by walking the roots -- at gc_collect_cycles(), at an unset,
	// at an assignment over the last name, and when a frame's locals go. What
	// a program can observe of that is a destructor running and a weak
	// reference going empty.
	`class C { public $n; function __construct($n){$this->n=$n;} function __destruct() { echo "D{$this->n}\n"; } }
echo "### drop\n";
$x = new C(1); $x = null; gc_collect_cycles(); echo "after\n";
echo "### unset\n";
$y = new C(2); unset($y); echo "after\n";
echo "### still-held\n";
$a = new C(3); $b = $a; unset($a); gc_collect_cycles(); echo "after\n"; unset($b); echo "gone\n";
echo "### cycle\n";
class N { public $next; public $c; function __construct($n){ $this->c = new C($n); } }
$p = new N(4); $q = new N(5); $p->next = $q; $q->next = $p; $p = null; $q = null;
gc_collect_cycles(); echo "after\n";
echo "### array\n";
$arr = [new C(6)]; $arr = null; gc_collect_cycles(); echo "after\n";
echo "### property\n";
$holder = new stdClass; $holder->c = new C(7); $holder = null; gc_collect_cycles(); echo "after\n";
echo "### closure\n";
$fn = function () { }; $fn = null; gc_collect_cycles();
$c8 = new C(8); $fn2 = function () use ($c8) { }; unset($c8); gc_collect_cycles(); echo "held\n";
$fn2 = null; gc_collect_cycles(); echo "after\n";
echo "### generator\n";
function g($c) { yield 1; }
$gen = g(new C(9));
foreach ($gen as $v) { break; }
$gen = null; gc_collect_cycles(); echo "after\n";
echo "### weakref\n";
$t = new stdClass; $w = WeakReference::create($t);
var_dump($w->get() !== null); $t = null; gc_collect_cycles(); var_dump($w->get());
echo "### weakmap\n";
$m = new WeakMap; $k = new stdClass; $m[$k] = 1; var_dump(count($m));
$k = null; gc_collect_cycles(); var_dump(count($m));
echo "### end\n";`,
	// Cloning shares a property that holds a *reference*: both objects' slots
	// are the same box, so a write through either is seen by both. Only a
	// reference the program asked for -- an explicit `&` -- counts; a box
	// php2go made for its own reasons is not one, and neither var_dump's `&`
	// nor the copy shares it.
	`class CR { public $x = 1; public $y = 2; }
$t = new CR; $r =& $t->x;
$c = clone $t;
$r = 99;
var_dump($t->x, $c->x, $c->y);
$c->x = 7;
var_dump($t->x, $r);
var_dump($t, $c);
echo "### unshared\n";
class CS { public $arr = [1]; }
$s = new CS; $s->arr[] = 2;
$s2 = clone $s; $s2->arr[] = 3;
var_dump($s->arr, $s2->arr);
var_dump($s2);
echo "### end\n";`,
	// A typed property's declaration constrains whatever reference the property
	// shares, and it stops constraining it when the property goes away. Each
	// *slot* is its own source, so a clone that still holds the box keeps its
	// own claim on it after the original is gone.
	`class TR { public int $p = 42; }
class TRB extends TR {}
echo "### gone\n";
$b = new TRB;
$r =& $b->p;
unset($b);
$r = "foo";
var_dump($r);
echo "### clone-holds-it\n";
$t = new TR;
$x =& $t->p;
$copy = clone $t;
unset($t);
try { $x = "foo"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($copy->p);
echo "### private-through-getter\n";
class TP {
 private int $p = 42;
 public function &getRef() { return $this->p; } }
class TPB extends TP { public $p; }
$o = new TPB;
$g =& $o->getRef();
unset($o);
$g = "foo";
var_dump($g);
echo "### end\n";`,
	// Fetching the next element writes the loop variable, and writing it lets
	// go of what it held. A destructor there runs at the *first* fetch, before
	// the body has run once.
	`class FV { public function __destruct() { echo "dtor\n"; throw new Exception("foo"); } }
echo "### overwritten\n";
$v = new FV;
try {
  foreach ([1, 2] as $v) { var_dump($v); }
  echo "loop finished\n";
} catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### quiet\n";
class FQ { public function __destruct() { echo "quiet dtor\n"; } }
$w = new FQ;
foreach ([1, 2] as $w) { var_dump($w); }
echo "### end\n";`,
	// A private property is a slot of its own whenever another class in the
	// chain declares the same name -- whatever *that* one's visibility is,
	// since the engine keys a private property `\0Owner\0name` and nothing
	// else can collide with it. Which of the two a name means is decided by the
	// scope reading it.
	`class SP { private $x = "p"; protected $q = "q"; public $r = "r";
 function readP() { return $this->x; }
 function writeP($v) { $this->x = $v; }
 function varsP() { return get_object_vars($this); } }
class SC extends SP { private $x = "c";
 function readC() { return $this->x; }
 function varsC() { return get_object_vars($this); } }
$o = new SC;
echo "### slots\n";
foreach (get_mangled_object_vars($o) as $k => $v) {
  printf("%s => %s\n", str_replace("\0", "|", $k), $v);
}
echo "### by scope\n";
var_dump($o->readP(), $o->readC());
$o->writeP("p2");
var_dump($o->readP(), $o->readC());
var_dump($o->varsP(), $o->varsC(), get_object_vars($o));
var_dump($o);
print_r($o);
echo "### against a protected one\n";
class MP { private $p = "priv"; function inMP() { return $this->p; } }
class MC extends MP { protected $p = "prot"; function inMC() { return $this->p; } }
$m = new MC;
var_dump($m->inMP(), $m->inMC());
var_dump($m);
echo "### neither is visible\n";
class NA { private $n = 1; }
class NB extends NA { private $n = 2; }
class NC extends NB { function read() { return $this->n ?? "none"; } }
$n = new NC;
var_dump($n->read());
var_dump($n);
echo "### unshadowed is unchanged\n";
class UA { private $u = "u"; protected $v = "v"; public $w = "w";
 function all() { return get_object_vars($this); } }
$u = new UA;
var_dump($u, $u->all(), get_object_vars($u));
foreach (get_mangled_object_vars($u) as $k => $v) {
  printf("%s => %s\n", str_replace("\0", "|", $k), $v);
}
echo "### end\n";`,
	// Hooks are chosen the same way a slot is: a scope that declares its own
	// *private* property of the name means that declaration, whatever a
	// subclass declares. A hooked property can be virtual and so have no slot
	// to tell the two apart by, which is why the choice is made by scope rather
	// than by storage.
	`class HA {
 private $plain;
 private $hooked { get { echo "HA::hooked::get\n"; } set { echo "HA::hooked::set\n"; } }
 public function touch() { $this->plain; $this->plain = 1; $this->hooked; $this->hooked = 1; } }
class HB extends HA {
 public $plain { get { echo "HB::plain::get\n"; } set { echo "HB::plain::set\n"; } }
 public $hooked { get { echo "HB::hooked::get\n"; } set { echo "HB::hooked::set\n"; } } }
echo "### own instance\n";
(new HA)->touch();
echo "### subclass instance, parent scope\n";
$b = new HB;
$b->touch();
echo "### subclass instance, outside\n";
$b->plain; $b->plain = 1; $b->hooked; $b->hooked = 1;
echo "### end\n";`,
	// A trait's private property belongs to each class that uses it, so a class
	// and its parent both using the trait have a slot each -- and a further
	// subclass, which uses it nowhere, has neither.
	`trait TP { private $t = 0; }
class TX { use TP; function inX() { return $this->t; } }
class TY extends TX { use TP; function inY() { return ++$this->t; } }
class TZ extends TY { function inZ() { return $this->t ?? "none"; } }
$o = new TZ;
foreach (get_mangled_object_vars($o) as $k => $v) {
  printf("%s => %s\n", str_replace("\0", "|", $k), $v);
}
var_dump($o->inX(), $o->inY(), $o->inX());
var_dump(@$o->inZ());
var_dump($o);
echo "### composition\n";
trait TH { private $h = "trait"; public $shared = "ops"; function fromTrait() { return $this->h; } }
class HParent { private $h = "parent"; function fromParent() { return $this->h; } }
class HChild extends HParent { use TH; }
$h = new HChild;
var_dump($h->fromParent(), $h->fromTrait());
var_dump($h);
echo "### end\n";`,
	// A resource is a handle: `===` and `==` are identity, and beside a number
	// or a numeric string the resource is its id. php2go had no case for the
	// kind at all, so `===` was always false -- even for `$b = $a` -- and `==`
	// was always true between two of them.
	//
	// Not a case here: the two sides have no resource in common. php2eval's
	// engine is not the CLI, so it has no STDOUT/STDIN, and php2go implements
	// neither fopen nor tmpfile. `operator_unsupported_types.phpt` covers it --
	// it builds a resource from STDOUT and checks that a compound assignment
	// which throws leaves the variable identical.
	// eval is not compiled the way a file is, and the difference shows in what
	// it answers with. A file's op_array ends in `RETURN 1` -- which is why
	// `include` of a file that returns nothing yields 1 -- and eval'd code ends
	// in `RETURN null`. An explicit `return null;` or a bare `return;` really
	// does answer NULL, in both.
	`var_dump(eval("return null;"), eval("return 5;"), eval("return;"), eval("1;"));
var_dump(eval("return false;"), eval("return [];"), eval("return 0;"));
echo "### declarations still land\n";
eval("function evalFn() { return 42; }");
eval("class EvalCls { const K = 7; }");
var_dump(evalFn(), EvalCls::K);
echo "### the caller's variables are shared\n";
$x = 5; eval("\$x++;"); var_dump($x);
echo "### end\n";`,
	// An engine array store -- ArrayObject and its kin -- answers a write-path
	// dimension fetch with the storage slot itself, exactly as a WeakMap does,
	// so an indirect modification really modifies it. A userland ArrayAccess is
	// the overloaded case and still earns the notice, and so does a subclass
	// that writes its own offsetGet, because the engine's handler calls the
	// override.
	`$o = new ArrayObject(["a" => [1]]);
$o["a"][] = 2;
$o["n"] = 1; $o["n"]++;
$r = 5; $o["ref"] = 0; $o["ref"] =& $r; $r++;
var_dump($o["a"], $o["n"], $o["ref"], $o);
echo "### missing key\n";
$o["fresh"][] = "x";
var_dump($o["fresh"]);
echo "### userland\n";
class UA implements ArrayAccess {
 private $d = [];
 public function offsetExists($k): bool { return isset($this->d[$k]); }
 #[\ReturnTypeWillChange] public function offsetGet($k) { return $this->d[$k] ?? null; }
 public function offsetSet($k, $v): void { $this->d[$k] = $v; }
 public function offsetUnset($k): void { unset($this->d[$k]); } }
$u = new UA; $u["a"] = [1]; $u["a"][] = 2; var_dump($u["a"]);
echo "### override\n";
class OverriddenStore extends ArrayObject {
 #[\ReturnTypeWillChange] public function offsetGet($k) { return parent::offsetGet($k); } }
$s = new OverriddenStore(["a" => [1]]);
$s["a"][] = 2;
var_dump($s["a"]);
echo "### end\n";`,
	// A compound assignment computes before it judges the write. Probed: the
	// same `+= 1` reports the *operand types* on a readonly `object` property
	// and `Cannot modify readonly property` on a readonly `int` one -- so
	// readonly and `private(set)` are settled after the operation, while
	// `Cannot access private property` is settled before it.
	`class RO { public readonly object $o; function __construct() { $this->o = new stdClass; } }
class RI { public readonly int $i; function __construct() { $this->i = 1; } }
class PV { private object $p; function __construct() { $this->p = new stdClass; } }
class AV { public private(set) object $a; function __construct() { $this->a = new stdClass; } }
class AI { public private(set) int $n = 1; }
$try = function (callable $f) {
  try { $f(); echo "no error\n"; }
  catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); } };
$ro = new RO; $ri = new RI; $pv = new PV; $av = new AV; $ai = new AI;
$try(function () use ($ro) { $ro->o += 1; });
$try(function () use ($ro) { $ro->o .= "x"; });
$try(function () use ($ri) { $ri->i += 1; });
$try(function () use ($pv) { $pv->p += 1; });
$try(function () use ($av) { $av->a += 1; });
$try(function () use ($ai) { $ai->n += 1; });
$try(function () use ($ro) { $ro->o++; });
$try(function () use ($ri) { --$ri->i; });
echo "### end\n";`,
	// A first-class callable of a method the class does not declare is a closure
	// over the *forwarder*, and it behaves like the direct call: any named
	// argument is taken and lands in the array __call receives, keyed by name.
	// Reflection does not describe the forwarder either -- the engine hands
	// back a trampoline named after the method that was asked for, taking one
	// `mixed ...$arguments`.
	`class Tramp {
 public function __call($name, $args) { var_dump($name, $args); }
 public static function __callStatic($name, $args) { var_dump($name, $args); } }
$t = new Tramp;
$unpacked = ["unpacked"];
echo "### instance\n";
$t->test(1, 2, a: 123);
$t->test(...)(1, 2);
$t->test(...)(1, 2, a: 123, b: "x");
$t->test(...)(a: 123, b: "x");
$t->test(...)();
$t->test(...)(...$unpacked);
echo "### static\n";
Tramp::stat(1, 2, a: 123);
Tramp::stat(...)(a: 123);
echo "### reflection\n";
$r = new ReflectionFunction(Tramp::fail(...));
var_dump($r->getName(), $r->getNumberOfParameters(),
  $r->getNumberOfRequiredParameters(), $r->isVariadic(), $r->isStatic());
$p = $r->getParameters()[0];
var_dump($p->getName(), $p->isVariadic(), $p->isOptional(),
  (string) $p->getType(), $p->getPosition());
var_dump($r->getReturnType());
$r2 = new ReflectionFunction($t->nope(...));
var_dump($r2->getName(), $r2->getParameters()[0]->getName());
echo "### end\n";`,
	// Writing over a WeakMap entry lets go of what it held, so a destructor
	// there runs at the write -- and what that destructor puts back in the map
	// is in the map by the time anything looks.
	`class WDtor { public $n;
 function __construct($n) { $this->n = $n; }
 function __destruct() {
   global $w, $kept;
   echo "dtor{$this->n}\n";
   for ($i = 0; $i < 2; $i++) { $o = new stdClass; $kept[] = $o; $w[$o] = $i; }
 } }
$kept = [];
$w = new WeakMap;
$k = new stdClass;
$w[$k] = new WDtor(1);
echo "### overwrite\n";
$w[$k] = 123;
var_dump(count($w), $w[$k]);
echo "### unset\n";
$k2 = new stdClass;
$w[$k2] = new WDtor(2);
unset($w[$k2]);
var_dump(count($w));
echo "### end\n";`,
	// Every way of copying an object's property table keeps a property that
	// holds a *program-made* reference as a reference: copying an array is
	// what these all do, and copying an array keeps a shared box shared.
	`class RefProps { public $a = 1; public $b = 2; }
$o = new RefProps;
$ref = &$o->a;
var_dump(get_object_vars($o));
var_dump(get_mangled_object_vars($o));
var_dump((array) $o);
var_dump($o);
echo "### the copy really is the same box\n";
$copy = (array) $o;
$copy["a"] = 99;
var_dump($o->a, $ref);
echo "### and through a &__get that grew the property\n";
class GrowRef { public $_; public function &__get($n) { return $this->$n; } }
$g = new GrowRef;
$r = &$g->x;
var_dump(get_object_vars($g), property_exists($g, "x"));
echo "### end\n";`,
	// Casting an object to an array is not the same question as asking for its
	// property table. A Closure has no properties and yet `(array) $c` is
	// `[0 => $c]`, and an engine array store answers with its *storage* --
	// `ZEND_PROP_PURPOSE_ARRAY_CAST` -- while get_mangled_object_vars() on the
	// same object still answers with nothing.
	`$c = function () { return 2; };
$h = (array) $c;
var_dump(count($h), $h[0] === $c, $h[0]());
var_dump(get_mangled_object_vars($c), get_object_vars($c));
echo "### array stores\n";
var_dump((array) new ArrayObject([1, 2]));
var_dump(get_mangled_object_vars(new ArrayObject([1, 2])));
var_dump((array) new ArrayIterator(["a" => 1]));
echo "### everything else is unchanged\n";
var_dump((array) new stdClass, (array) new WeakMap, (array) 1, (array) null);
class Plain { public $a = 1; protected $b = 2; private $c = 3; }
$plain = (array) new Plain;
foreach ($plain as $k => $v) { printf("%s => %s\n", str_replace("\0", "|", $k), $v); }
echo "### end\n";`,
	// A WeakMap entry is a slot, not an overloaded element. Its dimension
	// handler answers with the entry itself, so an indirect modification really
	// modifies it and earns no "Indirect modification of overloaded element"
	// notice -- and `$map[$o] = &$x` replaces the entry with the box the
	// program named, rather than writing through it. Only an explicit `&`
	// leaves the `&` var_dump prints.
	`$map = new WeakMap;
$a = new stdClass; $b = new stdClass;
echo "### indirect\n";
$map[$a] = [];
$map[$a][] = 42;
$map[$b] = 41;
$map[$b]++;
var_dump($map[$a], $map[$b]);
var_dump($map);
echo "### by-reference\n";
$n = 1;
$map[$b] = 0;
$map[$b] =& $n;
$n++;
var_dump($map[$b], $map->offsetGet($b));
var_dump($map);
echo "### still-a-map\n";
var_dump(count($map), isset($map[$a]), isset($map[new stdClass]));
$map->offsetUnset($a);
var_dump(count($map));
echo "### end\n";`,
	// get_defined_vars answers with the variables that have a *value*. At global
	// scope every compiled variable is bound to its entry in the globals table,
	// so a name the program only ever mentions -- a `$$var` that was never
	// assigned, a foreach variable before the loop -- is a reference holding
	// undef, and is not defined.
	`$key = "asdf";
foreach (get_defined_vars() as $key => $value) { unset($$key); }
var_dump(@$key);
echo "### a name only mentioned is not defined\n";
function mentions() {
  $set = 1;
  if (false) { $never = 2; }
  return array_keys(get_defined_vars());
}
var_dump(mentions());
echo "### and one bound by reference to nothing is not either\n";
function bound() {
  $f = function () use (&$u) { };
  return array_keys(get_defined_vars());
}
var_dump(bound());
echo "### end\n";`,
	// `@` does not install a fixed mask: it narrows the *current* level to the
	// ones no setting can hide. So `error_reporting(E_WARNING)` answers 0
	// inside a suppressed call, not the mask.
	`foreach ([E_ALL, E_WARNING, E_NOTICE, 0, E_ERROR | E_WARNING] as $level) {
  error_reporting($level);
  printf("%d -> %d\n", $level, @error_reporting());
}
error_reporting(E_ALL);
echo "### nested, and restored afterwards\n";
function inner() { return error_reporting(); }
var_dump(@inner(), error_reporting());
error_reporting(E_WARNING);
var_dump(@inner(), error_reporting());
error_reporting(E_ALL);
echo "### end\n";`,
	// A private method is per-class, and the calling scope's own wins whatever
	// the object's class resolved to -- a subclass that overrides a private
	// method carries ZEND_ACC_CHANGED, which sends the engine to
	// `zend_get_parent_private_method` and that asks only the scope.
	`class PA {
 public function __construct() { $this->foo(); }
 private function foo() { echo "PA::foo\n"; } }
class PB extends PA { public function foo() { echo "PB::foo\n"; } }
class PC extends PA { protected function foo() { echo "PC::foo\n"; } }
class PD extends PA { private function foo() { echo "PD::foo\n"; } }
new PA; new PB; new PC; new PD;
echo "### from outside the scope the override answers\n";
(new PB)->foo();
echo "### ordinary polymorphism is untouched\n";
class QA { public function m() { echo "QA::m\n"; } public function call() { $this->m(); } }
class QB extends QA { public function m() { echo "QB::m\n"; } }
(new QB)->call();
echo "### end\n";`,
	// An offset a string merely disapproves of earns a *warning* first, and an
	// error handler that throws over it is what reaches the program: the
	// refusal the write would otherwise have earned never happens.
	`set_error_handler(function ($severity, $message) { throw new Exception($message); });
$x = "foo";
try { $y = &$x["2bar"]; } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
try { $x["2bar"] = "z"; } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
restore_error_handler();
echo "### and without a handler the refusal stands\n";
try { $z = &$x["2bar"]; } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
echo "### end\n";`,
	// A hook is a frame in a backtrace, named for the property rather than for
	// a method -- `H->$q::get()` -- and named after the class that *declared*
	// it, not the instance's.
	//
	// Only get hooks here: php2eval's engine bails on any *write* through a set
	// hook, so the set side and `#[\\SensitiveParameter]` on its parameter are
	// covered by `property_hooks/parameter_attributes` instead. See TODO.
	`class HGet { public $q { get { throw new Exception("g"); } } }
try { $x = (new HGet)->q; } catch (Throwable $e) { echo $e->getTraceAsString(), "\n"; }
echo "### the declaring class, not the instance's\n";
class HParent { public $r { get { throw new Exception("r"); } } }
class HChild extends HParent {}
try { $x = (new HChild)->r; } catch (Throwable $e) { echo $e->getTraceAsString(), "\n"; }
echo "### a hook that returns normally leaves no frame behind\n";
class HQuiet { public $t = 1 { get => $this->t + 1; } }
var_dump((new HQuiet)->t);
function whereAmI() { return count(debug_backtrace()); }
var_dump(whereAmI());
echo "### end\n";`,
	// var_export runs the get hooks, includes a virtual property that has no
	// slot to read, and writes every entry under its *plain* name -- so two
	// classes' private properties of one name are two entries, while an
	// override of a public one is a single entry answered by the most derived
	// declaration.
	`class ET { public $a = "a"; public $v { get => "V"; }
 private $p = "p"; protected $q = "q";
 public $b = "b" { get => strtoupper($this->b); set { $this->b = $value; } }
 public $h = "h" { get => "T:" . $this->h; } }
class EC extends ET { private $p = "cp"; public $h = "h" { get => "C:" . $this->h; } }
var_export(new ET); echo "\n";
var_export(new EC); echo "\n";
echo "### plain objects are unchanged\n";
class EPlain { public $x = 1; private $y = 2; protected $z = 3; }
var_export(new EPlain); echo "\n";
var_export([1, "k" => new EPlain]); echo "\n";
var_export((object) ["k" => 1]); echo "\n";
echo var_export(5, true), "\n";
echo "### a walk asks as the class it is in\n";
class WT { private $w = "t" { get => "T:" . $this->w; }
 public function walk() { return get_object_vars($this); } }
class WC extends WT { private $w = "c" { get => "C:" . $this->w; }
 public function walkChild() { return get_object_vars($this); } }
$o = new WC;
var_dump($o->walk(), $o->walkChild(), get_object_vars($o));
echo "### end\n";`,
	// The enumerations that run hooks walk the properties in the order the
	// *names* were first declared -- the parent's first, with a subclass's
	// override keeping the position it overrides -- which is not the order the
	// slots are in: a hooked property that overrides a plain one does not keep
	// the slot it overrides.
	`class HT { public $a = "a"; public $v { get => "V"; }
 public $b = "b" { get => strtoupper($this->b); set { $this->b = $value; } } }
class HC extends HT { public $a = "a" { get => strtoupper(parent::$a::get()); } }
$c = new HC;
var_dump(get_object_vars($c));
foreach ($c as $k => $x) { echo "$k=$x\n"; }
echo json_encode($c), "\n";
var_dump((array) $c);
var_dump($c);
echo "### a subclass that adds names appends them\n";
class HD extends HT { public $z = "z" { get => strtoupper($this->z); } public $w = "w"; }
$d = new HD;
var_dump(get_object_vars($d));
foreach ($d as $k => $x) { echo "$k=$x\n"; }
echo "### end\n";`,
	// eval is a stack frame like an include, and its own name is `eval` with no
	// argument -- what it ran is not a path, and the trace does not repeat the
	// source. An include carries the path it resolved.
	`function inner() {
  foreach (debug_backtrace() as $i => $f) {
    printf("#%d %s line=%s args=%d\n", $i, $f["function"], $f["line"] ?? "-", count($f["args"] ?? []));
  } }
function outer() { eval("inner();"); }
outer();
echo "### and in an exception's trace\n";
function thrower() { throw new Exception("x"); }
function viaEval() { eval("thrower();"); }
try { viaEval(); } catch (Throwable $e) {
  foreach ($e->getTrace() as $i => $f) { printf("#%d %s\n", $i, $f["function"]); }
}
echo "### end\n";`,
	// The read half of a compound assignment is taken *after* the diagnostic
	// the missing property earns, because the slot is made before the report
	// goes out and an error handler can write to it.
	`#[\AllowDynamicProperties] class Handled {
 function error($_, $msg) { echo $msg, "\n"; $this->a = 12345; } }
$c = new Handled;
set_error_handler([$c, "error"]);
$c->a %= 10;
restore_error_handler();
var_dump($c->a);
echo "### and one that leaves it alone\n";
#[\AllowDynamicProperties] class Quiet { function error($_, $msg) { echo $msg, "\n"; } }
$q = new Quiet;
set_error_handler([$q, "error"]);
$q->b .= "tail";
restore_error_handler();
var_dump($q->b);
echo "### end\n";`,
	// A frame's compiled variables go when the frame does, exception in flight
	// or not: the engine frees them in the leave helper, *after* the frame is
	// off the stack. So a destructor among them runs before the caller's catch
	// body, and its own stack frame is attributed to the call site rather than
	// to the callee.
	`class Loud { public $n;
 function __construct($n) { $this->n = $n; }
 function __destruct() { echo "dtor{$this->n}\n"; } }
function leaves($n) { $local = new Loud($n); throw new Exception("from $n"); }
echo "### locals go first\n";
try { leaves(1); } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### and a destructor that throws is caught where it ran\n";
class Angry { function __destruct() {
  try { throw new Exception("inner"); } catch (Exception $e) { echo "inner caught\n"; } } }
function alsoLeaves() { $a = new Angry; throw new Exception("outer"); }
try { alsoLeaves(); } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### a constructor that throws gives up its object\n";
class Holds { public $held; function __construct() { $this->held = new Loud(2); throw new Exception("ctor"); } }
try { $h = new Holds; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### the trace keeps arguments, not the receiver\n";
class Keeper { public $id;
 function __construct($id) { $this->id = $id; }
 function __destruct() { echo "keeper{$this->id} gone\n"; } }
function takes($o) { throw new Exception("held"); }
function wrap($id) { $o = new Keeper($id); takes($o); }
$trace = null;
try { wrap(9); } catch (Exception $e) { echo "caught\n"; $trace = $e->getTrace(); }
var_dump($trace[0]["args"][0]->id);
echo "before unset\n";
unset($trace);
echo "after unset\n";
echo "### end\n";`,
	// A destructor can drop the last name on something else, and the second
	// object's destructor runs before the opline that set off the first one
	// finishes -- the engine frees it at once, by refcount. php2go decides what
	// to collect from a walk taken *before* any destructor ran, so it has to
	// walk again once one has.
	`class Node { public $n; public $next;
 function __construct($n) { $this->n = $n; }
 function __destruct() { echo "d{$this->n}\n"; $this->next = null; } }
echo "### chain\n";
$a = new Node(1); $a->next = new Node(2); $a->next->next = new Node(3);
$a = null;
echo "after\n";
echo "### through-a-global\n";
class Holder { public $value; }
class Dropper { public $n;
 function __construct($n) { $this->n = $n; }
 function __destruct() { global $box; echo "x{$this->n}\n"; if (isset($box)) $box->value = null; } }
$box = new Holder;
$box->value = new Dropper(1);
$keep = new Dropper(2);
$box->value = $keep;
unset($keep);
$box->value = null;
echo "after\n";
$box = null;
echo "### end\n";`,
	// Leaving a loop by exception releases what the loop was walking, at that
	// point rather than at the end of the request. The engine frees every
	// temporary whose live range covers the throw and not the handler --
	// `cleanup_live_vars(execute_data, op_num, try_catch->catch_op)` -- and a
	// foreach's iterator is one of them, so a generator is force-closed and its
	// `finally` runs before the catch body does.
	`function gen() { try { yield 1; yield 2; } finally { echo "gen finally\n"; } }
echo "### caught outside\n";
try {
  foreach (gen() as $v) { echo "v$v\n"; throw new Exception("boom"); }
} catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### caught inside the loop\n";
foreach (gen() as $v) {
  try { throw new Exception("inner"); }
  catch (Exception $e) { echo "inner catch, loop alive\n"; }
  break;
}
echo "### through a finally\n";
function walk() {
  try {
    foreach (gen() as $v) { throw new Exception("deep"); }
  } finally { echo "walk finally\n"; }
}
try { walk(); } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### an object the loop was walking\n";
class Held implements IteratorAggregate {
 public function getIterator(): Iterator { return new ArrayIterator([1, 2]); }
 public function __destruct() { echo "Held destructed\n"; } }
try {
  foreach (new Held as $v) { throw new Exception("held"); }
} catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "### end\n";`,
	// When the thing a foreach was walking is let go. The engine releases it at
	// ZEND_FE_FREE, so a destructor on it runs at the end of the loop rather
	// than at the end of the request -- and where the exception it throws is
	// considered to have been thrown is *not* where that free sits. A `break`
	// or `return` leaving the loop emits its own free inside whatever try the
	// jump was written in, and the engine moves the throw to the loop's own
	// final free, which is outside it.
	`class FD implements IteratorAggregate {
 public function __construct(public $n) {}
 public function getIterator(): Iterator { return new ArrayIterator([1, 2]); }
 public function __destruct() { throw new Exception("dtor{$this->n}"); } }
echo "### ran-out\n";
try {
  foreach (new FD(1) as $v) { echo "v$v\n"; }
  echo "after\n";
} catch (Exception $e) { echo "outer: ", $e->getMessage(), "\n"; }
echo "### break-2\n";
function twoDeep() {
  foreach ([0] as $_) {
    try {
      foreach (new FD(2) as $v) {
        try { break 2; }
        catch (Exception $e) { echo "inner: ", $e->getMessage(), "\n"; }
        finally { echo "finally1\n"; }
      }
    } catch (Exception $e) { echo "outer: ", $e->getMessage(), "\n"; }
    finally { echo "finally2\n"; }
  }
}
twoDeep();
echo "### return\n";
function ret() {
  try {
    foreach (new FD(3) as $v) { return "returned"; }
  } catch (Exception $e) { return "caught: " . $e->getMessage(); }
}
var_dump(ret());
echo "### end\n";`,
	// A `catch (E)` that binds no variable drops the exception the moment it
	// catches it -- `OBJ_RELEASE(exception)` -- so a destructor on the
	// exception runs there, and what it throws takes the place of the catch
	// body rather than following it.
	`class Boom extends Exception {
 public function __destruct() { echo "dtor\n"; throw new RuntimeException("from dtor"); } }
echo "### novar\n";
try {
  try { throw new Boom("first"); }
  catch (Exception) { echo "unreachable catch\n"; }
  echo "unreachable fallthrough\n";
} catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
echo "### with-var\n";
class Quiet extends Exception { public function __destruct() { echo "quiet dtor\n"; } }
try { throw new Quiet("held"); } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "still here\n";
echo "### end\n";`,
	// When a weak reference goes empty. The engine notifies weak references
	// when the target is *freed*, not when a walk decides it is unreachable,
	// and those are two different moments: a destructor that stores `$this`
	// somewhere reachable resurrects the object, so nothing is freed and the
	// reference stands. Freeing is also what drops what the object was
	// holding, so a destructor that runs later in the same sweep already sees
	// a reference to the earlier object cleared.
	`class R { public $self; public $n;
 function __construct($n){ $this->n = $n; $this->self = $this; }
 function __destruct(){ global $keep; $keep = $this; echo "D{$this->n}\n"; } }
echo "### resurrect\n";
$w = WeakReference::create(new R(1));
gc_collect_cycles();
var_dump($w->get() !== null);
gc_collect_cycles();
var_dump($w->get() !== null);
var_dump($keep instanceof R);
echo "### freed-first\n";
class Holder { public $h; }
class Watcher { public $w; function __destruct(){ var_dump($this->w->get() === null); } }
$h = new Holder; $wt = new Watcher;
$wt->w = WeakReference::create($h); $h->h = $wt;
$h = null; $wt = null; gc_collect_cycles();
echo "### end\n";`,
	// Comparing an object. Two of the same class are compared property by
	// property; two of different classes are *uncomparable*, which PHP
	// expresses by calling the object greater whichever side it is on. Beside a
	// string an object with __toString is rendered first; beside a number it is
	// not, and converts to 1 with a notice instead. An object outranks an array.
	//
	// usort over a mixed array is left out: which pairs the sort compares is
	// its own business, and the notice above makes that observable.
	`class A { public $a; }
class B extends A { public $b; }
class C { public $x = 1; }
class D { public $x = 1; }
$o = new stdClass;
echo "### object-vs-scalar\n";
foreach ([["", '""'], [0, '0'], [1, '1'], [null, 'null'], [true, 'true'], [false, 'false'], [[1], 'array']] as [$v, $n]) {
  echo "-- $n\n";
  try { var_dump($o == $v, $o != $v, $o < $v, $o > $v, $v < $o, $v > $o); }
  catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
}
echo "### object-vs-object\n";
$p = new B(); $p->a = 0; $p->b = 1;
$q = new B(); $q->a = 1; $q->b = 0;
var_dump($p < $q, $p > $q, $p == $q, $p <=> $q);
$c = new C; $d = new D;
var_dump($c == $d, $c === $d, $c <=> $d);
$c2 = new C;
var_dump($c == $c2, $c === $c2, $c === $c);
echo "### spaceship\n";
var_dump($o <=> "", "" <=> $o, $o <=> 1, 1 <=> $o);
echo "### end\n";`,
	`$o = new stdClass;
echo "### spaceship-int\n"; var_dump($o <=> 1);
echo "### spaceship-int-rev\n"; var_dump(1 <=> $o);
echo "### lt-int\n"; var_dump($o < 1);
echo "### gt-int\n"; var_dump($o > 1);
echo "### eq-int\n"; var_dump($o == 1);
echo "### spaceship-str\n"; var_dump($o <=> "");
echo "### spaceship-str-rev\n"; var_dump("" <=> $o);
echo "### null\n"; var_dump($o == null, $o != null, $o < null, $o > null, null < $o, null > $o);
echo "### tostring\n";
class S { public $v; function __construct($v) { $this->v = $v; } function __toString(): string { return $this->v; } }
$s = new S("b");
var_dump($s == "b", $s < "c", $s > "a", "a" < $s, $s <=> "b", $s <=> "c");
echo "### tostring-num\n"; var_dump($s == 1, $s <=> 1);
echo "### end\n";`,
	// var_dump's & marks a slot holding a reference that more than one name
	// shares. php2go boxes slots into references for its own write paths too,
	// and those are invisible; only a reference the program asked for counts,
	// and only while a second name still holds it -- which is why unset(), a
	// frame ending, and a by-reference foreach moving on all take one away.
	`echo "### element\n";
$a = [1,2]; $r = &$a[0]; var_dump($a);
echo "### element-unset\n";
$b = [1,2]; $s = &$b[0]; unset($s); var_dump($b);
echo "### property\n";
class C { public $p = 1; }
$o = new C; $pr = &$o->p; var_dump($o);
echo "### property-unset\n";
$o2 = new C; $pr2 = &$o2->p; unset($pr2); var_dump($o2);
echo "### local-died\n";
function bind(array &$t) { $x = &$t[0]; $x = 9; }
$c = [1,2]; bind($c); var_dump($c);
echo "### foreach\n";
$d = [1,2,3]; foreach ($d as &$v) {} var_dump($d);
echo "### foreach-unset\n";
$e = [1,2,3]; foreach ($e as &$w) {} unset($w); var_dump($e);
echo "### foreach-in-function\n";
function walk(array &$t) { foreach ($t as &$q) { $q++; } }
$f = [1,2]; walk($f); var_dump($f);
echo "### nested\n";
$g = [[1]]; $h = &$g[0]; var_dump($g);
echo "### copy\n";
$i = [1]; $j = &$i[0]; $k = $i; var_dump($k);
echo "### write-path\n";
class D { public $q = 1; }
$m = new D; $m->q = 5; var_dump($m);
$n = [1]; $n[0] = 7; var_dump($n);
echo "### end\n";`,
	// The three binary bitwise operators work on bytes when both operands are
	// strings, and on integers otherwise. AND and XOR stop at the shorter
	// operand; OR keeps the longer one's tail. ~ follows the same rule.
	//
	// The operands travel through variables on purpose: the compiler folds a
	// constant one, and a folded result with a byte above 0x7f cannot be
	// carried by the IR, whose literals are a protobuf string field.
	`$pairs = [["abc","def"],["Hello","World"],["abc","de"],["de","abc"],["",""],["a",""],
	           ["","a"],["0123","abcd"]];
	 foreach ($pairs as $p) {
	   [$x, $y] = $p;
	   var_dump(bin2hex($x & $y), bin2hex($x | $y), bin2hex($x ^ $y), bin2hex(~$x));
	 }
	 $n = 1.5; $arr = [1];
	 foreach ([fn() => "abc" & 5, fn() => 5 & "abc", fn() => ~$n, fn() => ~$arr] as $try) {
	   try { var_dump($try()); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 var_dump("12" & "34", "abc" & "5");`,
	// Outside the object a private property's key carries its declaring class
	// and a protected one a star, both between NUL bytes -- which is how two
	// classes in a hierarchy keep same-named private properties apart.
	`class P { public $pub = 1; protected $prot = 2; private $priv = 3; }
	 class Q extends P { private $qpriv = 4; public $qpub = 5; }
	 foreach ([(array)(new Q), get_mangled_object_vars(new Q)] as $table) {
	   foreach ($table as $k => $v) { echo str_replace("\0", "@", $k), " => "; var_dump($v); }
	 }
	 var_dump(get_object_vars(new Q), json_encode(new Q));
	 echo str_replace("\0", "@", serialize(new Q)), "\n";
	 foreach (new Q as $k => $v) { echo str_replace("\0", "@", $k), "=", $v, " "; }
	 echo "\n";`,
	// `yield from` proxies: a delegating generator's current() and key() read
	// the *inner* generator live, so two delegates over one source interleave
	// rather than each seeing what it last copied out.
	`function upto($n) { foreach (range(0, $n) as $v) yield $v; }
	 function passthru2($g) { yield from $g; }
	 foreach ([0, 1, 2] as $levels) {
	   echo "levels $levels\n";
	   $all = []; $gens = [];
	   $all[] = $gens[0][0] = upto(2 << $levels);
	   for ($level = 1; $level < $levels; $level++) {
	     for ($i = 0; $i < (1 << $level); $i++) {
	       $all[] = $gens[$level][$i] = passthru2($gens[$level - 1][$i >> 1]);
	     }
	   }
	   while (1) {
	     foreach ($all as $g) {
	       var_dump($g->current());
	       $g->next();
	       if (!$g->valid()) { break 2; }
	     }
	   }
	 }
	 // Keys proxy too, and a chain of delegations reaches the innermost.
	 function keyed() { yield "a" => 1; yield "b" => 2; }
	 $chain = passthru2(passthru2(keyed()));
	 foreach ($chain as $k => $v) { echo $k, "=", $v, "\n"; }`,
	// Destructuring, including the by-reference form -- which has to be checked
	// *twice over*, since the second run of the same opline is where a stale
	// reference in the temporary shows up.
	`$srcs = [[1,2,3], ["a"=>1,"b"=>2], [], [1], [[1,2],[3,4]], "str", 42, null, [1,2,[3,4]]];
	 $names = ["[1,2,3]","['a'=>1,'b'=>2]","[]","[1]","[[1,2],[3,4]]","'str'","42","null","[1,2,[3,4]]"];
	 foreach ($srcs as $i => $src) {
	   echo "### list3 ", $names[$i], "\n";
	   try { [$a, $b, $c] = $src; echo "= ", var_export([$a,$b,$c], true), "\n"; }
	   catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	   echo "### keyed ", $names[$i], "\n";
	   try { ["a"=>$x, "b"=>$y] = $src; echo "= ", var_export([$x,$y], true), "\n"; }
	   catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	   echo "### nested ", $names[$i], "\n";
	   try { [[$p, $q], $r] = $src; echo "= ", var_export([$p,$q,$r], true), "\n"; }
	   catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	   echo "### byref ", $names[$i], "\n";
	   try { $copy = $src; [&$r1] = $copy; $r1 = "CHANGED"; echo "= ", var_export($copy, true), "\n"; }
	   catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 foreach ([[1,2],[3,4]] as [$m, $n]) { echo "= $m$n\n"; }
	 ["k" => ["j" => $deep]] = ["k" => ["j" => "found"]]; var_export($deep); echo "\n";
	 for ($i = 0; $i < 3; $i++) { $g = [9, 9]; [&$rr] = $g; $rr = "D"; var_export($g); echo "\n"; }
	 for ($i = 0; $i < 3; $i++) { $h = [1, 2]; $e = &$h[1]; $e = "Z"; var_export($h); echo "\n"; }`,
	// The array-key matrix: what each shape of key is coerced to, and what the
	// append slot becomes afterwards.
	`$keys = [0, 1, -1, 2.7, -2.7, 1.0, "0", "1", "-1", "01", "1.0", "1e2", " 1", "1 ", "abc", "",
	          null, true, false, "9223372036854775807", "9223372036854775808", "-0", "0x1A", "0b1"];
	 $names = ["0","1","-1","2.7","-2.7","1.0","'0'","'1'","'-1'","'01'","'1.0'","'1e2'","' 1'","'1 '","'abc'","''",
	           "null","true","false","'INTMAX'","'INTMAX+1'","'-0'","'0x1A'","'0b1'"];
	 foreach ($keys as $i => $k) {
	   echo "### key ", $names[$i], "\n";
	   $a = [];
	   try { $a[$k] = "v"; } catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; continue; }
	   foreach ($a as $kk => $vv) { echo "= stored ", var_export($kk, true), " ", gettype($kk), "\n"; }
	   echo "= read ", var_export($a[$k] ?? "MISSING", true),
	        " isset ", var_export(isset($a[$k]), true),
	        " ake ", var_export(array_key_exists($k, $a), true), "\n";
	 }
	 // The first integer key sets the append slot whatever its sign (PHP 8.3),
	 // and the slot only rises afterwards.
	 $b = [5 => "a"]; $b[] = "b"; $b["7"] = "c"; $b[] = "d"; var_export($b); echo "\n";
	 $c = [-5 => "a"]; $c[] = "b"; var_export($c); echo "\n";
	 $d = [-5 => "a", -9 => "b"]; $d[] = "c"; var_export($d); echo "\n";
	 $e = ["x" => 1]; $e[] = "y"; var_export($e); echo "\n";
	 var_export(["10" => 1, 9 => 2, "x" => 3, 1.9 => 4, true => 5]); echo "\n";`,
	// The string-offset matrix: read, isset/empty and write for every shape of
	// index. The three operations disagree with each other more than they
	// agree, which is the point of checking all of them together.
	`$idx = [0, 1, 4, 5, -1, -5, -6, 2.7, "0", "1", "abc", "1abc", "", null, true, false];
	 $inames = ["0","1","4","5","-1","-5","-6","2.7","'0'","'1'","'abc'","'1abc'","''","null","true","false"];
	 foreach ($idx as $i => $k) {
	   $s = "hello";
	   echo "### read ", $inames[$i], "\n";
	   try { echo "= ", var_export($s[$k], true), "\n"; }
	   catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	   echo "### isset ", $inames[$i], "\n";
	   try { echo "= ", var_export(isset($s[$k]), true), " ", var_export(empty($s[$k]), true), "\n"; }
	   catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	   echo "### write ", $inames[$i], "\n";
	   try { $s[$k] = "Z"; echo "= ", var_export($s, true), "\n"; }
	   catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 $g = "ab"; $g[5] = "X"; var_export($g); echo "\n";
	 $h = "ab"; @($h[0] = "XY"); var_export($h); echo "\n";`,
	// The cast matrix. INF and NAN are spelled PHP's way wherever a float is
	// rendered, which Go's formatter does not do.
	`class CastStr { function __toString(): string { return "obj"; } }
	 $vals = [0, 1, -1, 2.5, -0.0, INF, NAN, "", "0", "1", "abc", "1abc", " 42 ", "0x1A", "1e3",
	          null, true, false, [], [1, 2], ["k" => "v"], new CastStr];
	 $names = ["0","1","-1","2.5","-0.0","INF","NAN","''","'0'","'1'","'abc'","'1abc'"," ' 42 '","'0x1A'","'1e3'",
	           "null","true","false","[]","[1,2]","['k'=>'v']","CastStr"];
	 foreach ($vals as $i => $v) {
	   foreach (["int", "float", "string", "bool", "array"] as $t) {
	     echo "### (", $t, ") ", $names[$i], "\n";
	     try {
	       $r = match($t) { "int" => (int) $v, "float" => (float) $v, "string" => (string) $v,
	                        "bool" => (bool) $v, "array" => (array) $v };
	       echo "= ", var_export($r, true), "\n";
	     } catch (Throwable $e) { echo "! ", get_class($e), ": ", $e->getMessage(), "\n"; }
	   }
	   echo "### types ", $names[$i], "\n";
	   echo "= ", gettype($v), " ", get_debug_type($v), " ",
	        var_export(is_numeric($v), true), " ", var_export(empty($v), true), "\n";
	 }`,
	// The arithmetic matrix, with the operands printed before each result so a
	// *diagnostic* is attributed to the pair that produced it. Conversion is
	// strictly left to right: an operand that cannot convert stops the walk, so
	// a later one never warns, and an earlier one's warning survives the
	// refusal.
	`$vals = [2.5, "", "abc", "2abc", null, true, "10", "1e2", 7];
	 $names = ["2.5","''","'abc'","'2abc'","null","true","'10'","'1e2'","7"];
	 foreach ($vals as $i => $a) {
	   foreach ($vals as $j => $b) {
	     foreach (["%", "+", "-", "*"] as $op) {
	       echo "### ", $names[$i], " ", $op, " ", $names[$j], "\n";
	       try {
	         $r = match($op) { "+" => $a + $b, "-" => $a - $b, "*" => $a * $b, "%" => $a % $b };
	         echo "= ", var_export($r, true), "\n";
	       } catch (Throwable $t) { echo "! ", get_class($t), ": ", $t->getMessage(), "\n"; }
	     }
	   }
	 }`,
	// The comparison matrix. Every pairing of a representative value set under
	// every operator -- 1734 answers -- which is how PHP 8's "saner string to
	// number comparisons" and null's string-ness were found to be wrong.
	`$vals = [0, 1, -1, 0.0, 1.5, "", "0", "1", "abc", "1abc", null, true, false, [], [1], "10", "1e1"];
	 $names = ["0","1","-1","0.0","1.5","''","'0'","'1'","'abc'","'1abc'","null","true","false","[]","[1]","'10'","'1e1'"];
	 foreach ($vals as $i => $a) {
	   foreach ($vals as $j => $b) {
	     foreach (["==", "===", "<", "<=", ">", "<=>"] as $op) {
	       $r = match($op) { "==" => $a == $b, "===" => $a === $b, "<" => $a < $b,
	                         "<=" => $a <= $b, ">" => $a > $b, "<=>" => $a <=> $b };
	       echo $names[$i], " ", $op, " ", $names[$j], " = ", var_export($r, true), "\n";
	     }
	   }
	 }`,
	// __sleep selects which properties are written, in its own order, and names
	// one that does not exist at its peril.
	`class Sleeps {
	   public $a = 1; public $b = 2; private $c = 3;
	   public function __sleep() { echo "sleep "; return ["c", "a", "nope"]; }
	   public function __wakeup() { echo "wakeup "; $this->b = 99; }
	 }
	 $written = @serialize(new Sleeps);
	 var_dump($written);
	 var_dump(unserialize($written));`,
	// `instanceof` follows the whole graph: an interface has parents of its
	// own, so implementing Iterator makes a class Traversable. Composing a
	// trait is not a subtype relation.
	`interface Deep {} interface Mid extends Deep {} interface Top extends Mid {}
	 class Chain implements Top {}
	 trait NotAType {}
	 class Uses { use NotAType; }
	 $c = new Chain;
	 var_dump($c instanceof Top, $c instanceof Mid, $c instanceof Deep,
	          new Uses instanceof Uses);
	 class Walker implements Iterator {
	   private $i = 0; private $d = ["x", "y"];
	   public function rewind(): void { echo "rewind "; $this->i = 0; }
	   public function valid(): bool { echo "valid "; return $this->i < count($this->d); }
	   public function current(): mixed { echo "current "; return $this->d[$this->i]; }
	   public function key(): mixed { echo "key "; return $this->i; }
	   public function next(): void { echo "next "; $this->i++; }
	 }
	 var_dump(new Walker instanceof Traversable);
	 function wants(Traversable $t) { return "accepted"; }
	 var_dump(wants(new Walker));
	 var_dump(iterator_to_array(new Walker), iterator_count(new Walker),
	          iterator_to_array(new Walker, false));
	 var_dump(iterator_to_array([1, 2]), iterator_to_array(["a" => 1], false));`,
	// The magic accessors, and the two rules that are easy to miss: `??` asks
	// __isset before __get, and a write-mode fetch of an overloaded property
	// warns unless what __get returned is an object.
	`class Overload {
	   private $d = ["a" => 1];
	   public function __get($n) { echo "get($n) "; return $this->d[$n] ?? null; }
	   public function __set($n, $v) { echo "set($n) "; $this->d[$n] = $v; }
	   public function __isset($n) { echo "isset($n) "; return isset($this->d[$n]); }
	   public function __unset($n) { echo "unset($n) "; unset($this->d[$n]); }
	 }
	 $o = new Overload;
	 var_dump($o->a, $o->a ?? "def", $o->zz ?? "def", isset($o->a), isset($o->zz));
	 $o->b = 2; var_dump($o->b);
	 unset($o->a); var_dump(isset($o->a), $o->a ?? "gone");
	 @($o->arr[] = 1);
	 $o->obj = new stdClass;
	 $o->obj->x = 5;
	 var_dump($o->obj->x);`,
	// __toString is what makes an object a string, everywhere a string is
	// wanted: echo, a cast, concatenation, interpolation. A class without one
	// is an Error rather than an empty string, and a body that returns nothing
	// fails the implicit `string` return type.
	`class Str { function __toString(): string { return "S!"; } }
	 class NoStr {}
	 class Empty2 { function __toString() {} }
	 $s = new Str;
	 echo $s, "\n", (string) $s, "\n", $s . "x", "\n";
	 var_dump("$s", "a{$s}b", strlen($s), $s == "S!");
	 foreach ([fn() => print(new NoStr), fn() => (string) (new NoStr),
	           fn() => (new NoStr) . "", fn() => "x" . (new Empty2)] as $try) {
	   try { $try(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A by-reference foreach over an object binds the object's own slots, skips
	// what is uninitialized, and refuses to alias a readonly property.
	`class IterProps {
	   public int $a = 1;
	   public readonly int $ro;
	   public int $b = 2;
	   public function fill() { $this->ro = 9; }
	 }
	 $o = new IterProps;
	 foreach ($o as $k => &$v) { $v *= 10; }
	 unset($v);
	 var_dump($o->a, $o->b);
	 $o->fill();
	 try { foreach ($o as &$v) {} } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 foreach ($o as $k => $v) { echo "$k=$v\n"; }`,
	// An attribute may only be written where its own #[Attribute] allows.
	`#[Attribute(flags: Attribute::TARGET_CLASS)] class OnlyClass {}
	 #[Attribute(Attribute::TARGET_ALL)] class Anywhere {}
	 #[OnlyClass] #[Anywhere] class Marked {}
	 #[Anywhere] function marked() {}
	 var_dump((new ReflectionClass("Marked"))->getAttributes()[0]->newInstance());
	 var_dump((new ReflectionFunction("marked"))->getAttributes()[0]->newInstance());
	 #[OnlyClass] function wrong() {}
	 try { (new ReflectionFunction("wrong"))->getAttributes()[0]->newInstance(); }
	 catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	// An object or an array cannot be an array offset, and the refusal is
	// worded differently inside isset/empty.
	`$arr = [1, 2, 3]; $obj = new stdClass; $fn = function () {};
	 foreach ([$obj, $fn, [1]] as $bad) {
	   try { $arr[$bad]; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	   try { $arr[$bad] = 1; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	   try { isset($arr[$bad]); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 }
	 // The offsets that *are* usable still behave.
	 var_dump($arr[1], $arr[true], $arr["1"], isset($arr[9]));`,
	// A property type is invariant: a redeclaration may only repeat it. A
	// trait contribution is compared on the type and readonly too.
	`interface PX {} interface PY {}
	 class PropBase { public PX&PY $p; public int $q; }
	 class PropSame extends PropBase { public PX&PY $p; public int $q; }
	 var_dump(new PropSame instanceof PropBase);
	 trait PT1 { public int $r; }
	 trait PT2 { public int $r; }
	 class PropTrait { use PT1, PT2; }
	 var_dump(get_class(new PropTrait));`,
	// class_alias will not take a reserved word, and says so before it looks
	// the aliased class up.
	`class Aliasable {}
	 var_dump(class_alias("Aliasable", "MyAlias"), new MyAlias instanceof Aliasable);`,
	// from()/tryFrom() check the argument against the backing type first, and
	// report the value as it was *after* coercion.
	`enum Suit: string { case H = "h"; case S = "s"; }
	 enum Num: int { case One = 1; }
	 var_dump(Suit::from("h"), Suit::tryFrom("nope"), Num::from(1));
	 foreach ([fn() => Suit::from([]), fn() => Num::from("x"), fn() => Suit::from("42"),
	           fn() => Num::tryFrom([])] as $try) {
	   try { $try(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 // Serializable is deprecated for any class that names it.
	 class OldSer implements Serializable {
	   public function serialize(): ?string { return ""; }
	   public function unserialize(string $data): void {}
	 }
	 var_dump(new OldSer instanceof Serializable);`,
	// A typed class constant is checked where it is *read*, since its
	// initializer may name something that only exists by then.
	`class TypedK {
	   public const string S = "s";
	   public const ?array A = null;
	   public const int|float N = 1;
	   public const string BAD = LATE_C;
	 }
	 var_dump(TypedK::S, TypedK::A, TypedK::N);
	 define("LATE_C", new stdClass);
	 try { var_dump(TypedK::BAD); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 // Read twice: the refusal is not cached away.
	 try { var_dump(TypedK::BAD); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// Intersection and DNF types. A value satisfies `(X&Y)|Z` when it satisfies
	// every atom of *some* alternative, and nothing coerces to an intersection.
	`interface IX {} interface IY {} interface IW {} interface IZ {}
	 class XY implements IX, IY {} class JustX implements IX {} class WZ implements IW, IZ {}
	 function dnf1(IX&IY $v) { return "1"; }
	 function dnf2((IX&IY)|int $v) { return "2"; }
	 function dnf3((IX&IY)|(IW&IZ) $v) { return "3"; }
	 function dnf4((IX&IY)|null $v) { return "4"; }
	 var_dump(dnf1(new XY), dnf2(new XY), dnf2(7), dnf3(new WZ), dnf3(new XY), dnf4(null));
	 foreach ([fn() => dnf1(new JustX), fn() => dnf2(new JustX), fn() => dnf2("s"),
	           fn() => dnf3(new JustX), fn() => dnf4(1)] as $try) {
	   try { $try(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 }
	 // Covariance over a DNF return type: narrowing to one alternative is fine.
	 class DnfBase { public function m(): (IX&IY)|IW { return new XY; } }
	 class DnfChild extends DnfBase { public function m(): IX&IY { return new XY; } }
	 var_dump((new DnfChild)->m() instanceof IX);`,
	// `use T { ... }`: an alias adds a *name* without removing the original,
	// `insteadof` takes one trait's version out of the running, and a visibility
	// clause changes how the method is reached.
	`trait TrA { public function a() { return "A::a"; } public function shared() { return "A"; } }
	 trait TrB { public function shared() { return "B"; } }
	 class UsesTraits {
	   use TrA, TrB { a as b; TrA::shared insteadof TrB; TrB::shared as fromB; }
	 }
	 $t = new UsesTraits;
	 var_dump($t->a(), $t->b(), $t->shared(), $t->fromB());
	 var_dump(method_exists($t, "b"), method_exists($t, "fromB"), is_callable([$t, "b"]));`,
	// Instantiating a class resolves its deferred initializers -- the property
	// defaults and constants the compiler could not fold -- and does it before
	// the object exists. The autoloader running is the observable part.
	`spl_autoload_register(function ($class) {
	   echo "load $class\n";
	   if ($class === "LateK") { class LateK { const C = "k"; } }
	 });
	 class UsesLate { public $p = LateK::C; const D = LateK::C . "!"; public $q = 1; }
	 var_dump(new UsesLate, UsesLate::D);
	 // Resolved once: a second instantiation autoloads nothing.
	 var_dump(new UsesLate);`,
	// `static` in a callable or a constant name is the *called* class, which an
	// inherited static method reached through a subclass makes different from
	// the executing one.
	`class Lsb1 {
	   static function probe() {
	     var_dump(is_callable("static::ok"), is_callable(["static", "ok"]),
	              is_callable("self::ok"), defined("static::K"), defined("self::K"));
	     if (defined("static::K")) { echo constant("static::K"), "\n"; }
	   }
	 }
	 class Lsb2 extends Lsb1 { const K = "k"; static function ok() {} }
	 Lsb1::probe(); Lsb2::probe();`,
	// WeakMap as a map: object keys, the pair-shaped debug output, and the
	// refusals for everything that is not an object. The *weakness* is not
	// exercised — php2go never collects, so a test that drops the key would
	// disagree, and the corpus covers that honestly instead.
	`$map = new WeakMap; var_dump(count($map), $map);
	 $k = new stdClass; $k->value = 1; $v = new stdClass; $v->value = 2;
	 $map[$k] = $v; var_dump(count($map), $map, isset($map[$k]), $map[$k]);
	 $map[$k] = 42; var_dump($map[$k], count($map));
	 unset($map[$k]); var_dump(count($map), isset($map[$k]));
	 foreach ([fn() => $map[1] = 2, fn() => $map[1], fn() => $map[] = 2] as $try) {
	   try { $try(); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 // The "not contained" message names the object *handle*, and the oracle's
	 // counter is ahead of a fresh request's -- so only its shape is compared.
	 try { $map[$k]; } catch (Throwable $t) {
	   echo get_class($t), ": ", str_starts_with($t->getMessage(), "Object stdClass#"),
	        str_ends_with($t->getMessage(), " not contained in WeakMap"), "\n";
	 }
	 $w = WeakReference::create($k); var_dump($w->get() === $k, $w instanceof WeakReference);
	 class Counted implements Countable { function count(): int { return 7; } }
	 var_dump(count(new Counted));`,
	// Output buffering. The *level* is relative: the oracle runs inside a
	// buffer of its own, so only the difference is comparable.
	`$base = ob_get_level();
	 echo "a"; ob_start(); echo "b";
	 var_dump(ob_get_level() - $base, ob_get_contents(), ob_get_length());
	 ob_start(); echo "c"; var_dump(ob_get_clean(), ob_get_level() - $base);
	 echo "d"; var_dump(ob_get_flush(), ob_get_level() - $base);`,
	// settype takes its subject by reference and uses the old type spellings.
	`$a = "12abc"; var_dump(settype($a, "integer"), $a);
	 $b = 1; var_dump(settype($b, "bool"), $b);
	 $c = "x"; var_dump(settype($c, "array"), $c);
	 $d = 3; var_dump(settype($d, "null"), $d);
	 $e = ["k" => 1]; var_dump(settype($e, "object"), $e);
	 $f = [1, 2]; var_dump(@settype($f, "string"), $f);
	 $g = new stdClass; var_dump(@settype($g, "int"), $g);
	 foreach (["nope", "resource"] as $bad) {
	   $h = 1;
	   try { settype($h, $bad); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// (int) and (float) of an object are a warning and the number 1.
	`$o = new stdClass;
	 var_dump(@(int) $o, @(float) $o, (bool) $o, (array) $o);
	 var_dump((object) "s", (object) 5, (object) null);`,
	// Resources are their own value type. The *id* is deliberately not printed:
	// it depends on how many handles the SAPI opened first, and the oracle runs
	// under embed while the .phpt corpus was recorded under the CLI.
	`$path = "/tmp/php2go-res-case.txt";
	 var_dump(file_put_contents($path, "one\ntwo\n"));
	 $r = fopen($path, "r");
	 var_dump(is_resource($r), gettype($r), get_debug_type($r), (bool) $r, $r == $r);
	 var_dump(fgets($r), feof($r), fgets($r), feof($r), fgets($r), feof($r));
	 var_dump(rewind($r), fread($r, 3), ftell($r));
	 var_dump(fclose($r), is_resource($r), gettype($r), get_debug_type($r));
	 var_dump(file_get_contents($path), file_exists($path), unlink($path), file_exists($path));
	 var_dump(@fopen("/no/such/php2go/file", "r"));`,
	// The slicing family, and what each does with the two kinds of key.
	`$a = [1, 2, 3, 4, 5];
	 var_dump(array_slice($a, 1, 3), array_slice($a, -2), array_slice($a, 1, -1),
	          array_slice(["x" => 1, 2, 3], 1, null, true), array_slice($a, 99));
	 $b = [1, 2, 3, 4, 5]; var_dump(array_splice($b, 1, 2, ["a", "b", "c"]), $b);
	 $c = [1, 2, 3]; var_dump(array_splice($c, -1), $c);
	 $d = ["k" => 1, 2, 3]; var_dump(array_splice($d, 0, 0, ["z"]), $d);
	 var_dump(array_unique([1, "1", 2, 2.0, "a", "a"]));
	 var_dump(array_merge([1, 2], ["k" => 3], [4]), array_merge());`,
	`$x = 3; $y = 1; $source = ["named" => &$x, 9 => &$y, "plain" => 2];
	 $merged = array_merge($source); $merged[0] = 10; $merged["named"] = 20;
	 var_dump($merged, $x, $y);`,
	// foreach over something that is neither an array nor an object warns and
	// runs zero times; it is not an error.
	`$n = null; foreach ($n as $v) { echo "no"; } echo "after\n";
	 $i = 5; foreach ($i as $k => $v) { echo "no"; } echo "after\n";
	 $s = "abc"; foreach ($s as &$v) { echo "no"; } echo "after\n";`,
	// The array functions that change a length in place. array_pop takes the
	// next-free key down with it, array_shift renumbers, and both leave string
	// keys where they were.
	`$a = [1, 2, 3]; var_dump(array_pop($a), $a); $a[] = 9; var_dump($a);
	 $b = ["x" => 1, 5 => 2, 3]; var_dump(array_shift($b), $b);
	 $c = [1]; var_dump(array_push($c, 2, 3), $c);
	 $d = ["k" => 1, 7 => 2]; var_dump(array_unshift($d, "z"), $d);
	 $e = []; var_dump(array_pop($e), array_shift($e), $e);
	 var_dump(array_fill(5, 3, "v"), array_fill_keys(["a", 2], 0));
	 try { array_fill(0, -1, 1); } catch (ValueError $err) { echo $err->getMessage(), "\n"; }`,
	// Which magic forwarder answers a call is decided by whether a $this is in
	// play, not by whether the syntax was static: `self::m()` and
	// `["C", "m"]` from inside a method both reach __call.
	`class Mg {
	   public function __call($n, $a) { return "call:$n"; }
	   public static function __callStatic($n, $a) { return "static:$n"; }
	   public function inside() {
	     return [self::gone(), Mg::gone(), call_user_func(["Mg", "gone"]),
	             call_user_func([$this, "gone"]), call_user_func("Mg::gone")];
	   }
	 }
	 var_dump((new Mg)->inside());
	 var_dump(Mg::gone(), call_user_func(["Mg", "gone"]), call_user_func("Mg::gone"));
	 var_dump(is_callable(["Mg", "gone"]), is_callable([new Mg, "gone"]));`,
	// `[$obj, "parent::m"]` names the scope the lookup starts from, and that
	// scope is the object's class rather than the calling function's. The form
	// is deprecated, and the deprecation is reported by the same check that
	// decides whether the callable is usable -- so it appears even when the
	// answer is no.
	`class CbA { public function f() { return "A::f"; }
	             private function hidden() { return "A::hidden"; } }
	 class CbB extends CbA { public function f() { return "B::f"; } }
	 class CbC extends CbB {
	   public function f() { return "C::f"; }
	   public function go() {
	     var_dump(call_user_func([$this, "parent::f"]));
	     try { call_user_func([$this, "parent::nope"]); }
	     catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	     try { call_user_func([$this, "parent::hidden"]); }
	     catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	   }
	 }
	 (new CbC)->go();
	 try { call_user_func(["NoSuchCb", "m"]); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { call_user_func([42, "m"]); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { call_user_func("self::m"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// Linking is where the engine finds out it has not seen a class: the
	// parent, the interfaces and the traits are all asked for, and so is any
	// class a variance check needs.
	`spl_autoload_register(function($c) {
	     echo "load $c\n";
	     if ($c === "AutoP") { class AutoP { function m(): AutoQ { return new AutoQ; } } }
	     if ($c === "AutoQ") { class AutoQ extends AutoP {} }
	 });
	 class AutoC extends AutoP { function m(): AutoQ { return new AutoQ; } }
	 var_dump(new AutoC instanceof AutoP);
	 try { new class extends NoSuchParent {}; } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	// Destructors run at the end of the request: the symbol table is torn down
	// in reverse insertion order first, then whatever objects are left. An
	// object whose constructor threw is never destructed, and neither is one
	// whose constructor was never reached because an argument to it threw.
	`class Dt { public $n; function __construct($n) { $this->n = $n; }
	            function __destruct() { echo "bye {$this->n}\n"; } }
	 class Boom { function __construct() { echo "ctor\n"; throw new Exception("no"); }
	              function __destruct() { echo "never\n"; } }
	 function bang() { throw new Exception("arg"); }
	 $first = new Dt(1); $second = new Dt(2);
	 try { new Boom; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
	 try { new Dt(bang()); } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
	 echo "end\n";`,
	// An override has to still work everywhere the original did. Only the
	// *accepted* forms can be checked here: an incompatible one is an
	// E_COMPILE_ERROR, which bails the engine out of eval before it can be
	// compared. The .phpt corpus covers the wording of the refusal.
	`interface Sig { public function m(int $a, string $b = 'xyz', ...$rest): int|float; }
	 class SigOk implements Sig { public function m(int|string $a, $b = 'xyz', ...$rest): int { return 1; } }
	 abstract class SigA { abstract protected function m(): static; }
	 class SigB extends SigA { protected function m(): static { return $this; } }
	 trait SigT { abstract public function m(array $a); }
	 class SigU { use SigT; public function m(iterable $a) {} }
	 var_dump((new SigOk)->m(1), (new SigB)->m() instanceof SigB);`,
	// PHP 8.4 property hooks.
	`class Hk { public $prop { get { return 42; } } }
	 $h = new Hk; var_dump($h->prop);
	 try { $h->prop = 0; } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	`class Bk { public int $p = 1 { get { return $this->p * 10; } set { $this->p = $value + 1; } } }
	 $b = new Bk; var_dump($b->p); $b->p = 5; var_dump($b->p);
	 $b->p += 1; var_dump($b->p); $b->p++; var_dump($b->p, $b->p--, $b->p);`,
	`class Pa { public $p = 1 { get { return $this->p * 2; } set { $this->p = $value; } } }
	 class Pb extends Pa { public $p { get { return parent::$p::get() + 100; }
	                                   set { parent::$p::set($value * 10); } } }
	 $x = new Pb; var_dump($x->p); $x->p = 3; var_dump($x->p);`,
	`class Vi { public $v { get => 5; } public $n { get => null; } }
	 $x = new Vi; var_dump($x->v, isset($x->v), isset($x->n), empty($x->v), empty($x->n), $x);
	 try { unset($x->v); } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	`class Im { public $prop { get { echo __METHOD__, "\n"; return $this->prop; } } }
	 $c = new Im; $c->prop = 42; var_dump($c->prop);`,
	// A write that reaches through a hooked property is refused unless what the
	// get hook returns is an object -- which can be modified without touching
	// the property -- or a slot, which only a `&get` hook hands out.
	`class Ind { public $arr = [] { set { $this->arr = $value; } }
	            public $obj { get { return $this->obj ??= new stdClass; } } }
	 $i = new Ind;
	 try { $i->arr[] = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 try { $r =& $i->arr; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 var_dump($i->arr);
	 $i->obj->x = 7; var_dump($i->obj->x);`,
	// Binding an existing variable to a typed property makes that variable
	// typed too, from the binding onwards: the declaration travels with the box.
	`class Bind { public string $x = "x"; public int $n = 1; }
	 $b = new Bind; $y = "y"; $b->x = &$y;
	 $z = 42; $y = $z; var_dump($y, $z, $b->x);
	 $w = "5"; $b->n = &$w; var_dump($w, $b->n);
	 try { $w = []; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($b);`,
	// __get answering for an unset typed property is held to that type, and the
	// engine words the failure as a return-value problem rather than a write.
	`class Get {
	   public int $v; public $plain; public string $s;
	   public function __get($n) { return $n === "s" ? [] : "42"; }
	 }
	 $g = new Get; unset($g->v, $g->plain, $g->s);
	 var_dump($g->v, $g->plain, $g->missing);
	 try { var_dump($g->s); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// ...but only where the caller could have seen the property. A private one
	// read from outside is none of that scope's business, so __get's answer
	// stands whatever type it has.
	`class Hid {
	   private int $p; protected int $q; public int $r;
	   public function __get($n) { return "42"; }
	   public function inside() { unset($this->p); var_dump($this->p); }
	 }
	 $h = new Hid; var_dump($h->p, $h->q);
	 unset($h->r); var_dump($h->r);
	 $h->inside();`,
	// The end of a request tears the symbol table down in reverse declaration
	// order, but an object a static still holds survives that and is destroyed
	// with the store afterwards -- in creation order.
	`class Sing {
	   public function __construct(public string $tag) { echo "made $tag\n"; }
	   public function __destruct() { echo "gone {$this->tag}\n"; }
	   private static $one = null;
	   public static function one() { return self::$one ??= new Sing("one"); }
	   public static function two() { static $it = null; return $it ??= new Sing("two"); }
	 }
	 $first = Sing::one();
	 $second = Sing::two();
	 $plain = new Sing("plain");
	 echo "end\n";`,
	// At global scope a compiled variable *is* the entry in the globals table,
	// so `$c =& $z` has to rebind that entry as well as the name.
	`$c = 5; $z = 1; $c =& $z;
	 var_dump($GLOBALS["c"], $c);
	 $z = 9; var_dump($GLOBALS["c"], $c);
	 function pull() { global $c; var_dump($c); $c = 77; }
	 pull(); var_dump($z, $c);
	 $arr = []; $arr[] =& $arr; $q = 1; $arr =& $q;
	 // Drain first: the root buffer carries whatever earlier cases left in it,
	 // so a count taken without one is a count of the case before as well.
	 gc_collect_cycles(); var_dump(gc_collect_cycles());`,
	// A class that refuses to grow refuses every route to a new property, not
	// only a plain assignment: a write descent and a reference binding too.
	`function tn($l, $fn) {
	   echo "# $l\n";
	   try { $fn(); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 $m = new WeakMap;
	 tn("assign", function () use ($m) { $m->prop = 1; });
	 tn("descend", function () use ($m) { $m->prop[] = 1; });
	 tn("bind", function () use ($m) { $r = 1; $m->prop = &$r; });
	 $plain = new stdClass;
	 tn("stdClass descend", function () use ($plain) { $plain->prop[] = 1; var_dump($plain->prop); });`,
	// A few of the engine's classes hold state with no written form and refuse
	// to be serialized or unserialized at all; a WeakReference additionally
	// refuses to be constructed, and describes itself by what it points at.
	`function tw($l, $fn) {
	   echo "# $l\n";
	   try { var_dump($fn()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 tw("serialize WeakReference", fn() => serialize(WeakReference::create(new stdClass)));
	 tw("serialize WeakMap", fn() => serialize(new WeakMap));
	 tw("serialize Closure", fn() => serialize(function () {}));
	 tw("serialize Generator", function () { $g = (function () { yield 1; })(); return serialize($g); });
	 tw("unserialize WeakReference", fn() => unserialize(chr(79) . ':13:"WeakReference":0:{}'));
	 tw("unserialize Closure", fn() => unserialize(chr(79) . ':7:"Closure":0:{}'));
	 tw("new WeakReference", fn() => new WeakReference);
	 tw("new WeakMap", fn() => new WeakMap);
	 $held = new stdClass; $held->hello = "world";
	 $ref = WeakReference::create($held);
	 var_dump($ref->get() === $held);
	 var_dump(array_keys((array) $ref));`,
	// call_user_func has no slot to hand over: whatever the callee asked for,
	// it gets a copy, and the engine says so once per by-reference parameter.
	`function wants(&$a, &$b) { $a++; $b--; return true; }
	 $i = 0; $j = 0;
	 var_dump(call_user_func("wants", $i, $j));
	 var_dump($i, $j);
	 var_dump(call_user_func_array("wants", [$i, $j]));
	 var_dump($i, $j);
	 $alias = &$i;
	 var_dump(call_user_func("wants", $i, $j));
	 var_dump($i, $j);
	 wants($i, $j);
	 var_dump($i, $j);`,
	// A key that carries the engine's visibility mangling can arrive through an
	// (object) cast. var_dump and foreach read it as the mangling says;
	// get_object_vars and the (array) cast hand the table back as it stands;
	// json_encode leaves it out, since it is not public.
	`$o = (object) ["\0A\0b" => 42, "\0*\0c" => 24, "plain" => 1];
	 var_dump($o);
	 foreach ($o as $k => $v) { var_dump($k); }
	 var_dump(get_object_vars($o));
	 var_dump((array) $o);
	 var_dump(json_encode($o));
	 print_r($o);`,
	// Some of the engine's own classes refuse a property they did not declare
	// outright, where an ordinary class only deprecates it.
	`function td($l, $fn) {
	   echo "# $l\n";
	   try { $fn(); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 set_error_handler(function ($n, $m) { echo "warned: ", $m, "\n"; return true; });
	 td("Closure", function () { $c = function () {}; $c->a = 1; });
	 td("Generator", function () { $g = (function () { yield 1; })(); $g->a = 1; });
	 td("WeakMap", function () { $w = new WeakMap; $w->a = 1; });
	 td("ArrayObject", function () { $a = new ArrayObject; $a->a = 1; });
	 td("stdClass", function () { $s = new stdClass; $s->a = 1; });`,
	// A Throwable's constructor is the engine's, and its signature is enforced
	// like any other. The diagnostic names the class that *declares* it -- one
	// of three -- not the one being instantiated.
	`function te($l, $fn) {
	   echo "# $l\n";
	   try { $fn(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 class MyEx extends Exception {}
	 class MyErr extends Error {}
	 class MyArgErr extends ArgumentCountError {}
	 class MyErrEx extends ErrorException {}
	 te("Exception", fn() => new Exception(new stdClass));
	 te("subclass of Exception", fn() => new MyEx(new stdClass));
	 te("Error", fn() => new MyErr(new stdClass));
	 te("deep subclass of Error", fn() => new MyArgErr(new stdClass));
	 te("ErrorException", fn() => new MyErrEx(new stdClass));
	 te("code", fn() => new Exception("m", new stdClass));
	 te("fine", function () { $e = new MyEx("m", 3); var_dump($e->getMessage(), $e->getCode()); });`,
	// A trait's static members belong to each class that uses it, so reaching
	// one through the trait itself is deprecated -- properties and methods
	// alike, and a method the trait does not declare still names what was
	// called.
	`trait StaticT {
	   public static $held;
	   public static function named() { echo "named\n"; }
	   public static function __callStatic($n, $a) { echo "callStatic($n)\n"; }
	 }
	 class StaticUser { use StaticT; }
	 StaticT::$held = 42;
	 var_dump(StaticT::$held);
	 StaticT::named();
	 StaticT::missing();
	 StaticUser::$held = 7;
	 var_dump(StaticUser::$held);
	 StaticUser::named();`,
	// Eval'd code is named after the site that ran it, not after whatever the
	// compiler was handed: `path(line) : eval()'d code`. Only the tail is
	// compared, since the path itself is the harness's rather than PHP's.
	`eval('$f = __FILE__; echo substr($f, strpos($f, ") : ") + 1), "\n";');`,
	`eval('echo __LINE__, "\n";');
	 eval("echo 1;\necho __LINE__, \"\\n\";");`,
	`set_error_handler(function ($n, $m, $f, $l) {
	   echo substr($f, strpos($f, ") : ") + 1), " on ", $l, "\n";
	   return true;
	 });
	 eval('echo $evalUndefined;');`,
	// A method a class composes from a trait is the class's own as far as a
	// subclass is concerned: overriding it is checked against the composition.
	`trait ComposedT { public function m($a = 1) { return "T$a"; } }
	 class ComposedA { use ComposedT; }
	 class ComposedB extends ComposedA { public function m($a = 1, $b = 2) { return "B$a$b"; } }
	 echo (new ComposedA)->m(), "|", (new ComposedB)->m(), "\n";`,
	// `as final` says nothing about visibility: a protected method aliased
	// final stays protected.
	`trait FinalVisT {
	   protected function v() { return "v"; }
	   public function call() { return $this->v(); }
	 }
	 class FinalVisUser { use FinalVisT { FinalVisT::v as final; } }
	 $o = new FinalVisUser;
	 echo $o->call(), "\n";
	 try { $o->v(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// A trait implementing an interface method with a tentative return type
	// earns the same deprecation writing it in the class would.
	`trait ItT { public function getIterator() { return new ArrayIterator([]); } }
	 class ItUser implements IteratorAggregate { use ItT; }
	 foreach (new ItUser as $v) { echo $v; }
	 echo "ok\n";`,
	// __DIR__ inside eval'd code is the directory of the file that ran the
	// eval, and __FILE__ is that file's name with the eval suffix on it.
	`$dir = __DIR__;
	 $file = __FILE__;
	 eval('var_dump(__DIR__ === $dir, __FILE__ === $file, str_contains(__FILE__, $file));');`,
	// An anonymous class is named after the file it was written in, so one
	// declared inside eval carries the eval's name -- and the opline that
	// declares it has to still find it under that name.
	`eval('$o = new class { public $v = 7; };
	   var_dump($o->v,
	     str_starts_with(get_class($o), "class@anonymous"),
	     str_contains(get_class($o), " : eval()" . chr(39) . "d code"));');`,
	// An unhandled match names an enum case; every other object is described
	// by its type instead.
	`enum MatchSuit { case Hearts; case Spades; }
	 enum MatchRank: string { case Ace = "A"; }
	 foreach ([MatchSuit::Hearts, MatchRank::Ace, new stdClass, [1], 1.5, "x", null, true] as $subject) {
	   try { match ($subject) { "never" => 1 }; }
	   catch (\UnhandledMatchError $e) { echo $e->getMessage(), "\n"; }
	 }`,
	// call_user_func forwards a named argument to the *callee*: the name is
	// resolved against what is being called, not against call_user_func.
	`class CufT {
	   public static function m($a = 5, $b = 1) { echo "s a=$a b=$b\n"; }
	   public function i($a = 5, $b = 1) { echo "i a=$a b=$b\n"; }
	 }
	 function cuf_f($a = 5, $b = 1) { echo "f a=$a b=$b\n"; }
	 function cuf_v(...$rest) { var_dump($rest); }
	 call_user_func(["CufT", "m"], b: 0);
	 call_user_func("CufT::m", b: 0);
	 call_user_func([new CufT, "i"], b: 0);
	 call_user_func("cuf_f", b: 0);
	 call_user_func("cuf_f", 9, b: 0);
	 try { call_user_func("cuf_f", zzz: 0); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 call_user_func(callback: "cuf_f");
	 var_dump(call_user_func("str_pad", "a", length: 3));
	 call_user_func("cuf_v", 1, k: 2);`,
	// A named argument given to a closure's own __invoke, or to Closure::call
	// past the new $this, resolves against the closure's parameters -- the
	// engine answers both with a trampoline carrying that signature.
	`class InvHost {}
	 $t = function ($a = "a", $b = "b", $c = "c") { echo "a=$a b=$b c=$c\n"; };
	 $v = function (...$args) { var_dump($args); };
	 $t->__invoke("A", c: "C");
	 $v->__invoke("A", c: "C");
	 $t->call(new InvHost, "A", c: "C");
	 $v->call(new InvHost, "A", c: "C");
	 $t->call(newThis: new InvHost);
	 try { $t->call(new InvHost, zz: 1); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// The reflectors take named arguments the same way, and their *Args forms
	// read a string key as a parameter name.
	`class RefTarget {
	   public function __construct($a = "a", $b = "b") { echo "ctor a=$a b=$b\n"; }
	   public function method($a = "a", $b = "b") { echo "m a=$a b=$b\n"; }
	 }
	 function ref_f($a = "a", $b = "b") { echo "f a=$a b=$b\n"; }
	 function ref_v(...$rest) { var_dump($rest); }
	 $rf = new ReflectionFunction("ref_f");
	 $rf->invoke("A", b: "B");
	 $rf->invokeArgs(["A", "b" => "B"]);
	 (new ReflectionFunction("ref_v"))->invokeArgs([1, "k" => 2]);
	 try { $rf->invokeArgs(["zz" => 1]); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 $rm = new ReflectionMethod(RefTarget::class, "method");
	 $rm->invoke(new RefTarget, "A", b: "B");
	 $rm->invokeArgs(new RefTarget, ["A", "b" => "B"]);
	 $rc = new ReflectionClass(RefTarget::class);
	 $rc->newInstance("A", b: "B");
	 $rc->newInstanceArgs(["A", "b" => "B"]);`,
	// A name given to `new` resolves against the constructor, and a class that
	// declares none takes no arguments at all.
	`class NamedPlain {}
	 class NamedCtor { public function __construct($a = 1) { echo "a=$a\n"; } }
	 try { new NamedPlain(x: 1); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { new stdClass(x: 1); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { new NamedCtor(x: 1); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 new NamedCtor(a: 5);
	 $e = new Exception(message: "m", code: 3);
	 echo $e->getMessage(), $e->getCode(), "\n";
	 try { new Exception(zz: 1); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// An engine function's parameter default is text on its declaration, and a
	// name that skips a parameter has to fall back to it. An optional one with
	// no default written cannot be filled in at all.
	`var_dump(str_pad("foo", 6, pad_type: STR_PAD_LEFT));
	 var_dump(array_slice([1, 2, 3, 4, 5], 1, preserve_keys: true));
	 var_dump(array_slice(array: ["a" => 0, "b" => 1], offset: 1, preserve_keys: true));
	 try { array_keys(strict: true); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { array_keys([], strict: true); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump(array_keys([41, 42], filter_value: 42, strict: true));
	 var_dump(htmlspecialchars("<a>&\"", double_encode: false));`,
	// array_keys keeps only the keys whose value matches, loosely or not.
	`var_dump(array_keys([41, 42, "42"], 42));
	 var_dump(array_keys([41, 42, "42"], 42, true));
	 var_dump(array_keys(["a" => 1, "b" => 2], 2));
	 var_dump(array_keys([1, 2]));`,
	// A temporary has nothing to write back into, so sending one to a
	// by-reference parameter is refused. The callee is declared *after* the
	// caller on purpose: known earlier, the compiler refuses it outright, and
	// an E_COMPILE_ERROR is not something the oracle can be asked about.
	`function tmpTest() {
	   tmpByVal(func_get_args()[0]);
	   try { tmpByRef(func_get_args()[0]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	   tmpByVal(TMPC[0]);
	   try { tmpByRef(TMPC[0]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 }
	 const TMPC = ["foo"];
	 function tmpByVal($arg) { var_dump($arg); }
	 function tmpByRef(&$arg) { var_dump($arg); }
	 tmpTest("y");
	 $fn = function (&$ref) {};
	 try { $fn([0, 1][0]); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { $fn([0, 1]->prop); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// #[Attribute]'s own constructor checks its argument before the flags are
	// looked at, and it is strict: a numeric string is refused too.
	`#[Attribute("foo")] class AttrS {}
	 #[Attribute(9999)] class AttrI {}
	 #[Attribute(-1)] class AttrN {}
	 #[Attribute(1.5)] class AttrF {}
	 #[Attribute(true)] class AttrB {}
	 #[Attribute(null)] class AttrZ {}
	 #[Attribute(Attribute::TARGET_CLASS)] class AttrOk {}
	 #[AttrS] class AttrT1 {}
	 #[AttrI] class AttrT2 {}
	 #[AttrN] class AttrT3 {}
	 #[AttrF] class AttrT4 {}
	 #[AttrB] class AttrT5 {}
	 #[AttrZ] class AttrT6 {}
	 #[AttrOk] class AttrT7 {}
	 foreach (["AttrT1", "AttrT2", "AttrT3", "AttrT4", "AttrT5", "AttrT6", "AttrT7"] as $c) {
	   try { (new ReflectionClass($c))->getAttributes()[0]->newInstance(); echo "$c ok\n"; }
	   catch (Throwable $e) { echo "$c ", get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	// A by-reference foreach is bound to the variable, not to the array it
	// held: assigning something that is not iterable ends the loop with a
	// warning, while unsetting the name does not -- the reference outlives it.
	// A by-value loop iterates a copy and sees none of this.
	`$b = [1, 2, 3]; foreach ($b as &$v2) { echo $v2, ","; $b = null; } echo "|\n";
	 $c = [1, 2, 3]; foreach ($c as &$v3) { echo $v3, ","; $c = "s"; } echo "|\n";
	 $d = [1, 2, 3]; foreach ($d as $v4) { echo $v4, ","; $d = null; } echo "|\n";
	 $e = [1, 2, 3]; foreach ($e as &$v5) { echo $v5, ","; unset($e); } echo "|\n";
	 $g = [1, 2]; foreach ($g as &$v6) { echo $v6, ","; } echo "|\n";`,
	// An offset a string does not have is *absent*, not empty: `??` has to see
	// nothing there rather than the "" a read would warn and give.
	`$foo = "test";
	 var_dump($foo[0] ?? "d1", $foo[5] ?? "d2", isset($foo[5]) ? $foo[5] : "d3",
	   $foo["str"] ?? "d4", $foo[-9] ?? "d6", $foo[-1] ?? "d7");
	 var_dump(isset($foo[5]), isset($foo["0"]), isset($foo["1x"]), isset($foo[-1]), isset($foo[-9]));
	 var_dump(empty($foo[5]), empty($foo[0]));`,
	// Readonly is settled before the value is converted, so a __toString that
	// the conversion would have run never runs. A __clone reinitialization is
	// still allowed, and there the conversion does happen.
	`class SideEffect { public function __toString() { var_dump("side-effect"); return ""; } }
	 class ReadonlyHost {
	   public function __construct(public readonly string $bar) {}
	   public function write() { $this->bar = new SideEffect(); }
	 }
	 $host = new ReadonlyHost("");
	 try { $host->write(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 class ReadonlyClone {
	   public function __construct(public readonly string $b) {}
	   public function __clone() { $this->b = new SideEffect(); }
	 }
	 var_dump((clone new ReadonlyClone("x"))->b);`,
	// A `function &f()` returns a reference, and sending that result to a
	// by-reference parameter passes the reference on rather than a copy.
	`function &returnsRef() { global $refTarget; return $refTarget; }
	 function takesRef(&$p) { $p = "via-arg"; }
	 function takesVal($p) { $p = "no"; }
	 takesRef(returnsRef()); var_dump($refTarget);
	 $refTarget = "keep"; takesVal(returnsRef()); var_dump($refTarget);
	 $nothing = null;
	 takesRef(returnsRef($nothing?->missing())); var_dump($refTarget);`,
	// A class named as a *string* is looked up the way any run-time class name
	// is, autoloaders included. The class being tested against never is.
	`spl_autoload_register(function ($n) { echo "AUTOLOAD $n\n"; if ($n !== "NeverDeclared") eval("class $n {}"); });
	 class AncP {} class AncC extends AncP {}
	 echo "1:"; var_dump(is_a("AncY1", "AncY2"));
	 echo "2:"; var_dump(is_a("AncY3", "AncY4", true));
	 echo "3:"; var_dump(is_subclass_of("AncC", "AncP"));
	 echo "4:"; var_dump(is_subclass_of("AncC", "AncZ1"));
	 echo "5:"; var_dump(is_a("AncC", "AncZ2", true));
	 echo "6:"; var_dump(is_subclass_of("NeverDeclared", "AncP"));
	 echo "7:"; var_dump(is_a(new AncC, "AncZ3"));`,
	// Readonly refuses unset() only once the property holds a value: before
	// that there is nothing to take back, and the scope rules alone decide.
	`class UnsetRO {
	   public readonly int $p;
	   public function un() { unset($this->p); echo "unset-ok\n"; var_dump(isset($this->p)); }
	   public function set() { $this->p = 5; }
	   public function again() { unset($this->p); $this->p = 7; var_dump($this->p); }
	 }
	 (new UnsetRO)->un();
	 $held = new UnsetRO; $held->set();
	 try { $held->un(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 $fresh = new UnsetRO;
	 try { unset($fresh->p); echo "outer-ok\n"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 (new UnsetRO)->again();`,
	// `global $x` creates the global holding null, so reading it afterwards is
	// reading a variable that exists. Only isset() can still tell.
	`function globalReader() { global $neverSet; var_dump($neverSet); $neverSet = $neverSet + 0; echo "ok\n"; }
	 globalReader();
	 var_dump($neverSet, array_key_exists("neverSet", $GLOBALS), isset($neverSet));
	 function globalWriter() { global $existing; var_dump($existing); $existing = 9; }
	 $existing = 3; globalWriter(); var_dump($existing);`,
	// A variable named by an expression converts that expression to a string,
	// so an object goes through __toString and one without it is an Error.
	`try { global ${new stdClass}; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 class VarName { public function __toString() { return "dyn"; } }
	 $dyn = 5;
	 $named = new VarName;
	 var_dump($$named);
	 $plain = "dyn";
	 var_dump($$plain);`,
	// getIterator() has to hand back something a foreach can walk: an array is
	// not, and neither is the aggregate itself, which would only ask again.
	`class AggArr implements IteratorAggregate { #[ReturnTypeWillChange] public function getIterator() { return ["foo"]; } }
	 class AggNull implements IteratorAggregate { #[ReturnTypeWillChange] public function getIterator() { return null; } }
	 class AggObj implements IteratorAggregate { #[ReturnTypeWillChange] public function getIterator() { return new stdClass; } }
	 class AggSelf implements IteratorAggregate { #[ReturnTypeWillChange] public function getIterator() { return $this; } }
	 class AggGood implements IteratorAggregate { public function getIterator(): Iterator { return new ArrayIterator([1, 2]); } }
	 class AggGen implements IteratorAggregate { public function getIterator(): Generator { yield 7; } }
	 class AggNest implements IteratorAggregate { public function getIterator(): Traversable { return new AggGood; } }
	 foreach (["AggArr", "AggNull", "AggObj", "AggSelf", "AggGood", "AggGen", "AggNest"] as $c) {
	   try { foreach (new $c as $v) { var_dump($v); } echo "$c done\n"; }
	   catch (Throwable $e) { echo "$c ", get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	// A by-reference parameter given a temporary writes into something nobody
	// can read -- which has to include whatever the temporary was made from.
	`class SpliceHost {
	   protected static $arr = ["a", "b", "c"];
	   public static function getArr() { return self::$arr; }
	 }
	 array_splice(SpliceHost::getArr(), 1, 1);
	 print_r(SpliceHost::getArr());
	 class PopHost {
	   protected static $t = [0 => "main"];
	   public static function getType() { return self::$t; }
	 }
	 $taken = array_pop((PopHost::getType()));
	 print_r(PopHost::getType());
	 var_dump($taken);`,
	// A parameter default is a constant expression evaluated per call, and
	// concatenation in one renders its operands like any other concatenation.
	`class DefaultCounter { public function __toString() { static $i = 0; return (string) $i++; } }
	 class DefaultConst { const C = "c"; }
	 class DefaultNoString {}
	 function defaultNew(string $f = new DefaultCounter() . "") { var_dump($f); }
	 defaultNew(); defaultNew(); defaultNew();
	 function defaultConst($x = DefaultConst::C . "!") { var_dump($x); }
	 defaultConst();
	 function defaultBad($x = new DefaultNoString . "") { var_dump($x); }
	 try { defaultBad(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// Comparing a structure that contains itself is refused rather than
	// answered, and the refusal comes before the sizes are looked at. The same
	// container on both sides is settled by identity and never descends.
	`$rec = [&$rec];
	 echo "A:"; try { var_dump($rec === [[]]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 echo "B:"; try { var_dump([[]] === $rec); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 echo "C:"; try { var_dump($rec == [[]]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 echo "D:"; var_dump($rec === $rec);
	 $one = [1];
	 echo "E:"; var_dump($rec === $one);
	 $x = [1, 2]; $y = [1, 2];
	 echo "F:"; var_dump([$x, $x] === [$y, $y], [$x] == [$y], $x === $y, [[1]] === [[1]], [[1]] == [[2]]);
	 class RecHost { public $p = 5; }
	 $o1 = new RecHost; $o2 = new RecHost; $o1->p = $o1; $o2->p = $o2;
	 echo "G:"; try { var_dump($o1 == $o2); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 echo "H:"; var_dump($o1 == $o1, new RecHost == new RecHost);
	 // Leave nothing cyclic behind: the cycle collector is shared with every
	 // case after this one, and would count these.
	 $rec[0] = 1; $o1->p = null; $o2->p = null;`,
	// `use T { m as final; }` makes the composed method final and
	// `as protected` changes its visibility, both without touching the trait --
	// which reflection has to answer for the pair rather than for the body.
	// Making a private method final says nothing, and is warned about, but only
	// when the alias is what made it final.
	`trait AliasT {
	   public function pub() {}
	   protected function prot() {}
	   private function priv() {}
	   public final function fin() {}
	   private final function pfin() {}
	 }
	 class AliasUser {
	   use AliasT { pub as final; prot as final; priv as final; fin as private; pfin as public; }
	 }
	 foreach (["pub", "prot", "priv", "fin", "pfin"] as $m) {
	   $r = new ReflectionMethod(AliasUser::class, $m);
	   printf("%s final=%d pub=%d prot=%d priv=%d\n",
	     $m, $r->isFinal(), $r->isPublic(), $r->isProtected(), $r->isPrivate());
	 }
	 trait AliasCtor { private function make() {} }
	 class AliasCtorUser { use AliasCtor { make as final __construct; } }
	 trait AliasRenamed { private function q() {} }
	 class AliasRenamedUser { use AliasRenamed { q as final renamed; } }
	 // A trait composing another may change its visibility on the way in, and
	 // a class using *that* gets the method as it was composed.
	 trait AliasInner { protected function aa() { echo "aa\n"; } }
	 trait AliasOuter { use AliasInner { aa as public; } }
	 class AliasBase { use AliasInner; }
	 class AliasChild extends AliasBase { use AliasOuter; }
	 (new AliasChild)->aa();
	 $r = new ReflectionMethod(AliasChild::class, "aa");
	 printf("aa pub=%d prot=%d\n", $r->isPublic(), $r->isProtected());
	 echo "done\n";`,
	// markLazyObjectAsInitialized hands the object back and drops the laziness
	// without running the initializer, which leaves the declared defaults --
	// what a ghost had put aside rather than never had. A proxy is marked the
	// same way and becomes an ordinary object of the class it stood in for.
	`class MarkLazy { public int $a = 1; public $b = "x"; public int $c; }
	 $rc = new ReflectionClass(MarkLazy::class);
	 $ghost = $rc->newLazyGhost(function ($o) { var_dump("init"); $o->a = 9; });
	 var_dump($rc->isUninitializedLazyObject($ghost));
	 var_dump($rc->markLazyObjectAsInitialized($ghost) === $ghost);
	 var_dump($rc->isUninitializedLazyObject($ghost), $ghost);
	 $proxy = $rc->newLazyProxy(function ($o) { var_dump("init"); return new MarkLazy; });
	 var_dump($rc->markLazyObjectAsInitialized($proxy) === $proxy);
	 var_dump($rc->isUninitializedLazyObject($proxy), $proxy);`,
	// A class with nothing to defer is handed back ordinary rather than lazy,
	// and an ordinary object of a class that declares no properties has none --
	// so a dynamic property set beforehand does not survive the reset.
	`#[AllowDynamicProperties] class ResetNoProps {}
	 $obj = new ResetNoProps;
	 $obj->dynamic = "value";
	 (new ReflectionClass($obj))->resetAsLazyGhost($obj, function ($o) { var_dump("init"); });
	 var_dump((new ReflectionClass($obj))->isUninitializedLazyObject($obj), $obj);`,
	// Every reflector prints its subject as a block of text, and the format is
	// exact: `@@ file 11-17` for a class but `@@ file 8 - 8` for a method, an
	// empty section still printing its braces, and the parameter block appearing
	// as soon as there is a return type even with no parameters.
	`interface DumpI { public function iface(); }
	 abstract class DumpBase {
	   const BC = 1;
	   public static $sp = 2;
	   protected int $prot = 3;
	   public function __construct(public readonly string $promoted = "d") {}
	   abstract protected function abs(): void;
	   public static function stat(int $a, string $b = "x", ...$rest): ?string { return null; }
	   public function inherited(&$ref, DumpI $i, int|float $u) {}
	 }
	 final class DumpImpl extends DumpBase implements DumpI {
	   const IC = "c";
	   private $priv;
	   public function iface() {}
	   protected function abs(): void {}
	   final public function fin() {}
	 }
	 // The dump names the file it was written in, and the two sides cannot
	 // agree on that -- the oracle evaluates a string, the interpreter runs a
	 // temporary -- so the name is taken out before comparing.
	 $show = fn ($r) => print(str_replace(__FILE__, "FILE", (string) $r) . "\n");
	 $show(new ReflectionClass(DumpImpl::class));
	 $show(new ReflectionMethod(DumpImpl::class, "fin"));
	 trait DumpTrait { private final function work() {} }
	 class DumpUser { use DumpTrait { work as __construct; } }
	 $show((new ReflectionClass("DumpUser"))->getMethod("__construct"));`,
	// `instanceof self` carries a class *reference*, not a name, and refuses
	// outside a class the same way every other reference does.
	`$outside = function () {
	   try { var_dump(new stdClass instanceof self); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 };
	 $outside();
	 class InstH {}
	 class InstJ extends InstH {
	   function t() { var_dump($this instanceof self, $this instanceof parent, $this instanceof static); }
	 }
	 (new InstJ)->t();
	 $o = new InstJ; $n = "InstH";
	 var_dump($o instanceof InstH, $o instanceof $n, $o instanceof $o);`,
	// A step whose diagnostic a handler turns into a throw leaves the value
	// alone: the engine reports first and checks for an exception after.
	`set_error_handler(function ($severity, $m) { throw new Exception($m, $severity); });
	 foreach (["", " ", "199A", "Hello world"] as $value) {
	   try { $value++; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
	   var_dump($value);
	   try { $value--; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
	   var_dump($value);
	 }
	 restore_error_handler();`,
	// unserialize writes properties the way any other write does: a class that
	// forbids dynamic properties refuses, one that merely does not allow them
	// earns the deprecation, and `O:` asking for an instance of something that
	// has none is refused before a property is read at all.
	`readonly class UnserRO {}
	 class UnserPlain {}
	 enum UnserEnum { case A; }
	 #[AllowDynamicProperties] class UnserAllowed {}
	 class UnserDeclared { public $d; }
	 try { var_dump(unserialize("O:7:\"UnserRO\":1:{s:1:\"x\";b:1;}")); }
	 catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump(unserialize("O:10:\"UnserPlain\":1:{s:1:\"y\";b:1;}"));
	 try { var_dump(unserialize("O:9:\"UnserEnum\":1:{s:1:\"q\";b:1;}")); }
	 catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump(unserialize("O:12:\"UnserAllowed\":1:{s:1:\"z\";i:5;}"));
	 var_dump(unserialize("O:13:\"UnserDeclared\":1:{s:1:\"d\";i:9;}"));`,
	// A set hook is handed the property's value, so its parameter is
	// contravariant: a supertype is fine, and so is a union that contains the
	// property's type. The refusal for an incompatible one is a link-time fatal
	// and cannot be asked of the oracle; these are the shapes it must not
	// refuse, and a plain property composed from a trait alongside an identical
	// one is still allowed.
	`class HBase {} class HDer extends HBase {}
	 class HookOk {
	   public HDer $a { set(HBase $v) { echo "set a\n"; } get { return new HDer; } }
	   public $u { set($v) { echo "set u\n"; } get { return "u"; } }
	   public HDer $c { set(HDer|int $v) { echo "set c\n"; } get { return new HDer; } }
	 }
	 $h = new HookOk;
	 $h->a = new HDer; $h->u = 1; $h->c = new HDer;
	 var_dump($h->u);
	 trait HookPlainT { public $shared = 1; }
	 class HookPlainUser { use HookPlainT; public $shared = 1; }
	 var_dump((new HookPlainUser)->shared);`,
	// A hook is compared with an inherited one the way any declaration is, and
	// a set access level may be widened but not narrowed. These are the shapes
	// that must *not* be refused -- the refusals are link-time fatals the
	// oracle cannot be asked about.
	`interface HookIfc { public string $prop { get; set; } }
	 class HookWide implements HookIfc {
	   public string $prop { get => "g"; set(string|int $v) { echo "wide set\n"; } }
	 }
	 class HookBase { public string $prop { get => "b"; set(string $v) { echo "base set\n"; } } }
	 class HookChild extends HookBase {
	   public string $prop { get => "c"; set(string|int $v) { echo "child set\n"; } }
	 }
	 class HookSame implements HookIfc { public string $prop = "s"; }
	 $w = new HookWide; $w->prop = "x"; echo $w->prop, "\n";
	 $c = new HookChild; $c->prop = "y"; echo $c->prop, "\n";
	 $s = new HookSame; $s->prop = "z"; echo $s->prop, "\n";
	 class SetWiden { public protected(set) string $p = "a"; }
	 class SetWider extends SetWiden { public string $p = "b"; }
	 var_dump((new SetWider)->p);`,
	// A dynamically named method call takes a string and nothing else -- not a
	// number, not null, not an object with __toString. The object form checks
	// the name before the receiver; the static form settles the class first.
	`class NameHost {
	   static function __callStatic($a, $b) { echo "cs:$a\n"; }
	   function __call($a, $b) { echo "c:$a\n"; }
	 }
	 class NameStr { function __toString() { return "ok"; } }
	 $n = 1; $o = new NameHost; $m = null; $s = "ok"; $arr = [1];
	 $notObject = null; $strObject = new NameStr; $t = true; $fl = 1.5;
	 foreach (["int" => $n, "null" => $m, "array" => $arr, "object" => $strObject,
	           "bool" => $t, "float" => $fl] as $label => $bad) {
	   try { $o->$bad(); } catch (Error $e) { echo "$label: ", $e->getMessage(), "\n"; }
	 }
	 $o->$s(); NameHost::$s();
	 try { NameHost::$n(); } catch (Error $e) { echo "static: ", $e->getMessage(), "\n"; }
	 try { $notObject->$n(); } catch (Error $e) { echo "receiver: ", $e->getMessage(), "\n"; }
	 try { $notObject->$s(); } catch (Error $e) { echo "null recv: ", $e->getMessage(), "\n"; }
	 try { NoSuchNameClass::$n(); } catch (Error $e) { echo "class: ", $e->getMessage(), "\n"; }`,
	// A protected method is reachable from a sibling, because the check runs
	// against the class that first declared it. A *constructor* has no
	// prototype, so overriding one makes the overriding class the root -- and
	// the same sibling is refused.
	`class CtorRoot { protected function __construct() { echo "root ctor\n"; } }
	 class CtorSibling extends CtorRoot {
	   static function makePlain() { new CtorPlain; }
	   static function makeOwn() { new CtorOwn; }
	 }
	 class CtorOwn extends CtorRoot { protected function __construct() { echo "own ctor\n"; } }
	 class CtorPlain extends CtorRoot {}
	 try { CtorSibling::makePlain(); } catch (Error $e) { echo "plain: ", $e->getMessage(), "\n"; }
	 try { CtorSibling::makeOwn(); } catch (Error $e) { echo "own: ", $e->getMessage(), "\n"; }
	 class MethRoot { protected function m() { echo "root m\n"; } }
	 class MethSibling extends MethRoot { function go(MethRoot $o) { $o->m(); } }
	 class MethOwn extends MethRoot { protected function m() { echo "own m\n"; } }
	 (new MethSibling)->go(new MethOwn);`,
	// `use T { T::m as other; }` adds a name, and when T::m is abstract the
	// requirement comes with it -- the class has to implement both names. The
	// refusal is a link-time fatal; what this pins down is that implementing
	// both is enough, and that a *concrete* trait method aliased twice needs
	// nothing.
	`trait AliasAbstract {
	   abstract public function doStuff();
	   public function main() { $this->doStuff(); $this->doOtherStuff(); }
	 }
	 class AliasBoth {
	   use AliasAbstract { AliasAbstract::doStuff as doOtherStuff; }
	   public function doStuff() { echo "doStuff\n"; }
	   public function doOtherStuff() { echo "doOtherStuff\n"; }
	 }
	 (new AliasBoth)->main();
	 trait AliasConcrete { public function work() { echo "work\n"; } }
	 class AliasRenames { use AliasConcrete { work as alsoWork; } }
	 $r = new AliasRenames; $r->work(); $r->alsoWork();`,
	// `self` and `static` in a declared type name classes, and which ones is
	// only known where the check happens: `static` is the class the call was
	// made on, `self` the one whose body is running, and in a property type the
	// one that declared it -- where the diagnostic still says `self`.
	`class StatA {} class StatB extends StatA { public function t($x): static { return $x; } }
	 $a = new StatA; $b = new StatB;
	 var_dump($b->t($b) instanceof StatB);
	 try { $b->t($a); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 trait StatT { public function u($x): static { return $x; } }
	 class StatC { use StatT; } class StatD extends StatC {}
	 var_dump((new StatC)->u(new StatC) instanceof StatC);
	 try { (new StatD)->u(new StatC); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 class SelfA {} class SelfB extends SelfA { function m(self $x) { echo "arg ok\n"; } }
	 $sb = new SelfB;
	 $sb->m(new SelfB);
	 try { $sb->m(new stdClass); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { $sb->m(new SelfA); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 class PropHost { public SelfA|self $p; }
	 $ph = new PropHost;
	 $ph->p = new PropHost; $ph->p = new SelfA; echo "prop ok\n";
	 try { $ph->p = new stdClass; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// json_encode drops a whole float's fractional part, and
	// JSON_PRESERVE_ZERO_FRACTION asks for it back -- written into the number
	// the encoder already produced rather than re-formatting it.
	// JSON_THROW_ON_ERROR turns the `false` into a JsonException.
	`var_dump(json_encode(42.0), json_encode(42.0, JSON_PRESERVE_ZERO_FRACTION));
	 var_dump(json_encode(42.5, JSON_PRESERVE_ZERO_FRACTION), json_encode(-0.0, JSON_PRESERVE_ZERO_FRACTION));
	 var_dump(json_encode(1e20, JSON_PRESERVE_ZERO_FRACTION), json_encode([1.0, 2.5], JSON_PRESERVE_ZERO_FRACTION));
	 var_dump(json_encode(["a" => 1.0], JSON_PRESERVE_ZERO_FRACTION));
	 try { var_dump(json_encode(INF, JSON_THROW_ON_ERROR)); }
	 catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump(json_encode(INF));`,
	// A `static` type no class scope can name is satisfied by nothing, and the
	// diagnostic keeps the word: an unbound closure declared `: static`.
	`class StatHost {}
	 $unbound = function ($x): static { return $x; };
	 // A closure names the file it was written in, which the two sides spell
	 // differently, so it is taken out before comparing.
	 try { $unbound(new stdClass); }
	 catch (TypeError $e) { echo str_replace(__FILE__, "FILE", $e->getMessage()), "\n"; }
	 $bound = Closure::bind($unbound, new StatHost, StatHost::class);
	 var_dump($bound(new StatHost) instanceof StatHost);`,
	// __toString is a coercion, so weak mode takes an object where a string is
	// declared -- argument, return and property alike -- and strict mode
	// refuses all three.
	`class WeakStr { function __toString(): string { return "s"; } }
	 function weakArg(string $x) { return $x; }
	 function weakRet(): string { return new WeakStr; }
	 class WeakHost { public string $p; }
	 var_dump(weakArg(new WeakStr));
	 var_dump(weakRet());
	 $wh = new WeakHost; $wh->p = new WeakStr; var_dump($wh->p);
	 var_dump(strlen(new WeakStr));`,
	// unset() on a *declared* typed property leaves the slot: reading it is
	// the uninitialized error, not a missing property -- unless the class has a
	// __get, which is what the absence is for.
	`class UnsetTyped { public ?int $foo = null; }
	 $x = new UnsetTyped; unset($x->foo);
	 try { var_dump($x->foo); } catch (Throwable $e) { echo "read: ", $e->getMessage(), "\n"; }
	 try { $x->foo = "ops"; } catch (Throwable $e) { echo "write: ", $e->getMessage(), "\n"; }
	 var_dump(isset($x->foo), $x->foo ?? "dflt");
	 class UnsetMagic { public ?int $p = 1; function __get($n) { echo "get $n\n"; return 7; } }
	 $b = new UnsetMagic; unset($b->p); var_dump($b->p);
	 class UnsetPlain { public $u = 1; }
	 $z = new UnsetPlain; unset($z->u); var_dump($z->u);`,
	// A property default the compiler could not fold is checked against the
	// declared type on every instantiation, and checked *strictly* whatever the
	// file declared: an int widens to float, a numeric string does not become
	// an int.
	`define("DEF_FIVE", 5);
	 define("DEF_NUMSTR", "7");
	 class DefBad { public string $foo = DEF_FIVE; }
	 class DefWide { public float $bar = DEF_FIVE; }
	 class DefStr { public int $n = DEF_NUMSTR; }
	 class DefOk { public int $k = DEF_FIVE; }
	 try { new DefBad; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { new DefBad; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump((new DefWide)->bar, (new DefWide)->bar);
	 try { var_dump((new DefStr)->n); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump((new DefOk)->k);`,
	// Growing an array inside a typed property that cannot hold one is
	// refused, and the wording says whether the write went through a reference
	// the program took. Taking the reference is fine; using it is not.
	`class AutoInit {
	   public ?string $p;
	   public ?iterable $i;
	   public static ?string $s;
	   public ?array $a = null;
	   public mixed $m = null;
	   public $u;
	 }
	 $o = new AutoInit;
	 $o->i[] = 1; var_dump($o->i);
	 try { $o->p[] = "x"; } catch (TypeError $e) { var_dump($e->getMessage()); }
	 try { $o->p["k"] = "x"; } catch (TypeError $e) { var_dump($e->getMessage()); }
	 $o->a[] = 1; $o->m[] = 2; $o->u[] = 3;
	 var_dump($o->a, $o->m, $o->u);
	 try { AutoInit::$s[] = "x"; } catch (TypeError $e) { var_dump($e->getMessage()); }
	 $ref = &$o->p;
	 try { $ref[] = "bar"; } catch (TypeError $e) { var_dump($e->getMessage()); }
	 var_dump($ref);
	 unset($o->p[0]);
	 echo "done\n";`,
	// unset() takes a declared property's slot away for writing as well as for
	// reading: the next write is to a property that is not there, so __set
	// answers. A declared property that was merely never initialized still has
	// its slot and is written directly.
	`class MagicTyped {
	   public int $foo;
	   public $bar;
	   function __set($n, $v) { echo "__set $n = $v\n"; }
	   function __get($n) { echo "__get $n\n"; return 9; }
	 }
	 $t = new MagicTyped;
	 $t->foo = 42; var_dump($t->foo);
	 unset($t->foo); $t->foo = 43; var_dump($t->foo);
	 $fresh = new MagicTyped; unset($fresh->foo); $fresh->foo = 1;
	 $direct = new MagicTyped; $direct->foo = 7; var_dump($direct->foo);
	 $t->bar = 1; unset($t->bar); $t->bar = 2; var_dump($t->bar);`,
	// A readonly property has no reference to give, whichever walk asks for
	// one -- including an ArrayIterator wrapping the object itself.
	`class ReadonlyWalk implements IteratorAggregate {
	   function __construct(public readonly string $foo = "bar") {}
	   function getIterator(): Traversable { return new ArrayIterator($this); }
	 }
	 $rw = new ReadonlyWalk;
	 try { foreach ($rw as $k => &$v) {} } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 try { $r = &$rw->foo; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 var_dump($rw);
	 foreach ($rw as $k => $v) { echo "$k => $v\n"; }`,
	// A by-reference parameter keeps the caller's slot, but a weak-mode
	// conversion is written back into it: after `to_int($x)` on a float, the
	// caller's variable *is* an int -- with the precision deprecation a lossy
	// one earns.
	`function refToInt(int &$x) {}
	 function refToString(string &$y) {}
	 function refToFloat(float &$z) {}
	 $a = 1.0; refToInt($a); var_dump($a);
	 $b = "5"; refToInt($b); var_dump($b);
	 $c = 1.5; refToInt($c); var_dump($c);
	 $d = 7; refToString($d); var_dump($d);
	 $g = 3; refToFloat($g); var_dump($g);
	 $bad = "x";
	 try { refToInt($bad); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($bad);`,
	// An anonymous class's real name carries a NUL before the file and line
	// that make it unique, and every diagnostic is a printf: each interpolated
	// string is cut at its own NUL, not the finished message. An argument error
	// builds "Class::method" as one string and loses the method with it; a
	// return error passes the class separately and keeps it; a type passes each
	// class name separately and keeps the rest of the union.
	`$anon = new class {
	   public function testParam(self|string $a) {}
	   public function testReturn(): self|string { return new stdClass; }
	 };
	 try { $anon->testParam(null); }
	 catch (TypeError $e) { echo str_replace(__FILE__, "FILE", $e->getMessage()), "\n"; }
	 try { $anon->testReturn(); }
	 catch (TypeError $e) { echo str_replace(__FILE__, "FILE", $e->getMessage()), "\n"; }`,
	// A method the engine supplies has no op_array, but reflection describes it
	// from the harvested signature all the same -- DateTime has a constructor,
	// and Exception::getMessage() is final.
	`$rc = new ReflectionClass("DateTime");
	 $rm = $rc->getMethod("__construct");
	 var_dump($rm->getAttributes(), $rm->getName(), $rm->isPublic(), $rm->isStatic(), $rm->isFinal());
	 $rm2 = new ReflectionMethod("Exception", "getMessage");
	 var_dump($rm2->getName(), $rm2->isFinal(), $rm2->isPublic());
	 try { $rc->getMethod("nope"); }
	 catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump((new ReflectionProperty("Exception", "message"))->getAttributes());
	 var_dump($rc->getReflectionConstant("ATOM")->getAttributes());
	 var_dump((new ReflectionFunction("unserialize"))->getAttributes());`,
	// Extending something that is not a class is refused, and an enum is a
	// class that happens to be final -- which is how the engine words it.
	`function tk($l, $code) {
	   echo "# $l\n";
	   try { eval($code); echo "linked\n"; } catch (Throwable $e) { echo get_class($e), "\n"; }
	 }
	 trait KTrait {}
	 interface KIface {}
	 interface KSub extends KIface {}
	 tk("interface extends interface", 'interface KSub2 extends KIface {}');
	 tk("class implements interface", 'class KOk implements KIface {}');
	 echo "done\n";`,
	// A trait may use traits of its own, and what they provide it provides.
	`trait NHello { public function sayHello() { echo "Hello "; } }
	 trait NWorld { public function sayWorld() { echo "World!\n"; } }
	 trait NBoth { use NHello, NWorld; }
	 class NUser { use NBoth; }
	 $n = new NUser; $n->sayHello(); $n->sayWorld();
	 trait NA { public function a() { echo "A\n"; } }
	 trait NB { use NA; }
	 trait NC { use NA; }
	 class NBoth2 { use NB, NC; }
	 (new NBoth2)->a();`,
	// An `insteadof` rule has to name a method that exists, must not exclude
	// the trait it takes the method from, and must not exclude the same one
	// twice.
	`function ti($l, $code) {
	   echo "# $l\n";
	   try { eval($code); echo "linked\n"; } catch (Throwable $e) { echo get_class($e), "\n"; }
	 }
	 trait IEmpty {}
	 trait IHas { public function bar() {} }
	 ti("good", 'class I1 { use IEmpty, IHas { IHas::bar insteadof IEmpty; } }');
	 (new I1)->bar();
	 echo "done\n";`,
	// `use T { m as protected; }` changes the visibility of m in the composing
	// class without touching the trait; `use T { m as public alias; }` gives the
	// new name its own and leaves the original alone.
	`trait Vis {
	   public function open() { echo "open\n"; }
	   protected static function shut() { echo "shut\n"; }
	   private static function tight() { echo "tight\n"; }
	 }
	 class VisUser {
	   use Vis {
	     open as protected;
	     Vis::shut as public;
	     Vis::tight as public loose;
	   }
	   public function inside() { $this->open(); }
	 }
	 $v = new VisUser;
	 $v->inside();
	 try { $v->open(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 VisUser::shut();
	 VisUser::loose();
	 try { VisUser::tight(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// Two traits providing the same name is a collision unless something
	// resolves it: `insteadof`, the class declaring the method itself, or the
	// other declaration being abstract -- a requirement rather than a body.
	`function tc($l, $code) {
	   echo "# $l\n";
	   try { eval($code); echo "linked\n"; } catch (Throwable $e) { echo get_class($e), "\n"; }
	 }
	 trait TA { public function m() { echo "A\n"; } public function only() {} }
	 trait TB { public function m() { echo "B\n"; } }
	 trait TAbs { abstract public function m(); }
	 tc("insteadof", 'class R1 { use TA, TB { TA::m insteadof TB; } }');
	 tc("own method", 'class R2 { use TA, TB; public function m() { echo "own\n"; } }');
	 tc("abstract", 'class R3 { use TAbs, TB; }');
	 tc("alias only", 'class R4 { use TA { only as also; } }');
	 (new R1)->m(); (new R2)->m(); (new R3)->m();`,
	// A magic accessor answers for a lazy proxy before anything is initialized
	// -- but the access *inside* it, which the re-entry guard sends to the real
	// property, is the real one, and reaching the state produces it. The proxy
	// and the instance behind it share that guard, so the accessor does not run
	// again on the way through.
	`#[AllowDynamicProperties]
	 class LazyMagic {
	   public $_;
	   public function __set($n, $v) { echo "set\n"; $this->$n = $v * 2; }
	   public function __get($n) { echo "get\n"; return $this->$n; }
	 }
	 $r = new ReflectionClass(LazyMagic::class);
	 $o = $r->newLazyProxy(function () { echo "init\n"; return new LazyMagic; });
	 $o->prop = 1;
	 var_dump($o->prop);
	 set_error_handler(function ($n, $m) { echo "warned: ", $m, "\n"; return true; });
	 $p = $r->newLazyProxy(function () { echo "init2\n"; return new LazyMagic; });
	 var_dump($p->missing);`,
	// A nested `list(list(&$b))` fetches the intermediate element only to
	// descend into it: only the leaf becomes a reference. Destructuring a value
	// nothing else names -- a function's by-value result -- gives the target a
	// copy and says so; a by-reference return is a real slot and does not.
	`$arr = [1, [2]];
	 list(&$a, list(&$b)) = $arr;
	 var_dump($a, $b, $arr);
	 $arr2 = [1, [2]];
	 list($c, &$d) = $arr2;
	 var_dump($arr2);
	 function byValue($x) { return $x; }
	 function &byRef(&$x) { return $x; }
	 $src = [1, 2];
	 [&$v] = byValue($src); var_dump($v, $src);
	 $src2 = [1, 2];
	 [&$w] = byRef($src2); var_dump($w, $src2);`,
	// `parent::$p::get()` on a property the parent declares without that hook
	// reaches the storage directly -- with the arity of the hook it stands in
	// for, and reaching a typed property that was never written is an Error
	// rather than a null. The write form answers with what it wrote.
	`class PlainP { public $backed; public int $typed; }
	 class HookC extends PlainP {
	   public $backed {
	     get { return parent::$backed::get(); }
	     set { echo "set answered: "; var_dump(parent::$backed::set($value)); }
	   }
	   public int $typed { get { return parent::$typed::get(); } }
	 }
	 $c = new HookC;
	 $c->backed = 42;
	 var_dump($c->backed);
	 try { var_dump($c->typed); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 class ArgC extends PlainP {
	   public $backed {
	     get { return parent::$backed::get(1); }
	     set { parent::$backed::set($value, 2); }
	   }
	 }
	 $a = new ArgC;
	 try { $a->backed = 1; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { var_dump($a->backed); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// Observing an object's state runs the get hooks -- foreach and
	// get_object_vars both do -- and a virtual property appears even though it
	// has no slot, in the place it was declared. A (array) cast does not: it is
	// the raw table.
	`class Hooked {
	   public $plain = "plain";
	   public $virt { get { return "V"; } set { } }
	   public $backed = "b" { get { return strtoupper($this->backed); } set { $this->backed = $value; } }
	   public $writeOnly { set { } }
	   private $hidden = "h";
	 }
	 $h = new Hooked;
	 foreach ($h as $k => $v) { echo $k, " => "; var_dump($v); }
	 var_dump(get_object_vars($h));
	 var_dump((array) $h);
	 class Inside2 { public $v { get { return "V"; } } public $p = 1;
	   function all() { $out = []; foreach ($this as $k => $x) { $out[$k] = $x; } return $out; } }
	 var_dump((new Inside2)->all());`,
	// A hooked property is never a slot to bind by reference, whatever its
	// hooks do -- even a &get that hands one out.
	`function tb($l, $fn) {
	   echo "# $l\n";
	   try { $fn(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 class HByVal { public $p { get { return $this->p; } set { $this->p = $value; } } }
	 class HByRef { public $p { &get { return $this->p; } } }
	 class HGetOnly { public $q { get { return 1; } } }
	 $target = 1;
	 tb("by-value hook", function () use (&$target) { $o = new HByVal; $o->p =& $target; });
	 tb("by-reference hook", function () use (&$target) { $o = new HByRef; $o->p =& $target; });
	 tb("virtual get only", function () use (&$target) { $o = new HGetOnly; $o->q =& $target; });`,
	// An interface may *declare* __callStatic without a body, and then there is
	// nothing to forward to: the engine reports the name that was called
	// rather than the accessor that would have answered.
	`interface AbsFwd { public static function __callStatic($m, $a); }
	 try { AbsFwd::whatever(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 abstract class AbsPlain { abstract function m(); }
	 try { AbsPlain::m(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// __COMPILER_HALT_OFFSET__ belongs to __halt_compiler(), which defines it
	// per file at compile time. define() is refused whether or not any file has
	// claimed it, and the name stays undefined either way.
	`var_dump(defined("__COMPILER_HALT_OFFSET__"));
	 var_dump(define("__COMPILER_HALT_OFFSET__", 1));
	 var_dump(defined("__COMPILER_HALT_OFFSET__"));
	 var_dump(define("__COMPILER_HALT_OFFSET__", 2));`,
	// A declaration may not name the same interface twice -- an alias of one
	// already there counts -- while one reached through another entry, or
	// inherited from the parent, is ordinary.
	`interface DupI {}
	 interface DupJ extends DupI {}
	 class DupParent implements DupI {}
	 class DupOk1 implements DupI, DupJ {}
	 class DupOk2 extends DupParent implements DupI {}
	 echo "ok\n";
	 class_alias("DupI", "DupAlias");
	 var_dump(class_exists("DupOk1"), interface_exists("DupAlias"));`,
	// A reference assignment drops what the target held *there*, so the
	// destructor runs before the expression answers -- and it can write through
	// the box the assignment just made.
	`class Drop { public $tag; function __construct($t) { $this->tag = $t; }
	   function __destruct() { $GLOBALS["a"] = null; } }
	 $a = new Drop("first"); $tmp = new Drop("second");
	 var_dump($a = &$tmp);
	 class Holder2 { public $value; }
	 class Drop2 { function __destruct() { global $box; $box->value = null; } }
	 $box = new Holder2; $box->value = new Drop2; $t2 = new Drop2;
	 var_dump($box->value = &$t2);
	 class Stat2 { static $held; function __destruct() { Stat2::$held = null; } }
	 Stat2::$held = new Stat2; $t3 = new Stat2;
	 var_dump(Stat2::$held = &$t3);`,
	// $GLOBALS is the symbol table, so removing an entry removes the variable --
	// but only unbinds it: anything else sharing the box keeps what it held.
	`$x = "here";
	 $alias = &$x;
	 unset($GLOBALS["x"]);
	 var_dump(isset($x), $alias, array_key_exists("x", $GLOBALS));
	 function reads() { global $x; return isset($x); }
	 var_dump(reads());`,
	// A trace frame names the class that *declares* the method: an inherited
	// one shows the parent, an override shows the overriding class, and a trait
	// method shows the class that used the trait.
	`class TBase { public function m() { throw new Exception("x"); } public function n() { $this->m(); } }
	 class TKid extends TBase { public function o() { $this->n(); } }
	 class TOver extends TBase { public function m() { throw new Exception("y"); } }
	 trait TT { public function t() { throw new Exception("t"); } }
	 class TUser { use TT; }
	 abstract class TStat { public static function s() { throw new Exception("s"); } }
	 class TStatKid extends TStat {}
	 $show = function ($e) { echo str_replace(__FILE__, "@", $e->getTraceAsString()), "\n--\n"; };
	 try { (new TKid)->o(); } catch (Throwable $e) { $show($e); }
	 try { (new TOver)->n(); } catch (Throwable $e) { $show($e); }
	 try { (new TUser)->t(); } catch (Throwable $e) { $show($e); }
	 try { TStatKid::s(); } catch (Throwable $e) { $show($e); }`,
	// __sleep names properties; a declared one that holds nothing yet is left
	// out silently, and only a name the class never declared is complained
	// about.
	// (The warning text itself is checked by the corpus: the engine words it
	// differently under eval, where these cases run.)
	`set_error_handler(function ($n, $m) {
	   $at = strpos($m, chr(34));
	   echo "warned: ", $at === false ? $m : substr($m, $at), "\n";
	   return true;
	 });
	 class SleepA { public int $a; public $b; public function __sleep() { return ["a", "b", "missing"]; } }
	 var_dump(serialize(new SleepA));
	 class SleepB { public int $a = 1; public function __sleep() { return ["a"]; } }
	 var_dump(serialize(new SleepB));
	 class SleepC { public $x = 1; public function __sleep() { return ["x", "y"]; } }
	 var_dump(serialize(new SleepC));`,
	// Serializing a lazy object produces its state -- __sleep only *selects*
	// from it -- unless the class supplies __serialize, which decides for
	// itself, or the object was made with SKIP_INITIALIZATION_ON_SERIALIZE.
	`class LzPlain { public int $a; }
	 class LzSleep { public int $a; public function __sleep() { return ["a"]; } }
	 class LzSer { public int $a; public function __serialize() { return []; } }
	 foreach (["LzPlain" => 0, "LzSleep" => 0, "LzSer" => 0,
	           "LzPlain*" => ReflectionClass::SKIP_INITIALIZATION_ON_SERIALIZE,
	           "LzSleep*" => ReflectionClass::SKIP_INITIALIZATION_ON_SERIALIZE] as $label => $flags) {
	   $name = rtrim($label, "*");
	   $r = new ReflectionClass($name);
	   $o = $r->newLazyGhost(function ($o) { echo "init\n"; $o->a = 1; }, $flags);
	   echo $label, " ";
	   var_dump(serialize($o));
	 }`,
	// A failure to compile is a throwable: a syntax error is a ParseError,
	// anything else the compiler refuses is a CompileError -- its parent, and
	// equally catchable. The message is the bare reason, and the position is
	// inside the code that was compiled.
	`function tryEval($code) {
	   try { eval($code); } catch (Throwable $e) {
	     echo get_class($e), " | ", $e->getMessage(), " | ", $e->getLine(),
	          " | ", str_replace(__FILE__, "@", $e->getFile()), "\n";
	     var_dump($e instanceof CompileError, $e instanceof Error);
	   }
	 }
	 tryEval('$x = []  FOO }');
	 tryEval('if (false) { class Dup { final final function m() {} } }');
	 tryEval('if (false) { class Vis { private protected $x; } }');
	 tryEval('return 1 + 1;');`,
	// A static is a shared box, not a value copied in and out: a call made from
	// inside the one that wrote it sees the write. That is what makes
	// `static $first = true; $first = false;` guard a re-entry.
	`function guard($n) {
	   static $first = true;
	   echo "guard $n first=", var_export($first, true), "\n";
	   if ($first) { $first = false; guard($n + 1); guard($n + 2); }
	 }
	 guard(1);
	 function counter() { static $n = 0; return ++$n; }
	 var_dump(counter(), counter(), counter());`,
	// A property name that *starts* with a NUL byte is refused for reading,
	// writing and unsetting -- but not for isset(), which answers false like
	// any absent name. A NUL further in is fine: that is how a mangled private
	// property is spelled.
	`function t($l, $fn) {
	   echo "# $l\n";
	   try { $fn(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 $o = new stdClass;
	 t("write", function () use ($o) { $p = chr(0); $o->$p = 1; });
	 t("write embedded", function () use ($o) { $p = "a" . chr(0) . "b"; $o->$p = 1; var_dump($o); });
	 t("read", function () use ($o) { $p = chr(0); var_dump($o->$p); });
	 t("isset", function () use ($o) { $p = chr(0); var_dump(isset($o->$p)); });
	 t("unset", function () use ($o) { $p = chr(0); unset($o->$p); });
	 t("empty name", function () use ($o) { $p = ""; $o->$p = 2; var_dump($o); });
	 class NulMagic {
	   public function __get($p) { return $this->$p; }
	   public function __set($p, $v) { $this->$p = $v; }
	   public function __unset($p) { unset($this->$p); }
	 }
	 t("through __get", function () { $m = new NulMagic; $p = chr(0); var_dump($m->$p); });
	 t("through __set", function () { $m = new NulMagic; $p = chr(0); $m->$p = 1; });
	 t("through __unset", function () { $m = new NulMagic; $p = chr(0); unset($m->$p); });`,
	// A trace escapes a string argument: not-printable-ASCII becomes \xNN in
	// upper case and a backslash doubles, while a quote is left alone. The
	// 15-byte cut happens before the escaping.
	`function boom($s) { throw new Exception("x"); }
	 $high = 255; $del = 127;
	 foreach ([chr(0), "a" . chr(0) . "b", "tab\there", "nl\nhere", "back\\slash", "quote'q",
	           chr($del), chr($high), "h\u{e9}llo", str_repeat("z", 20)] as $s) {
	   try { boom($s); } catch (Throwable $e) {
	     echo str_replace(__FILE__, "@", $e->getTraceAsString()), "\n";
	   }
	 }`,
	// An accessor is a frame in a backtrace like any other, attributed to the
	// access that ran it.
	`class Traced {
	   public function __get($n) { throw new Exception("in get"); }
	   public function __call($n, $a) { throw new Exception("in call"); }
	 }
	 $t = new Traced;
	 $clean = function ($e) { echo str_replace(__FILE__, "@", $e->getTraceAsString()), "\n"; };
	 try { $t->missing; } catch (Throwable $e) { $clean($e); }
	 try { $t->nope(1, "two"); } catch (Throwable $e) { $clean($e); }`,
	// trigger_error accepts only the four user levels; anything else is a
	// ValueError naming all of them. (E_USER_ERROR is a fatal, which the
	// engine cannot run under eval, so its own test lives in the corpus.)
	`var_dump(trigger_error("n"), trigger_error("w", E_USER_WARNING),
	          trigger_error("d", E_USER_DEPRECATED), trigger_error("x", E_USER_NOTICE));
	 try { trigger_error("bad", E_ERROR); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
	 try { trigger_error("bad", 0); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }`,
	// Serializable's deprecation is for something that can be instantiated and
	// serialized. An interface or an abstract class carrying it is not the
	// problem, and supplying *both* replacements -- own or inherited -- is
	// what the notice asks for; one of the two is not enough.
	`interface Ser extends Serializable {}
	 abstract class SerBase implements Serializable {}
	 echo "declared\n";
	 class SerPlain implements Serializable {
	   public function serialize(): string { return ""; }
	   public function unserialize(string $d): void {}
	 }
	 class SerBoth implements Serializable {
	   public function serialize(): string { return ""; }
	   public function unserialize(string $d): void {}
	   public function __serialize(): array { return []; }
	   public function __unserialize(array $d): void {}
	 }
	 class SerHalf implements Serializable {
	   public function serialize(): string { return ""; }
	   public function unserialize(string $d): void {}
	   public function __serialize(): array { return []; }
	 }
	 class SerKid extends SerBase {
	   public function serialize(): string { return ""; }
	   public function unserialize(string $d): void {}
	 }
	 abstract class SerAbstractKid implements Ser {}
	 echo "done\n";`,
	// The compound array callable. `self` and `parent` are relative to the
	// class the callable named -- so they work with no class scope at all --
	// while `static` is late static binding and needs one. Naming a scope the
	// class is not in is refused before the form is even called deprecated.
	`class CallA {
	   public static function s() { echo "s: ", get_called_class(), "\n"; }
	   public function i() { echo "i: ", static::class, "\n"; }
	 }
	 class CallB extends CallA {}
	 foreach ([["CallB", "parent::s"], ["CallB", "self::s"], ["CallB", "CallA::s"],
	           ["CallB", "CallB::s"], ["CallA", "CallB::s"], ["CallB", "static::s"]] as $c) {
	   echo "-- ", $c[0], " ", $c[1], "\n";
	   var_dump(is_callable($c));
	   try { call_user_func($c); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 class CallC extends CallA {
	   public function run() {
	     call_user_func(["CallC", "parent::i"]);
	     call_user_func(["CallC", "static::s"]);
	     call_user_func([$this, "parent::i"]);
	   }
	 }
	 (new CallC)->run();`,
	// The notice for an object beside a number names the type it was being
	// converted *to*, which is the other operand's -- not always int.
	`$o = new stdClass;
	 foreach ([1, 2.5, 0, "", "2.5", null, true, [1]] as $v) {
	   var_dump($o == $v, $o < $v);
	 }
	 class Named {}
	 $n = new Named; var_dump($n > 1.5, $n > 1);`,
	// `$o->p = &$x` needs a slot of the object's own to bind. A name only
	// __get answers for has none, and the engine refuses rather than making a
	// dynamic property beside the overloaded one.
	`function t($label, $fn) {
	   echo "# $label\n";
	   try { $fn(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 class RefGet { private $x; public function &__get($n) { return $this->x; } }
	 t("private + &__get", function () { $o = new RefGet; $y = 5; $o->x =& $y; var_dump($o->x); });
	 class ValGet { public function __get($n) { return 1; } }
	 t("by-value __get", function () { $o = new ValGet; $y = 5; $o->z =& $y; var_dump($o->z); });
	 class Bare {}
	 t("plain dynamic", function () { $o = new Bare; $y = 5; $o->z =& $y; $y = 6; var_dump($o->z); });
	 class SetOnly { public function __set($n, $v) { echo "set\n"; } }
	 t("__set only", function () { $o = new SetOnly; $y = 5; $o->z =& $y; $y = 6; var_dump($o->z); });
	 class Hidden { private $x = 3; }
	 t("private, no magic", function () { $o = new Hidden; $y = 5; $o->x =& $y; });
	 class Both { public $p = 1; public function &__get($n) { static $v = 9; return $v; } }
	 t("declared and visible", function () { $o = new Both; $y = 5; $o->p =& $y; $y = 7; var_dump($o->p); });`,
	// (string) $e is the exception, its trace, and -- printed ahead of it --
	// everything it was chained to, oldest first. An empty message drops the
	// colon rather than leaving a dangling separator.
	`function show($e) { echo str_replace(__FILE__, "@", (string) $e), "\n--\n"; }
	 function inner() { throw new RuntimeException("boom", 7); }
	 function outer() { try { inner(); } catch (Throwable $t) { throw new LogicException("wrap", 1, $t); } }
	 try { outer(); } catch (Throwable $e) { show($e); }
	 show(new Exception("m"));
	 class Silent extends Exception {}
	 try { throw new Silent(""); } catch (Throwable $e) { show($e); }
	 try { throw new Exception("interp"); } catch (Throwable $e) { show("[$e]"); }`,
	// An exception a finally throws over becomes the new one's previous -- but
	// not when that would close a loop, which the chain cannot represent.
	`try {
	   $e = new Exception("M1");
	   try { throw new Exception("M2", 0, $e); } finally { throw $e; }
	 } catch (Throwable $t) {
	   echo get_class($t), " ", $t->getMessage(), "\n";
	   var_dump($t->getPrevious()?->getMessage());
	 }
	 try {
	   try { throw new Exception("A"); } finally { throw new Exception("B"); }
	 } catch (Throwable $t) {
	   echo $t->getMessage(), " <- ", $t->getPrevious()?->getMessage(), "\n";
	 }`,
	// A constant holds its object for the whole request, so nothing else gets
	// its handle. (Handle *reuse* itself is not testable here: the engine runs
	// these through eval, where the object store behaves differently from a
	// plain script, so the .phpt corpus is where that evidence lives.)
	`define("HELD", new stdClass);
	 $id = spl_object_id(HELD);
	 $x = new stdClass; var_dump(spl_object_id($x) === $id);
	 unset($x);
	 var_dump(HELD instanceof stdClass);`,
	// Freeing an object releases what it held, and that can be the last thing
	// holding something else: the whole chain goes at once, not one link per
	// release point.
	`class Link { public $next; public $tag;
	   function __construct($tag) { $this->tag = $tag; }
	   function __destruct() { echo "gone {$this->tag}\n"; } }
	 class Owner { public $kids = [];
	   function __construct() { $this->kids[] = new Link("kid"); } }
	 new Owner();
	 echo "after owner\n";
	 $a = new Link("a"); $a->next = new Link("b"); $a->next->next = new Link("c");
	 unset($a);
	 echo "after chain\n";`,
	// gc_collect_cycles() reports what the *cycle* collector freed, which is a
	// narrower set than everything unreachable: garbage a refcount would have
	// taken is not counted, a cycle is, and so is whatever hung off it.
	`gc_collect_cycles();
	 $a = new stdClass; $a = null; var_dump(gc_collect_cycles());
	 $b = new stdClass; $b->s = $b; unset($b); var_dump(gc_collect_cycles());
	 $c = new stdClass; $d = new stdClass; $c->o = $d; $d->o = $c; unset($c, $d);
	 var_dump(gc_collect_cycles());
	 $e = new stdClass; $f = new stdClass; $e->o = $f; unset($e, $f);
	 var_dump(gc_collect_cycles());
	 $g = []; $g[] =& $g; unset($g); var_dump(gc_collect_cycles());
	 $h = []; $h[] = new stdClass; $h[0]->back =& $h; unset($h);
	 var_dump(gc_collect_cycles());
	 $i = new stdClass; $i->arr = []; $i->arr[] = $i; unset($i);
	 var_dump(gc_collect_cycles());`,
	// What hangs off a broken cycle is freed with it and counted; what merely
	// points *into* one is not.
	`$c = new stdClass; $d = new stdClass; $c->o = $d; $d->o = $c;
	 $c->extra = new stdClass; unset($c, $d); var_dump(gc_collect_cycles());
	 $e = new stdClass; $f = new stdClass; $e->o = $f; $f->o = $e;
	 $g = new stdClass; $g->o = $e; unset($e, $f, $g); var_dump(gc_collect_cycles());
	 $h = new stdClass; $h->self = $h; $h->o = new stdClass; $h->o->p = new stdClass;
	 unset($h); var_dump(gc_collect_cycles());
	 $i = []; $i[] =& $i; $i["o"] = new stdClass; unset($i);
	 var_dump(gc_collect_cycles());
	 $j = new stdClass; $j->self = $j; $k = $j; unset($j);
	 var_dump(gc_collect_cycles());
	 unset($k); var_dump(gc_collect_cycles());`,
	// A destructor still runs for garbage the collector does not count.
	`class Gd { public $x; function __destruct() { echo "gone\n"; } }
	 $a = new Gd; $a->x = $a; unset($a); var_dump(gc_collect_cycles());
	 $b = new Gd; unset($b); var_dump(gc_collect_cycles());`,
	// A ghost initializer that throws is rolled back -- but only to where it
	// started, which is not empty: a property given a value without
	// initialization keeps it, and the object is lazy again.
	`class Roll { public $a = 1; public int $b = 2; public int $c; }
	 $r = new ReflectionClass(Roll::class);
	 $o = $r->newLazyGhost(function ($o) { $o->a = 3; $o->b = 4; $o->c = 5;
	   $o->dyn = 6; throw new Exception("no"); });
	 (new ReflectionProperty(Roll::class, "c"))->setRawValueWithoutLazyInitialization($o, 0);
	 try { $r->initializeLazyObject($o); } catch (Exception $e) { echo $e->getMessage(), "\n"; }
	 var_dump($o, $r->isUninitializedLazyObject($o));`,
	// A lazy object never runs its own destructor. An uninitialized one has no
	// state to tear down; an initialized proxy leaves it to the instance.
	`class Dt { public $a; public function __destruct() { echo "bye\n"; } }
	 $r = new ReflectionClass(Dt::class);
	 $g = $r->newLazyGhost(function () { echo "ghost init\n"; });
	 $g = null;
	 $p = $r->newLazyProxy(function () { echo "proxy init\n"; return new Dt; });
	 $p = null;
	 echo "---\n";
	 $h = $r->newLazyGhost(function () { echo "ghost init\n"; });
	 var_dump($h->a);
	 $h = null;
	 echo "done\n";`,
	// The proxy compatibility rule runs the opposite way to the obvious one:
	// the proxy may be the instance's class or a subclass that adds no state.
	`class Base { public $p = "a"; }
	 class Plain extends Base { public function foo() {} }
	 class Extra extends Base { public $q = 1; }
	 class Stat extends Base { public static $s = 1; }
	 class Dtor extends Base { public function __destruct() {} }
	 class Cln extends Base { public function __clone() {} }
	 class Konst extends Base { const K = 1; }
	 foreach (["Plain", "Extra", "Stat", "Dtor", "Cln", "Konst", "Base"] as $n) {
	   $o = (new ReflectionClass($n))->newLazyProxy(fn() => new Base);
	   try { echo $n, " ", $o->p, "\n"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 }
	 $o = (new ReflectionClass("Base"))->newLazyProxy(fn() => new Plain);
	 try { echo $o->p, "\n"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// A write descent asks __get, and on a name the class does not declare the
	// accessor answers without the object ever being initialized.
	`class Mg { public $_;
	   public function &__get($n) { echo "__get $n\n"; return $n; }
	   public function __set($n, $v) { echo "__set $n\n"; } }
	 $r = new ReflectionClass(Mg::class);
	 $p = $r->newLazyProxy(function () { echo "init\n"; return new Mg; });
	 $x = &$p->x;
	 var_dump($r->isUninitializedLazyObject($p));
	 $p->_ = 1;
	 var_dump($r->isUninitializedLazyObject($p));`,
	// Cloning a lazy object initializes it first. A proxy stays a proxy, and
	// __clone runs on the copied instance rather than on the proxy shell --
	// the shell has no properties for it to work on.
	`class Val { public string $s = "A"; }
	 class Holder { public Val $v;
	   public function __construct() { $this->v = new Val; }
	   public function __clone() { $this->v = clone $this->v; } }
	 $r = new ReflectionClass(Holder::class);
	 foreach (["ghost", "proxy"] as $kind) {
	   echo "# $kind\n";
	   $o = $kind === "ghost" ? $r->newLazyGhost(function ($x) { $x->__construct(); })
	                          : $r->newLazyProxy(fn() => new Holder);
	   var_dump($r->isUninitializedLazyObject($o));
	   $c = clone $o;
	   var_dump($r->isUninitializedLazyObject($o), $r->isUninitializedLazyObject($c));
	   $o->v->s = "B";
	   var_dump($o->v->s, $c->v->s);
	 }`,
	// A reference bound to a typed property carries that property's type: the
	// check follows the slot, not the name being assigned.
	`class Tr { public int $n = 1; public $any; }
	 $t = new Tr; $t->any = &$t->n; $alias = &$t->n; $box = [&$t->n];
	 $alias .= "1"; var_dump($t->n);
	 try { $t->any = "no"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { $box[0] = []; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 $t->n = PHP_INT_MAX;
	 try { $alias++; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { $t->any--; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($t->n);`,
	// The same overflow reported on the property itself, and the coercion that
	// refuses a float-string too large for the type rather than truncating it.
	`class Ov { public int $n = PHP_INT_MAX; }
	 $o = new Ov;
	 try { $o->n++; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { $o->n = "9e99"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 $o->n = "3.7"; var_dump($o->n);`,
	`class Ref { private array $store = ['a'];
	            public array $q { &get { return $this->store; } } }
	 $r = new Ref; $r->q[] = 'b'; var_dump($r->q);
	 $alias =& $r->q; $alias[] = 'c'; var_dump($r->q);`,

	// Declaration-time checks.
	`abstract class Ab2 { abstract function foo(); }
	 interface Ib2 { function bar(); }
	 class Cb2 extends Ab2 implements Ib2 { function foo() { return 1; } function bar() { return 2; } }
	 $c = new Cb2; var_dump($c instanceof Ib2, $c->foo(), $c->bar());`,
	`class Src {} class_alias("Src", "Dst");
	 var_dump(class_alias("Src", "Dst"), new Dst instanceof Src);`,
	`interface Iface { public $prop { set; } }
	 class Plain implements Iface { public $prop; }
	 $p = new Plain; $p->prop = 3; var_dump($p->prop, $p);`,

	// What var_dump shows for a Closure, and late static binding.
	`class Kd { function m($q, &$r, $s = 1) {} static function s() {} }
	 $k = new Kd;
	 var_dump($k->m(...), Kd::s(...), strlen(...));`,
	`class Lsb { public static function who() { var_dump(get_called_class()); } }
	 class LsbB extends Lsb { public static function who() { parent::who(); self::show(); }
	   public static function show() { var_dump(static::class); } }
	 LsbB::who();
	 class LsbD extends LsbB {} LsbD::who();
	 Lsb::who();`,
	`enum Cmp { case A; case B; }
	 $a = Cmp::A; $b = Cmp::B;
	 var_dump($a > $a, $a < $a, $a >= $a, $a <= $a);
	 var_dump($a > $b, $a < $b, $a >= $b, $a <= $b);
	 var_dump($a > true, $a < true, $a >= true, $a <= true);
	 class Oc {} class Pc {} $o = new Oc; $p = new Oc; $q = new Pc;
	 var_dump($o > $p, $o < $p, $o >= $p, $o <= $p, $o < $q, $o >= $q, $a == $a, $a == $b);`,

	// Autoloading, and what a closure remembers.
	`spl_autoload_register(function ($c) { throw new RuntimeException("no $c"); });
	 try { new Missing; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { var_dump(Missing2::K); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	`class Ac { public static function f() { return function () {
	   var_dump(self::class, static::class); }; } }
	 class Bc extends Ac {}
	 $f = Bc::f(); $f();
	 ($f->bindTo(null, Ac::class))();`,
	`enum Ee { case Bar; }
	 class Bz {}
	 var_dump(enum_exists(Ee::class), enum_exists(Bz::class), class_exists(Bz::class),
	          class_exists("Nope", false), interface_exists("Nope", false));
	 if (true) { class Late {} }
	 var_dump(class_exists("Late", false));`,

	// By-reference arguments the compiler could not settle, and readonly.
	`$var = 1; $id = fn&(&$x) => $x; $ref =& $id($var); $ref++; var_dump($var);
	 $f = function (&$y) { $y = 9; }; $z = 0; $f($z); var_dump($z);
	 $srt = sort(...); $arr = [3,1,2]; $srt($arr); var_dump($arr);
	 $byval = function ($q) { $q = 5; }; $w = 1; $byval($w); var_dump($w);`,
	`enum Rd: int { case Bar = 0; }
	 function setByRef(&$b, $v) { $b = $v; }
	 try { $bar = Rd::Bar; setByRef($bar->value, 1); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 var_dump(Rd::Bar->value);
	 class Ro { public function __construct(public readonly int $v) {} }
	 $o = new Ro(5); var_dump($o->v);
	 try { setByRef($o->v, 9); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 class Rw { public int $w = 1; } $x = new Rw; setByRef($x->w, 4); var_dump($x->w);`,
	`function myf() { return 7; }
	 var_dump(function_exists("\\myf"), function_exists("\\strlen"), function_exists("nope"),
	          is_callable("\\myf"), is_callable("x\\"), is_callable("\\x"), is_callable(""),
	          \myf(), call_user_func("\\myf"));`,

	`class Sup { private $foo; static protected $bar; private function foo() {}
	   public function pf() { return property_exists($this, "foo"); }
	   public function me() { return method_exists($this, "foo"); } }
	 class Subc extends Sup { public function pb() { return property_exists($this, "bar"); } }
	 $e = new Subc;
	 var_dump($e->me(), method_exists($e, "pf"), $e->pf(), $e->pb(),
	          method_exists("Subc", "foo"), method_exists($e, "foo"));`,
	`$i = PHP_INT_MAX;
	 try { $a = [$i => 42, new stdClass]; var_dump($a); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 $b = [PHP_INT_MAX => 1];
	 try { $b[] = 2; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 $c = [1,2]; $c[] = 3; var_dump(count($c), $c);`,

	// PHP 8.4 asymmetric visibility.
	`class Av { public private(set) string $bar = "baz"; public protected(set) int $n = 1;
	   function set() { $this->bar = "in"; } }
	 $f = new Av; var_dump($f->bar);
	 try { $f->bar = "x"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 try { unset($f->bar); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 try { $f->n = 5; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 try { $r = &$f->bar; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 $f->set(); var_dump($f->bar);
	 class AvSub extends Av { function t() { $this->n = 9; } } $s = new AvSub; $s->t(); var_dump($s->n);`,
	`class Inner { public int $baz = 0; }
	 class Outer { public private(set) Inner $bar; function __construct() { $this->bar = new Inner; } }
	 $o = new Outer; $o->bar->baz = 42; var_dump($o->bar->baz);
	 $b = &$o->bar; var_dump($o);`,

	`class Uc { public int $a = 1; public $d = 2;
	   function __get($n) { return 7; } function __isset($n) { return true; }
	   function __unset($n) { echo "unset($n)\n"; } }
	 $t = new Uc; unset($t->a); unset($t->d);
	 var_dump($t, isset($t->a), $t->a, isset($t->d), $t->d);
	 class Un { public int $u; } $u = new Un; var_dump($u, isset($u->u));
	 try { var_dump($u->u); } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	`class Av2 { public private(set) string $bar;
	   function setBar($v) { $this->bar = $v; }
	   function unsetBar() { unset($this->bar); }
	   function __set($n, $v) { echo "__set\n"; } }
	 $f = new Av2;
	 try { $f->bar = "a"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 $f->setBar("b");
	 try { $f->bar = "c"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 $f->unsetBar(); $f->bar = "d";`,

	// Named arguments.
	`function na($a, $b, $c = 3) { var_dump($a, $b, $c); }
	 na(b: 2, a: 1); na(1, c: 9, b: 2);
	 foreach ([fn() => na(x: 1), fn() => na(1, a: 2), fn() => na(a: 1, c: 2)] as $f) {
	   try { $f(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }
	 function nb($a, $b = "b", $c = 3) { var_dump($a, $b, $c, func_get_args()); } nb(a: 1, c: 5);`,
	`function nv($a, ...$rest) { var_dump($a, $rest); }
	 nv(1, 2, x: 3, y: 4);
	 function nw(...$all) { var_dump($all); } nw(a: 1, b: 2);
	 class Nc { function __call($n, $a) { var_dump($n, $a); }
	            static function __callStatic($n, $a) { var_dump($n, $a); } }
	 (new Nc)->m(1, b: 2); Nc::s(x: 3);
	 $cl = function ($p, $q = 0) { var_dump($p, $q); }; $cl(q: 7, p: 8);`,

	// ++ and -- are not + 1 and - 1.
	`function step($v) { $x = $v; $x++; var_dump($x); $y = $v; $y--; var_dump($y); }
	 foreach ([null, true, false, 1, 1.5, "5", "5.5", "abc", "Az", "z", "a9", "-1", "0x1A"] as $v) { step($v); }`,
	`function step2($v) { $x = $v; $x++; var_dump($x); $y = $v; $y--; var_dump($y); }
	 step2(" "); step2("");
	 class Inc {} $o = new Inc;
	 try { $o++; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 $a = [1];
	 try { $a--; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 $b = ["k" => "z"]; $b["k"]++; var_dump($b);
	 class Pi { public $p = "Az"; } $pi = new Pi; $pi->p++; var_dump($pi->p);`,

	// readonly: two refusals at two different times, and __clone's exception.
	`class Ro2 { public readonly int $p; function init() { $this->p = 1; } }
	 $a = new Ro2;
	 try { $a->p = 5; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 $a->init();
	 try { $a->p = 6; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 try { $a->init(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 try { unset($a->p); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 class Ro2Sub extends Ro2 {} $b = new Ro2Sub;
	 try { $b->p = 5; } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	`class Cl { function __construct(public readonly int $b) {}
	   function __clone() { $this->b++;
	     try { $this->b = 9; } catch (Error $e) { echo "second: ", $e->getMessage(), "\n"; } }
	   function other() { try { $this->b = 3; } catch (Error $e) { echo "outside: ", $e->getMessage(), "\n"; } } }
	 $f = new Cl(1); $g = clone $f; var_dump($g->b); $f->other();
	 class Cl2 { function __construct(public readonly int $x) {} function __clone() {} }
	 $c = clone (new Cl2(1));
	 try { $c->x = 5; } catch (Error $e) { echo "after: ", $e->getMessage(), "\n"; }`,

	// Enum restrictions that are catchable rather than fatal.
	`enum Ed: int { case A = 0; case B = 0; }
	 foreach ([fn() => Ed::A, fn() => Ed::cases(), fn() => Ed::from(0)] as $f) {
	   try { var_dump($f()); } catch (Error $e) { echo $e->getMessage(), "\n"; } }`,

	// PHP 8.4 #[\Deprecated].
	`#[\Deprecated] function dp() {}
	 #[\Deprecated(message: "use u()", since: "8.4")] function dp2() {}
	 class Dc { #[\Deprecated] const K = 1; #[\Deprecated("gone")] public function m() {} }
	 enum De { #[\Deprecated] case A; }
	 set_error_handler(function ($code, $msg) { echo $code, ": ", $msg, "\n"; return true; });
	 dp(); dp2(); var_dump(Dc::K); (new Dc)->m(); var_dump(De::A);`,

	// A non-matching last catch resumes the search rather than leaving the frame.
	`function tcf() { try { try { try { throw new Exception("try"); }
	     catch (TypeError $e) { echo "0"; } finally { echo "1"; } }
	   finally { echo "2"; } } catch (Exception $e) { echo "3"; } finally { echo "4"; }
	   return 1; }
	 var_dump(tcf());
	 function tcf2() { try { try { throw new Exception("x"); } catch (TypeError $e) {}
	   finally { echo "1"; } } catch (Exception $e) { echo "c"; } echo "|"; }
	 tcf2();`,

	`function fch() { try { throw new Exception("try"); } finally { throw new Exception("finally"); } }
	 try { fch(); } catch (Exception $e) { do { var_dump($e->getMessage()); } while ($e = $e->getPrevious()); }
	 function fch2() { try { throw new Exception("not catch"); return true; }
	   finally { try { throw new Exception("caught"); } catch (Exception $e) {} } }
	 try { var_dump(fch2()); } catch (Exception $e) { do { var_dump($e->getMessage()); } while ($e = $e->getPrevious()); }`,

	// A typed property checks and coerces what is assigned to it.
	`class Tp { public int $i; public string $s; public float $f; public bool $b; public $u;
	   public ?int $n; public int|string $io; }
	 $t = new Tp; $t->i = "42"; $t->s = 42; $t->f = "1.5"; $t->b = 1; $t->u = "x";
	 $t->n = null; $t->io = "k";
	 var_dump($t->i, $t->s, $t->f, $t->b, $t->u, $t->n, $t->io);
	 foreach ([fn() => $t->i = "abc", fn() => $t->i = [], fn() => $t->n = "z"] as $try) {
	   try { $try(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; } }`,

	`class Sp { public static int $i = 0; public static ?string $s = null; }
	 Sp::$i = "42"; var_dump(Sp::$i);
	 try { Sp::$i = "abc"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 class Cp { public int $p = 1; public string $s = "a"; }
	 $x = new Cp;
	 try { $x->p .= "abc"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($x->p); $x->p += "3"; var_dump($x->p); $x->s .= "b"; var_dump($x->s);`,
	`function tf($a) { throw new Exception("x"); }
	 foreach ([1.0, 1.5, 1.0e100, 1, "s", null, true, [1], new stdClass] as $v) {
	   try { tf($v); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n"; } }`,

	`$f = 10e120; $s = (string)$f;
	 var_dump((int)$f, (int)$s, (int)"-1.0E+121", (int)"9999999999999999999999",
	          (int)-10e120, (int)"12abc", (int)"1e3", (int)NAN, (int)INF);`,
	`$str = "abcdef";
	 foreach (["2", "1.5", "007", "1.0E+121", 1.5, true, null, [1]] as $k) {
	   try { var_dump($str[$k]); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }`,
	`function rs(): string { $int = 42; $ref =& $int; return $int; }
	 var_dump(rs());
	 function ra(): array { $a = []; $r =& $a; unset($r); return $a; }
	 var_dump(ra());`,

	// A generator's own frame in a backtrace, and getReturn priming.
	`function g1() { return 42; yield 24; }
	 $g = g1(); var_dump($g->getReturn());
	 function g2() { yield 24; return 42; }
	 $h = g2(); var_dump($h->current()); $h->next(); var_dump($h->getReturn());
	 function g3() { yield 1; }
	 $i = g3(); try { var_dump($i->getReturn()); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }`,

	// References through arrays and variadics.
	`$ra = 1; $arr = array(&$ra); $arr[0] = 5; var_dump($ra);
	 function rbar(&$x) { $x++; }
	 function rfoo($x) { $x++; }
	 $rb = 1; call_user_func_array("rbar", array(&$rb)); var_dump($rb);
	 $rc = 1; call_user_func_array("rfoo", array(&$rc)); var_dump($rc);
	 $rd = 1; call_user_func_array("rbar", [$rd]); var_dump($rd);`,
	`function rv(&...$args) { foreach ($args as &$a) { $a++; } }
	 $r1 = [1,2,3]; rv(...$r1); var_dump($r1);
	 $r2 = [1,2]; $r3 = [3,4]; rv(...$r2, ...$r3); var_dump($r2, $r3);
	 function rw(...$args) { foreach ($args as &$a) { $a++; } }
	 $r4 = [1,2]; rw(...$r4); var_dump($r4);
	 $x = 1; $y = 2; rv($x, $y); var_dump($x, $y);`,

	// Truthiness.
	`echo ("0") ? "T" : "F";`,
	`echo ("0.0") ? "T" : "F";`,
	`echo ("") ? "T" : "F";`,
	`echo (0.0) ? "T" : "F";`,

	// The contracts INTRINSICS.md states. Each was probed against the engine
	// when that file was written; these are the tests that keep it honest, and
	// each is shaped so it would fail if the effect it claims were absent.
	// A case that would end in an uncaught fatal is caught and its class and
	// message echoed instead, since the two sides cannot be compared past a
	// bail-out.

	// Arithmetic converts strictly left to right and stops at the first operand
	// that cannot. So an earlier operand's diagnostic survives the refusal, and
	// a later one never gets to warn -- `"" % "2abc"` is a bare TypeError with
	// no "non-numeric value" for the "2abc".
	`try { var_dump(2.5 % ""); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump(2.5 % "2abc");
	 try { var_dump("" % "2abc"); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// Only the integral operators report a float they are about to truncate.
	`var_dump(2.5 + 1); var_dump(2.5 | 1);`,

	// Autovivifying a dimension has five outcomes, and true and false are not
	// alike: false deprecates and converts, true is fatal.
	`$a = []; $a["x"]["y"] = 1; var_dump($a);
	 $b = null; $b["x"] = 1; var_dump($b);
	 $c = false; $c["x"] = 1; var_dump($c);
	 try { $d = true; $d["x"] = 1; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { $e2 = 5; $e2["x"] = 1; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { $f = ""; $f["x"] = 1; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,

	// isset() is quiet about almost everything, but an offset of a type that
	// cannot be one throws -- so it is not effect-free. A declared typed
	// property that was never initialized is not set, and does not consult the
	// magic accessors.
	`$a = [1];
	 try { var_dump(isset($a[[]])); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { var_dump(empty($a[[]])); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump(isset($a[9]));
	 $s = "abc"; var_dump(isset($s["z"]), isset($s[1]));
	 class ITP { public int $i; }
	 $t = new ITP; var_dump(isset($t->i), empty($t->i));`,

	// A read of a missing dimension, variable or property warns; the same read
	// under @ does not, and the suppression does not leak past the expression.
	`$a = []; echo $a["x"]; $b = @$a["x"]; var_dump($b); echo $undef1;`,

	// finally is not a defer: its own return replaces the try's, and it can
	// swallow an exception that was already in flight.
	`function fin1() { try { return 1; } finally { echo "F"; } }
	 var_dump(fin1());
	 function fin2() { try { return 1; } finally { return 2; } }
	 var_dump(fin2());
	 function fin3() { try { throw new Exception("swallowed"); } finally { return 2; } }
	 var_dump(fin3());`,

	// yield from proxies rather than copying, and does not renumber: the
	// delegated keys are the source's own, so 0 appears twice.
	`function ydf() { yield from [1, 2]; yield 3; }
	 foreach (ydf() as $k => $v) { echo "$k=$v "; }
	 echo "\n";`,

	// Unpack is not +. Unpack takes the later value and renumbers integer keys;
	// + keeps the earlier value and preserves them.
	`var_dump([...["a" => 1], ...["a" => 2]]);
	 var_dump([...[5 => "x"], ...[5 => "y"]]);
	 var_dump(["a" => 1] + ["a" => 2]);
	 var_dump([5 => "x"] + [5 => "y"]);`,

	// Copying an array does not sever a reference inside it: the box is shared,
	// so a write through it is visible in the copy. This is why an array
	// holding a reference is never uniquely owned.
	`$a = [1, 2]; $r = &$a[0]; $b = $a; $r = 9; var_dump($b[0], $a[0]);`,

	// A string offset write past the end pads with spaces rather than failing,
	// so it is not a bounded index assignment. The empty string is a string
	// like any other here: PHP 7.0 grew an array out of it and 8 does not, so
	// `$e[0] = "a"` yields "a" rather than refusing. php2go excluded it from
	// the string path until 2026-08-28 and answered "Cannot use string offset
	// as an array" to a write the engine performs.
	`$s = "abc"; $s[1] = "Z"; var_dump($s);
	 $t = "abc"; $t[5] = "Z"; var_dump($t);
	 $u = "abc"; var_dump($u[9]);
	 $e = ""; $e[0] = "a"; var_dump($e);
	 $f = ""; $f[3] = "b"; var_dump($f);`,

	// A string offset is one byte, not a one-rune string. Construct the bytes
	// at run time so protobuf source storage is not part of this measurement.
	`$binary = hex2bin("00ff80");
	 var_dump(bin2hex($binary[1]), ord($binary[1]), bin2hex($binary[-1]));`,

	// Every key coercion diagnoses where PHP diagnoses: a fractional float
	// loses precision, true and null do not.
	`$k = []; $k[1.5] = "a"; $k[true] = "b"; $k[null] = "c"; var_dump($k);`,

	// gettype() and the name used in an operand error disagree, so they cannot
	// share an implementation.
	`var_dump(gettype(1.0), gettype(null), gettype(new stdClass), gettype([]));
	 try { var_dump([] % 1); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }`,

	// A type check against a class that does not exist answers false rather
	// than failing to resolve it.
	`$o = new stdClass; var_dump($o instanceof NoSuchClass1, "str" instanceof NoSuchClass2);`,

	// clone runs __clone, which can print, and copies an array property rather
	// than sharing it. On a non-object the error names __clone even so.
	`class CLN { public $a = [1]; public function __clone() { echo "CL"; } }
	 $x = new CLN; $y = clone $x; $y->a[] = 2; var_dump(count($x->a), count($y->a));
	 try { $z = clone 5; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,

	// Named arguments reorder by name, and unpacking a string-keyed array is
	// the same thing spelled differently. A literal cannot be passed by
	// reference at all.
	`function nma($a, $b) { echo "$a-$b\n"; }
	 nma(b: 2, a: 1);
	 nma(...["b" => 2, "a" => 1]);
	 function nmr(&$x) {}
	 try { nmr(5); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,

	// A float with no fractional part satisfies an int declaration silently;
	// one with a fraction deprecates; one out of range is refused outright
	// rather than truncated.
	`function coi(int $x) { var_dump($x); }
	 coi(5.0);
	 coi(1.5);
	 try { coi("21e50"); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,

	// echo is to_string plus a write, so a __toString that prints interleaves
	// its output before the value it returns -- echo is not a leaf.
	`class ETS { public function __toString() { echo "SIDE"; return "v"; } }
	 echo new ETS, "\n";
	 echo [1], "\n";
	 try { echo new stdClass; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// array_walk takes an object too, and what it walks is the raw property
	// table: writing through the by-reference parameter reaches the property.
	`class AW { private $hidden = 3; protected $guarded = 4; public $a = 1; public $b = 2; }
	 $o = new AW;
	 array_walk($o, function (&$v, $k) { $v = $v * 2; echo bin2hex($k), "=", $v, "\n"; });
	 var_dump((array) $o);
	 $a = [1, [2], 3]; $log = [];
	 array_walk($a, function (&$v, $k) use (&$a, &$log) {
	   $log[] = $k;
	   if ($k === 0) { unset($a[2]); $a[9] = 9; $a[1] = "changed"; }
	   $v = "v" . $v;
	 });
	 var_dump($a, $log);
	 $replaced = [1, 2];
	 try {
	   array_walk($replaced, function (&$v, $k) use (&$replaced) { $replaced = 42; });
	 } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 var_dump($replaced);
	 $n = 5;
	 $x = 1; $references = []; $references[0] =& $x; unset($references[0]);
	 var_dump($x);
	 $escaped = null; $escapeSource = [1]; array_walk($escapeSource, function (&$v) use (&$escaped) { $escaped =& $v; }); $escaped = 9;
	 var_dump($escapeSource);
	 try { array_walk($n, fn () => 1); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 try { array_walk($missing, fn () => 1); } catch (Throwable $t) { echo $t->getMessage(), "\n"; }`,
	// A by-reference foreach over an object needs a box per property. A `&get`
	// hook hands one back, so the loop variable aliases whatever it returned; a
	// plain `get` computes a value and has nothing to alias, which the engine
	// refuses -- naming the declaring class, not the object's.
	`class ByValHook {
	   public $byRef { &get { $x = 42; return $x; } }
	   public $byVal = 'byValue' { get => $this->byVal; set => $value; }
	 }
	 foreach (new ByValHook as $p => $v) { echo "$p: "; var_dump($v); }
	 try {
	   foreach (new ByValHook as $p => &$v) { var_dump($v); }
	 } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }`,
	// A private property is a different property from a subclass's of the same
	// name, hooks included: iterating $this inside the declaring class reaches
	// its own, not the one the subclass declared over it.
	`class HookA {
	   private $shared { get => 'A'; }
	   public function walk() { foreach ($this as $k => $v) { echo "$k="; var_dump($v); } }
	 }
	 class HookB extends HookA { public $shared { get => 'B'; } }
	 (new HookB)->walk();
	 foreach (new HookB as $k => $v) { echo "outer $k="; var_dump($v); }`,
	// Once the program has bound a reference to a typed property, an overflow
	// stepping that property is reported as a step through the reference --
	// while a plain assignment to the same name keeps the property wording.
	`class Step { public int $foo = 0; }
	 $plain = new Step; $plain->foo = PHP_INT_MAX;
	 try { ++$plain->foo; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 $held = new Step; $r = &$held->foo; $held->foo = PHP_INT_MAX;
	 try { $held->foo++; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 $held->foo = PHP_INT_MIN;
	 try { --$held->foo; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { $held->foo = "x"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($held->foo, $r);`,
	// Binding a reference between two typed properties: a value the
	// destination cannot take at all is a plain assignment error, while one it
	// could coerce is refused because coercing would change what the property
	// already holding the box sees.
	`class Bind { public int $i = 0; public string $s = "x"; public float $f = 1.5; }
	 $a = new Bind;
	 try { $a->i = &$a->s; } catch (TypeError $e) { echo "1: ", $e->getMessage(), "\n"; }
	 $b = new Bind; $b->s = "5";
	 try { $b->i = &$b->s; } catch (TypeError $e) { echo "2: ", $e->getMessage(), "\n"; }
	 var_dump($b->i, $b->s);
	 $c = new Bind;
	 try { $c->i = &$c->f; } catch (TypeError $e) { echo "3: ", $e->getMessage(), "\n"; }
	 var_dump($c->i, $c->f);
	 $d = new Bind; $loose = "x";
	 try { $d->i = &$loose; } catch (TypeError $e) { echo "4: ", $e->getMessage(), "\n"; }`,
	// One box cannot hold two different values: a write through a reference two
	// typed properties share, which each would coerce differently, names both.
	// unset() then detaches the property from the box rather than emptying it.
	`class Both { public int|string $x; public float|string $y; }
	 $t = new Both;
	 $r = "foobar";
	 $t->x = &$r;
	 $t->y = &$r;
	 try { $r = 42; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($r);
	 try { $r = 42.0; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($r);
	 unset($r, $t->x, $t->y);
	 $t->x = 42;
	 try { $t->y = &$t->x; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($t->x);`,
	// A static typed property behaves the same, and a box rebound to one takes
	// only *that* property's type -- not the types of whatever else shared the
	// box it replaced.
	`class RefStatics { public static iterable $it = []; public static ?array $a; }
	 RefStatics::$a = &RefStatics::$it;
	 try { RefStatics::$it = new ArrayIterator(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump(RefStatics::$it);
	 $loose = null;
	 RefStatics::$a = &$loose;
	 RefStatics::$it = new ArrayIterator();
	 try { $loose = 1; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($loose);`,
	// Rebinding a property to another box leaves the one it held: what is
	// still holding that box alone stops being a reference, and stops being
	// constrained by the property that left. Rebinding a property to the box it
	// already holds moves it to the end of that box's sources, which is the
	// order the engine names them in when they disagree.
	`class Rebind { public int $int; public ?int $nint; public ?string $nstring; }
	 $t = new Rebind;
	 $t->nint = 42; $t->int = &$t->nint;
	 $t->int = 42; $t->nint = &$t->int;
	 var_dump($t);
	 $t->nstring = null; $t->nint = &$t->nstring;
	 var_dump($t);
	 $t->nint = null; $t->nstring = &$t->nint;
	 var_dump($t);
	 try { $t->nstring = "42"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 var_dump($t->nint, $t->nstring);`,
	// The operand naming a property is converted to a string the way anything
	// else is: an object goes through __toString, an array warns and becomes
	// "Array", and an object without __toString is an Error -- on the read
	// path, the write path, isset(), unset() and a compound assignment alike.
	`class PropName { public int $p = 1; public $q = 2; }
	 $named = new class { public function __toString(): string { return "q"; } };
	 $opaque = new stdClass;
	 $o = new PropName;
	 $o->$named = 5;
	 var_dump($o->q, $o->$named);
	 $o->$named .= "!";
	 var_dump($o->q);
	 try { $o->$opaque = 1; } catch (Throwable $e) { echo "w: ", get_class($e), ": ", $e->getMessage(), "\n"; }
	 try { var_dump($o->$opaque); } catch (Throwable $e) { echo "r: ", $e->getMessage(), "\n"; }
	 try { var_dump(isset($o->$opaque)); } catch (Throwable $e) { echo "i: ", $e->getMessage(), "\n"; }
	 try { unset($o->$opaque); } catch (Throwable $e) { echo "u: ", $e->getMessage(), "\n"; }
	 $list = [1];
	 var_dump($o->$list);
	 $number = 5;
	 $o->$number = 9;
	 var_dump($o);`,
	// __get standing in for a declared property that was unset still has to
	// produce that property's type -- on a write-mode fetch as much as on a
	// read. A `&__get` hands back the box itself, so the coercion lands in it,
	// and the box does not lose its `&` just because the accessor returned.
	`class MagicTyped {
	   public $prop = "42";
	   public int $val;
	   public function &__get($name) { return $this->prop; }
	 }
	 $a = new MagicTyped;
	 unset($a->val);
	 var_dump($a);
	 $bound = &$a->val;
	 var_dump($bound, $a);
	 $a->prop = "x";
	 var_dump($a, $bound);
	 $b = new MagicTyped;
	 $keep = &$b->prop;
	 $b->prop = "x";
	 unset($b->val);
	 try { var_dump($b->val); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump($b);`,
	// `X::class` is the one place a missing class scope is worded as a refusal
	// to *use* the keyword; everything else reached through self, parent or
	// static says "Cannot access ... when no class scope is active".
	`$probe = [
	   'self::class'   => fn () => self::class,
	   'static::class' => fn () => static::class,
	   'parent::class' => fn () => parent::class,
	   'self::FOO'     => fn () => self::FOO,
	   'self::m()'     => fn () => self::m(),
	   'new self'      => fn () => new self,
	   'static::m()'   => fn () => static::m(),
	   'parent::m()'   => fn () => parent::m(),
	   'self::$p'      => fn () => self::$p,
	 ];
	 foreach ($probe as $label => $fn) {
	   try { var_dump($fn()); } catch (Throwable $t) { echo "$label: ", get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// An array whose next integer key is taken has nowhere to append, and says
	// so before anything the assignment itself would say -- including the
	// notice for binding a reference to a value.
	`function amrVal() { return 42; }
	 $var = 24;
	 $arr = [PHP_INT_MAX => "foo"];
	 try { var_dump($arr[] = &$var); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 var_dump(count($arr));
	 try { var_dump($arr[] = &amrVal()); } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 var_dump(count($arr));
	 try { $arr[] = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
	// array_merge_recursive merges *into* the entry it already has, so the
	// destination keeps its keys and its next free index -- and overflows on
	// them where the engine does.
	`var_dump(array_merge_recursive(["a" => [5 => "x"]], ["a" => ["y"]]));
	 var_dump(array_merge_recursive(["a" => [5 => "x"]], ["a" => "scalar"]));
	 var_dump(array_merge_recursive(["a" => "s1"], ["a" => "s2"]));
	 var_dump(array_merge_recursive(["a" => ["k" => 1]], ["a" => ["k" => 2]]));
	 var_dump(array_merge_recursive([3 => "a"], [7 => "b"]));
	 try { array_merge_recursive(["" => [PHP_INT_MAX => null]], ["" => [null]]); }
	 catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 try { array_merge_recursive(["foo" => [PHP_INT_MAX => null]], ["foo" => "aa"]); }
	 catch (Throwable $e) { echo $e->getMessage(), "\n"; }`,
	// An object's property table is walked live: a property the body adds is
	// reached before the loop ends, by value as much as by reference. An array
	// is not -- a by-value loop iterates a copy.
	`class LiveWalk { public $a = 1; }
	 $o = new LiveWalk;
	 foreach ($o as $k => $v) {
	   echo "$k=", var_export($v, true), "\n";
	   if ($k === "a") { $o->b = 2; }
	   if ($k === "b") { $o->c = 3; }
	 }
	 var_dump($o);
	 $arr = ["a" => 1];
	 foreach ($arr as $k => $v) { echo "arr $k\n"; if ($k === "a") { $arr["b"] = 2; } }
	 var_dump($arr);`,
	// A private property belongs to the class that declared it, whatever
	// subclass the object is: iterating $this inside the declaring class sees
	// it, and iterating from outside does not.
	`class PrivBase { private $priv = "ok";
	   public function walk() { foreach ($this as $k => $v) { echo "in $k: $v\n"; } }
	 }
	 class PrivSub extends PrivBase { public $open = "seen"; }
	 $s = new PrivSub;
	 $s->walk();
	 foreach ($s as $k => $v) { echo "out $k: $v\n"; }
	 var_dump($s);`,
	// An array cast to an object names its integer keys by their digits, and
	// that name has to be one a property access can find again: `$o->$p` with
	// $p = "0" reaches the property, and binding a reference to it does not
	// make a second one alongside.
	`$o = (object) [1, 2, 3];
	 var_dump($o, get_object_vars($o), (array) $o, json_encode($o));
	 print_r($o);
	 var_dump(isset($o->{"1"}), $o->{"2"});
	 foreach ($o as $p => $v) { echo "$p : $v\n"; $r = &$o->$p; }
	 var_dump($o);`,
	// `self::` in a class constant's initializer means the class that *wrote*
	// it, not the one it is read through -- even when the subclass declares its
	// own constant of that name. The engine flattens inherited constants into
	// every subclass's table, so where the constant is found says nothing about
	// where it came from.
	`class ConstBase {
	   const A = "Base::A";
	   const B = self::A . " and " . self::C;
	   const C = "Base::C";
	 }
	 class ConstSub extends ConstBase { const A = "Sub::A"; const C = "Sub::C"; }
	 var_dump(ConstBase::B, ConstSub::B);`,
	// A variadic parameter is not named in the argument TypeError: the position
	// belongs to the argument, not to the parameter, so naming `$rest` would
	// name the wrong thing.
	`function variadicTyped(int $a, ?int ...$rest) {}
	 function plainTyped(int $a, ?int $b) {}
	 try { variadicTyped(1, "x"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
	 try { plainTyped(1, "x"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }`,
	// define() will not make a class constant, whatever is on either side of
	// the separator.
	`foreach (["foo::bar", "::", "A::B::C"] as $name) {
	   try { var_dump(define($name, 1)); }
	   catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 var_dump(define("plainName", 1), plainName);`,
	// A string offset write that reaches past the front of the string warns and
	// does nothing -- and the assignment answers null, not the value it was
	// handed.
	`$s = ""; var_dump($s[-1] = "a"); var_dump($s);
	 $t = "abc"; var_dump($t[-1] = "z"); var_dump($t);
	 $u = "abc"; var_dump($u[-5] = "z"); var_dump($u);`,
	// empty() needs a value, and only __get can supply one: a class whose
	// __isset says a property is set but which cannot say what it holds is
	// empty either way.
	`class NoGet { private $d = ["foo" => "", "bar" => "x"];
	   public function __isset($n): bool { return isset($this->d[$n]); }
	 }
	 class WithGet extends NoGet { private $held = ["foo" => "", "bar" => "x"];
	   public function __isset($n): bool { return isset($this->held[$n]); }
	   public function __get($n) { return $this->held[$n]; }
	 }
	 $a = new NoGet;
	 var_dump(isset($a->foo), empty($a->foo), empty($a->bar), empty($a->nope));
	 $b = new WithGet;
	 var_dump(isset($b->foo), empty($b->foo), empty($b->bar), empty($b->nope));`,
	// Converting a *string* to float goes through the C library, which keeps
	// the sign of a zero: (float)"-0" is -0.0 where (float)0 is 0.0. A
	// backtrace spells the special values PHP's way rather than Go's.
	`var_dump((float) "-0", (float) "0", (float) "-0.0", (float) " -0", (float) "-1",
	   -0.0, (int) "-0", "-0" + 0.0, (float) "x");
	 function takesInt(int $i) {}
	 foreach ([NAN, INF, -INF, 0.5] as $v) {
	   try { takesInt($v); } catch (TypeError $e) { echo $e->getTraceAsString(), "\n"; }
	 }`,
	// A string offset is refused by what was about to be done with it: an
	// increment says so rather than complaining about indexing.
	`$probe = [
	   'increment'  => function () { $s = "ab"; $s[0]++; },
	   'pre-inc'    => function () { $s = "ab"; ++$s[0]; },
	   'decrement'  => function () { $s = "ab"; $s[0]--; },
	   'compound'   => function () { $s = "ab"; $s[0] += 1; },
	   'index'      => function () { $s = "ab"; $s[0][0] = 1; },
	   'append'     => function () { $s = "ab"; $s[0][] = 1; },
	   'unset'      => function () { $s = "ab"; unset($s[0][0]); },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "$label: ";
	   try { $fn(); echo "ok\n"; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// Making an object lazy again throws its state away, which is what its
	// destructor is for -- unless SKIP_DESTRUCTOR says otherwise. The object
	// has to be an instance of the reflected class, and one that is already
	// waiting for an initializer has nothing to throw away.
	`class ResetMe { public int $a = 0;
	   public function __construct() { $this->a = 1; }
	   public function __destruct() { echo "destruct\n"; }
	 }
	 class ResetSub extends ResetMe {}
	 class ResetOther {}
	 $r = new ReflectionClass(ResetMe::class);
	 echo "# subclass\n";
	 $r->resetAsLazyGhost(new ResetSub, function () {});
	 echo "# wrong class\n";
	 try { $r->resetAsLazyGhost(new ResetOther, function () {}); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 echo "# destructor\n";
	 $o = new ResetMe;
	 $r->resetAsLazyGhost($o, function ($o) { $o->__construct(); });
	 echo "# already lazy\n";
	 try { $r->resetAsLazyGhost($o, function () {}); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 echo "# skipped\n";
	 $p = new ResetMe;
	 $r->resetAsLazyGhost($p, function ($o) { $o->__construct(); }, ReflectionClass::SKIP_DESTRUCTOR);
	 echo "# done\n";`,
	// A class with its own object-creation handler carries engine state a lazy
	// object cannot stand in for. stdClass has none and may be made lazy;
	// Exception and ArrayObject have one, and so does anything extending them.
	`class LazyExc extends Exception {}
	 class LazyStd extends stdClass { public int $a = 1; }
	 foreach ([stdClass::class, Exception::class, LazyExc::class, LazyStd::class,
	           ArrayObject::class] as $name) {
	   $r = new ReflectionClass($name);
	   try { $r->newLazyGhost(function ($o) {}); echo "$name: ok\n"; }
	   catch (Throwable $e) { printf("%s: %s: %s\n", $name, $e::class, $e->getMessage()); }
	 }`,
	// A proxy keeps its own class for a method call: the method is the proxy's,
	// which may be a subclass of what the instance behind it is, and $this is
	// the proxy -- only its property reads forward. While the initializer runs
	// the object is not labelled lazy at all.
	`class ProxA { public string $s = "from-A"; public function who() { return static::class . ":" . $this->s; } }
	 class ProxB extends ProxA { public function foo() { return "B::foo " . $this->s; } }
	 $r = new ReflectionClass(ProxB::class);
	 $o = $r->newLazyProxy(function ($p) { echo "init: "; var_dump($p); $a = new ProxA; $a->s = "instance"; return $a; });
	 var_dump(get_class($o), $o->foo(), $o->who(), $o === $o);
	 $g = new ReflectionClass(ProxA::class);
	 $ghost = $g->newLazyGhost(function ($p) { echo "ghost: "; var_dump($p); });
	 var_dump($ghost->s);`,
	// The options mask takes two bits, and SKIP_DESTRUCTOR only means something
	// where there is state to destroy -- so only the reset forms accept it.
	`class OptC { public $a = 1; }
	 $r = new ReflectionClass(OptC::class);
	 $probe = [
	   'ghost -1'        => fn () => $r->newLazyGhost(function () {}, -1),
	   'proxy -1'        => fn () => $r->newLazyProxy(function () {}, -1),
	   'ghost skip dtor' => fn () => $r->newLazyGhost(function () {}, ReflectionClass::SKIP_DESTRUCTOR),
	   'reset -1'        => fn () => $r->resetAsLazyGhost(new OptC, function () {}, -1),
	   'reset proxy -1'  => fn () => $r->resetAsLazyProxy(new OptC, function () {}, -1),
	   'ghost serialize' => fn () => $r->newLazyGhost(function () {}, ReflectionClass::SKIP_INITIALIZATION_ON_SERIALIZE) !== null,
	   'reset skip dtor' => fn () => $r->resetAsLazyGhost(new OptC, function () {}, ReflectionClass::SKIP_DESTRUCTOR),
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "$label: ";
	   try { var_dump($fn()); } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 }`,
	// A generator that ended by throwing is finished but has not *returned*, so
	// getReturn() refuses just as it does before the body reaches its return.
	// Generator::throw() takes a Throwable and nothing else, and a Generator
	// cannot be cloned at all.
	`function genReturns() { yield 1; return 7; }
	 function genThrows() { throw new Exception("boom"); yield 1; return 2; }
	 $a = genReturns();
	 try { var_dump($a->getReturn()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 foreach ($a as $x) {}
	 var_dump($a->getReturn());
	 $b = genThrows();
	 try { $b->next(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 try { var_dump($b->getReturn()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 $c = genReturns(); $c->current();
	 foreach ([new stdClass, 1, new RuntimeException("ok")] as $bad) {
	   try { $c->throw($bad); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	// Some engine classes have no clone handler at all, and the refusal is
	// worded the way an enum case's is. Which classes is the engine's answer:
	// ArrayObject and Closure clone, Generator and WeakReference do not.
	`function genClone() { yield 1; }
	 $probe = ['Generator' => genClone(), 'stdClass' => new stdClass,
	           'ArrayObject' => new ArrayObject, 'Closure' => function () {},
	           'WeakMap' => new WeakMap, 'WeakReference' => WeakReference::create(new stdClass)];
	 foreach ($probe as $label => $o) {
	   try { $c = clone $o; echo "$label: ok\n"; }
	   catch (Throwable $e) { echo "$label: ", get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	// A generator abandoned while suspended is unwound so its finally blocks
	// run -- but they may not suspend, since there is nobody left to resume
	// them. What they throw belongs to the point the program lost the
	// generator, so a try around that point catches it.
	`function fcInner() { echo "inner\n"; yield 1; }
	 function fcYield() {
	   try { echo "try\n"; yield 1; } finally { echo "finally\n"; yield 2; }
	 }
	 function fcFrom() {
	   try { echo "try\n"; yield from fcInner(); } finally { echo "finally\n"; yield from fcInner(); }
	 }
	 foreach (["yield" => "fcYield", "yield from" => "fcFrom"] as $label => $maker) {
	   echo "## $label\n";
	   try { $g = $maker(); $g->rewind(); unset($g); }
	   catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	// A generator that two others delegate to, which then aborts: the first
	// sees the exception, and the rest find there is no return value to take.
	`function abortSource() { yield 1; throw new Exception("boom"); }
	 function abortDelegate($g) {
	   try { var_dump(yield from $g); } catch (Throwable $e) { echo "caught ", get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 $source = abortSource();
	 $gens = [abortDelegate($source), abortDelegate($source)];
	 foreach ($gens as $g) { $g->current(); }
	 do { foreach ($gens as $g) { $g->next(); } } while ($gens[0]->valid());`,
	// trigger_error raises at the E_USER_* levels, which error_reporting
	// filters on separately from the engine's own -- and an error handler is
	// told the E_USER_* code.
	`error_reporting(E_ALL & ~E_USER_NOTICE);
	 trigger_error("N", E_USER_NOTICE);
	 trigger_error("W", E_USER_WARNING);
	 error_reporting(E_ALL & ~E_USER_WARNING);
	 trigger_error("N2", E_USER_NOTICE);
	 trigger_error("W2", E_USER_WARNING);
	 error_reporting(E_ALL & ~E_NOTICE);
	 trigger_error("N3", E_USER_NOTICE);
	 error_reporting(E_ALL);
	 set_error_handler(function ($no, $msg) { echo "handler $no: $msg\n"; return true; });
	 trigger_error("H1", E_USER_WARNING);
	 trigger_error("H2", E_USER_NOTICE);
	 trigger_error("H3", E_USER_DEPRECATED);
	 restore_error_handler();`,
	// A fiber keeps its own error_reporting level: it inherits the one in force
	// when it first starts, with any `@` disregarded, and neither side's later
	// changes reach the other.
	`error_reporting(E_ALL);
	 $f = new Fiber(function () {
	   echo "in-start: ", error_reporting(), "\n";
	   Fiber::suspend();
	   error_reporting(E_ERROR);
	   echo "in-after-set: ", error_reporting(), "\n";
	   Fiber::suspend();
	   echo "in-resume2: ", error_reporting(), "\n";
	 });
	 @$f->start();
	 echo "out1: ", error_reporting(), "\n";
	 @$f->resume();
	 echo "out2: ", error_reporting(), "\n";
	 $f->resume();
	 echo "out3: ", error_reporting(), "\n";
	 error_reporting(E_ERROR | E_WARNING);
	 $g = new Fiber(function () { echo "g: ", error_reporting(), "\n"; });
	 $g->start();
	 error_reporting(E_ALL);`,
	// fiber.stack_size is refused outright when negative and refused later --
	// when a fiber actually needs a stack -- when it is smaller than two pages.
	// Left alone it means the platform's default, so an empty string that was
	// never assigned is not the same as one that was.
	// The engine's ini table outlives one case in *both* directions, and this
	// case reads the value it inherits -- `ini_set` answers the previous one.
	// Seeding it here is what makes a repeat run give the same answer as the
	// first: without it the opening `set=` is whatever the last run left, which
	// is `1M` from the restore below. Found by `GATE_REPEAT_TESTS=1`, not by
	// anyone noticing.
	`ini_set("fiber.stack_size", "");
	 $sizes = ["4096", "8192", "16384", "1M", "-1", "0", ""];
	 foreach ($sizes as $v) {
	   $set = ini_set("fiber.stack_size", $v);
	   $f = new Fiber(function () { echo "  ran\n"; });
	   echo var_export($v, true), " set=", var_export($set, true),
	        " get=", var_export(ini_get("fiber.stack_size"), true), "\n";
	   try { $f->start(); } catch (Throwable $e) { echo "  ", get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 // And the other direction: put it back, or a later case that starts a
	 // fiber finds no stack for it.
	 ini_set("fiber.stack_size", "1M");`,
	// A fiber abandoned while suspended is unwound where the program lost it,
	// and a suspend inside the finally that runs then has nowhere to go back
	// to -- so the FiberError belongs to that point and a try there catches it.
	`try {
	   (function (): void {
	     $fiber = new Fiber(function (): void {
	       try { Fiber::suspend(); } finally { Fiber::suspend(); }
	     });
	     $fiber->start();
	   })();
	 } catch (FiberError $e) { echo $e->getMessage(), "\n"; }
	 echo "done\n";`,
	// A hooked property with only a set hook and no backing storage cannot be
	// read at all, and one with only a get hook cannot be written. Both name
	// the *object's* class rather than the one that declared the hooks, as does
	// the refusal to unset one.
	`class HookSetOnly { public $p { set { echo "set\n"; } } }
	 class HookSetSub extends HookSetOnly {}
	 class HookGetOnly { public $p { get => 1; } }
	 class HookGetSub extends HookGetOnly {}
	 class HookBacked { public $p { set { $this->p = $value; } } }
	 $probe = [
	   'read write-only'  => function () { $o = new HookSetSub; $o->p = 1; return $o->p; },
	   'isset write-only' => fn () => isset((new HookSetOnly)->p),
	   'write read-only'  => function () { $o = new HookGetSub; $o->p = 1; },
	   'read read-only'   => fn () => (new HookGetSub)->p,
	   'backed set'       => function () { $o = new HookBacked; $o->p = 5; return $o->p; },
	   'unset hooked'     => function () { $o = new HookGetSub; unset($o->p); },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "$label: ";
	   try { var_dump($fn()); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// `$x .= $y` renders its operands the way `$x . $y` does: an object through
	// __toString, an array with a warning, and a class without one an Error.
	`class ConcatMe { public function __toString(): string { return "abc"; } }
	 $t = new ConcatMe; $t .= $t; var_dump($t);
	 $s = "x"; $s .= new ConcatMe; var_dump($s);
	 $q = "y";
	 try { $q .= new stdClass; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 var_dump($q);
	 $r = "z"; $r .= [1]; var_dump($r);`,
	// A static-form call that lands on __call is refused: the trampoline the
	// engine picks stands in for an instance method, so the static-call rule
	// applies -- naming the method that was asked for. is_callable still
	// answers true, and only from inside an instance of the class: __call is
	// what a class name forwards to there, __callStatic otherwise.
	`class StaticCall {
	   public function real() { echo "real\n"; }
	   public function __call($name, $args) { echo "call $name\n"; }
	   public function inside() {
	     foreach ([["StaticCall", "real"], "StaticCall::real",
	               ["StaticCall", "bar"], "StaticCall::bar"] as $m) {
	       try { $m(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	     }
	     var_dump(is_callable("StaticCall::bar"), is_callable(["StaticCall", "real"]));
	   }
	 }
	 (new StaticCall)->inside();
	 echo "-- call_user_func lends $this\n";
	 (function () { call_user_func(["StaticCall", "bar"]); })->call(new StaticCall);
	 echo "-- outside\n";
	 foreach ([["StaticCall", "real"], "StaticCall::bar"] as $m) {
	   try { $m(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }
	 var_dump(is_callable("StaticCall::bar"), is_callable(["StaticCall", "real"]));`,
	// The index of `$a[$k] = $v` is read before the value: with both undefined,
	// $k is reported first.
	`$a = [];
	 $a[$k] = $v;
	 var_dump($a);
	 $s = "abc"; $s[$i] = "z"; var_dump($s);`,
	// A generator can be rewound while it is still sitting on its first
	// yielded value -- rewind() is the start -- but not once it has been
	// resumed past it. Reaching the first yield through an empty `yield from`
	// still counts as being on it.
	`function rewindable() { yield 1; yield 2; }
	 $probe = [
	   'fresh'            => function () { $g = rewindable(); $g->rewind(); return $g->current(); },
	   'current then'     => function () { $g = rewindable(); $g->current(); $g->rewind(); return $g->current(); },
	   'next then'        => function () { $g = rewindable(); $g->next(); $g->rewind(); return $g->current(); },
	   'send then'        => function () { $g = rewindable(); $g->send(9); $g->rewind(); return $g->current(); },
	   'empty yield from' => function () { $g = (function () { yield from []; yield 4; })(); $g->current(); $g->rewind(); return $g->current(); },
	   'twice next'       => function () { $g = (function () { yield from [1, 2, 3]; })(); $g->next(); $g->next(); $g->rewind(); return $g->current(); },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "$label: ";
	   try { var_dump($fn()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	// The late static binding travels with a first-class callable and with
	// Closure::fromCallable: the class *named* decides what `static::` answers,
	// not the one that declared the method.
	`class LsbBase { public static function which() { return static::class; }
	   public function inst() { return static::class; } }
	 class LsbSub extends LsbBase {}
	 $a = LsbBase::which(...);
	 $b = LsbSub::which(...);
	 echo $a(), " ", $b(), "\n";
	 echo Closure::fromCallable(["LsbBase", "which"])(), " ",
	      Closure::fromCallable(["LsbSub", "which"])(), "\n";
	 $o = new LsbSub;
	 echo ($o->inst(...))(), "\n";`,
	// Binding a reference *into* a readonly property is refused the same way
	// taking one out of it is -- a foreach whose loop variable is the property
	// included.
	`class ReadonlyBind { public function __construct(public readonly int $v) {} }
	 class WritableBind { public int $w = 0; }
	 $o = new ReadonlyBind(1);
	 try { $r = &$o->v; } catch (Error $e) { echo "ref: ", $e->getMessage(), "\n"; }
	 try { foreach ([1] as &$o->v) {} } catch (Error $e) { echo "foreach: ", $e->getMessage(), "\n"; }
	 try { $o->v[] = 1; } catch (Error $e) { echo "append: ", $e->getMessage(), "\n"; }
	 var_dump($o->v);
	 $p = new WritableBind;
	 foreach ([7] as &$p->w) {}
	 var_dump($p->w);`,
	// json_encode presents an object the way its public properties read, which
	// for a hooked one means running the get hook -- a virtual property has no
	// slot to walk and would otherwise be missing entirely.
	`class JsonHooked {
	   public int $a = 1;
	   private int $p = 9;
	   protected int $q = 8;
	   public int $c { get { return $this->a + 2; } }
	   public int $d { get { return $this->a; } set { $this->a = $value; } }
	 }
	 var_dump(json_encode(new JsonHooked), get_object_vars(new JsonHooked));
	 class JsonPlain implements JsonSerializable { public function jsonSerialize(): mixed { return [1, 2]; } }
	 var_dump(json_encode(new JsonPlain), json_encode((object) ["x" => 1]));`,
	// printf's %s and implode's pieces are rendered the way any other string
	// is: an object goes through __toString. One that cannot be converted is
	// still an Error -- but printf has written what it built by then.
	`class Renders { public function __toString(): string { return "STR"; } }
	 printf("[%s]\n", new Renders);
	 var_dump(sprintf("[%s]", new Renders), vsprintf("[%s]", [new Renders]),
	          implode(",", [new Renders, new Renders]), implode(",", ["a", "b"]));
	 try { printf("[%s]", new stdClass); } catch (Error $e) { echo "\n", $e->getMessage(), "\n"; }
	 try { var_dump(implode(",", [new stdClass, "b"])); } catch (Error $e) { echo "implode: ", $e->getMessage(), "\n"; }
	 try { var_dump(sprintf("[%s]", new stdClass)); } catch (Error $e) { echo "sprintf: ", $e->getMessage(), "\n"; }
	 echo "end\n";`,
	// An offset an array cannot hold is refused wherever it appears, and the
	// wording follows what was being done with it: unset() says "unset offset",
	// isset() and empty() say "in isset or empty", and everything else --
	// including an array literal's key -- says "on array". A *constant* key
	// the compiler can see is a different matter: `[[] => 2]` never runs.
	`$a = ["x" => 1];
	 $probe = [
	   'unset'   => function () use (&$a) { unset($a[[]]); },
	   'isset'   => fn () => isset($a[[]]),
	   'empty'   => fn () => empty($a[[]]),
	   'read'    => fn () => $a[[]],
	   'write'   => function () use (&$a) { $a[new stdClass] = 1; },
	   'literal' => fn () => [new stdClass => null],
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "$label: ";
	   try { var_dump($fn()); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }
	 var_dump($a);`,
	// A constructor the calling scope cannot reach refuses before the object
	// exists, so no destructor runs for it -- unlike a constructor that throws,
	// where the object was made and then abandoned.
	`class PrivCtor { private function __construct() {} public function __destruct() { echo "dtor PrivCtor\n"; } }
	 class ThrowCtor { public function __construct() { throw new Exception("boom"); }
	   public function __destruct() { echo "dtor ThrowCtor\n"; } }
	 class ProtCtor { protected function __construct() {} }
	 class ProtSub extends ProtCtor { public static function make() { return new ProtCtor; } }
	 class FactoryCtor {
	   private function __construct(public int $v) {}
	   public static function make(int $v): static { return new static($v); }
	 }
	 class FactorySub extends FactoryCtor {}
	 foreach ([
	   'private'  => fn () => new PrivCtor,
	   'throwing' => fn () => new ThrowCtor,
	   'protected'=> fn () => new ProtCtor,
	   'from sub' => fn () => ProtSub::make() instanceof ProtCtor,
	   'factory'  => fn () => [FactoryCtor::make(1)->v, FactorySub::make(2)->v],
	 ] as $label => $fn) {
	   echo "$label: ";
	   try { var_dump($fn()); } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
	 }`,
	// A by-reference return hands back the slot, so the declared return type is
	// checked against what the slot holds when the function really returns --
	// after the finally, which the compiler emits a second check for.
	`function &refInt(): int { $x = 0; try { return $x; } finally { $x = "test"; } }
	 function valInt(): int { $x = 0; try { return $x; } finally { $x = "test"; } }
	 function &refOk(): int { $x = 0; try { return $x; } finally { $x = 5; } }
	 function &refLive() { $x = 0; $r = &$x; $x = 7; return $r; }
	 try { $a = &refInt(); var_dump($a); } catch (Error $e) { echo "ref: ", $e->getMessage(), "\n"; }
	 try { var_dump(valInt()); } catch (Error $e) { echo "val: ", $e->getMessage(), "\n"; }
	 $b = &refOk(); var_dump($b);
	 $c = &refLive(); var_dump($c);`,
	// A closure with a scope is named in a trace the way a method is: an arrow
	// for one with a $this, `::` for one without, and nothing at all for a
	// closure written outside a class.
	`function traceTop() { $c = function () { throw new Exception("x"); }; $c(); }
	 class TraceK {
	   public function inst() { $c = function () { throw new Exception("x"); }; $c(); }
	   public static function stat() { $c = function () { throw new Exception("x"); }; $c(); }
	 }
	 foreach ([fn () => traceTop(), fn () => (new TraceK)->inst(), fn () => TraceK::stat()] as $f) {
	   try { $f(); } catch (Throwable $e) {
	     foreach ($e->getTrace() as $frame) {
	       $name = $frame["function"];
	       // The closure's own name carries the file it was written in, which
	       // differs between the engine and this runtime; the prefix is what
	       // this case is about.
	       if (str_starts_with($name, "{closure")) { $name = "{closure}"; }
	       echo ($frame["class"] ?? ""), ($frame["type"] ?? ""), $name, "\n";
	     }
	     echo "---\n";
	   }
	 }`,
	// A message that names a *value* spells the booleans and null out rather
	// than calling them "bool": "on false", not "on bool".
	`function nameOf($label, $fn) {
	   echo "$label: ";
	   try { $fn(); echo "ok\n"; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 }
	 foreach ([false, true, null, 1, 1.5, "s", []] as $v) {
	   nameOf("member " . gettype($v), function () use ($v) { $v->bar(); });
	   nameOf("assign " . gettype($v), function () use ($v) { $v->p = 1; });
	   nameOf("foreach " . gettype($v), function () use ($v) { foreach ($v as $x) {} });
	 }`,
	// A method an engine class does not have is the program's mistake, not a
	// gap in this runtime: the class's own method list is what says which.
	`$probe = [new stdClass, new ArrayObject, new Exception("x"), new ArrayIterator([1]),
	           (function () { yield 1; })(), function () {}];
	 foreach ($probe as $o) {
	   try { $o->nope(); } catch (Throwable $e) { echo get_class($o), ": ", $e->getMessage(), "\n"; }
	 }
	 var_dump((new ArrayObject([1, 2]))->count(), (new Exception("m"))->getMessage());`,
	// A named argument keeps its name whichever SEND opcode carries it,
	// including the by-reference one -- a callee whose parameters are all `&`
	// gets its arguments through ZEND_SEND_REF rather than SEND_VAR.
	`function byValNamed($a, $b, $c = "c", $d = "d", $e = "e") { echo "a=$a, b=$b, c=$c, d=$d, e=$e\n"; }
	 function byRefNamed(&$a, &$b, &$c = "c", &$d = "d", &$e = "e") { echo "a=$a, b=$b, c=$c, d=$d, e=$e\n"; }
	 $a = "A"; $b = "B"; $c = "C"; $d = "D"; $e = "E";
	 byValNamed("A", "B", "C", d: "D", e: "E");
	 byValNamed("A", "B", "C", e: "E", d: "D");
	 byValNamed(e: "E", a: "A", d: "D", b: "B", c: "C");
	 byValNamed("A", "B", "C", e: "E");
	 byRefNamed($a, $b, $c, d: $d, e: $e);
	 byRefNamed($a, $b, $c, e: $e, d: $d);
	 byRefNamed(e: $e, a: $a, d: $d, b: $b, c: $c);
	 byRefNamed(a: $a, b: $b, c: $c, e: $e);`,
	// class_alias adds a second name to the class table, and get_declared_*
	// lists it -- under the name the table holds it by, which is lowercased.
	`trait AliasT {} class AliasK {} interface AliasI {}
	 class_alias("AliasT", "AliasTA");
	 class_alias("AliasK", "AliasKA");
	 class_alias("AliasI", "AliasIA");
	 $short = fn ($n) => str_starts_with($n, "Alias") || str_starts_with($n, "alias");
	 var_dump(array_values(array_filter(get_declared_traits(), $short)),
	          array_values(array_filter(get_declared_classes(), $short)),
	          array_values(array_filter(get_declared_interfaces(), $short)));
	 var_dump(class_exists("AliasKA"), (new AliasKA) instanceof AliasK);`,
	// Closure::fromCallable makes the closure eagerly, so a callable that
	// resolves but could not be called refuses there rather than at the call --
	// and says so in its own words, with the reason lowercased onto the end.
	`class FromC { public function method() { echo "ran\n"; } public static function stat() { echo "stat\n"; } }
	 foreach ([["FromC", "method"], "FromC::method", ["FromC", "stat"], [new FromC, "method"]] as $c) {
	   try { $f = Closure::fromCallable($c); $f(); }
	   catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	// `yield` in a by-reference generator wants something with a slot behind
	// it. A call's result has none -- unless the callee itself returns by
	// reference, which hands back a box.
	`function yieldPlain() { return "bar"; }
	 function &yieldRef() { static $x = "ref"; return $x; }
	 function &yg1() { yield yieldPlain(); }
	 function &yg2() { $v = "v"; yield $v; }
	 function &yg3() { yield yieldRef(); }
	 function &yg4() { yield "lit"; }
	 foreach (["yg1", "yg2", "yg3", "yg4"] as $g) {
	   echo "$g: "; $it = $g(); var_dump($it->current());
	 }`,
	// print_r says what an object *is*: an enum is an Enum, and a backed one
	// says what it is backed by.
	`enum PrSuit: string { case H = "h"; }
	 enum PrNums: int { case One = 1; }
	 enum PrPure { case A; }
	 print_r(PrSuit::H); echo "\n";
	 print_r(PrNums::One); echo "\n";
	 print_r(PrPure::A); echo "\n";
	 print_r([PrSuit::H]); echo "\n";
	 print_r(new stdClass); echo "\n";`,
	// A compound assignment and an increment *read* before they write, so a
	// typed property holding nothing is reported before the write is judged --
	// while a plain assignment, which reads nothing, reports the visibility.
	`class AsymInit {
	   public private(set) int $p;
	   public readonly int $r;
	   public function __construct() { $this->r = 1; }
	 }
	 $o = new AsymInit;
	 $probe = [
	   'increment' => function ($o) { $o->p++; },
	   'compound'  => function ($o) { $o->p .= "x"; },
	   'write'     => function ($o) { $o->p = 5; },
	   'readonly'  => function ($o) { $o->r++; },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "$label: ";
	   try { $fn($o); echo "ok\n"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 }
	 // Descending into a property to unset an element is the one write-shaped
	 // operation that does *nothing* when the property holds nothing: there is
	 // no element to remove, so who may modify it never comes up.
	 class AsymDim {
	   public private(set) array $empty;
	   public private(set) array $full = [1, 2];
	   public private(set) int $scalar;
	   public array $open;
	 }
	 $d = new AsymDim;
	 $dims = [
	   'unset uninitialized dim' => function ($d) { unset($d->empty[0]); },
	   'unset initialized dim'   => function ($d) { unset($d->full[0]); },
	   'unset the property'      => function ($d) { unset($d->scalar); },
	   'append uninitialized'    => function ($d) { $d->empty[] = 1; },
	   'unset public dim'        => function ($d) { unset($d->open[0]); },
	 ];
	 foreach ($dims as $label => $fn) {
	   echo "$label: ";
	   try { $fn($d); echo "done\n"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 }
	 var_dump($d->full);`,
	// A class waiting for its own parent cannot be extended: an autoloader
	// reached from that wait finds it in the class table but not linked, and
	// the engine reports it as not found rather than autoloading it again.
	`spl_autoload_register(function ($class) {
	   echo "autoload($class)\n";
	   class UnlinkedX extends UnlinkedB {}
	 });
	 try { class UnlinkedB extends UnlinkedA {} }
	 catch (Error $e) { echo "caught: ", $e->getMessage(), "\n"; }
	 var_dump(class_exists("UnlinkedB", false), class_exists("UnlinkedX", false));`,
	// A `data:` URI carries its content in the path, so opening one needs no
	// wrapper and no file: it reads and seeks like any other stream.
	`$h = fopen("data:text/plain,foobar", "r");
	 var_dump($h !== false, get_resource_type($h), fread($h, 3), fread($h, 3), feof($h));
	 var_dump(fread($h, 1), feof($h));
	 rewind($h); var_dump(fread($h, 6));
	 $b = fopen("data:text/plain;base64,Zm9v", "r");
	 var_dump(fread($b, 3));
	 var_dump(file_get_contents("data:text/plain,hi"));
	 var_dump(fclose($h), fclose($b));`,
	// The private `storage` a dump shows belongs to the engine class that
	// declares it, not to whatever subclass the program wrote.
	`class MyArrayObject extends ArrayObject {}
	 class MyArrayIterator extends ArrayIterator {}
	 var_dump(new ArrayObject([1]), new ArrayIterator([1]),
	          new MyArrayObject([1]), new MyArrayIterator([1]));
	 print_r(new ArrayIterator([1])); echo "\n";`,
	// Two things that do *not* produce a lazy object's state: calling a method
	// on it, and reaching a virtual hooked property, which has no state behind
	// it at all. A property the initializer was told to skip does not either.
	`class LazyUse {
	   public $virtual { get { return 1; } }
	   public function __construct(private int $id, private string $name) {}
	   public function getId(): int { return $this->id; }
	   public function getName(): string { return $this->name; }
	 }
	 $r = new ReflectionClass(LazyUse::class);
	 $e = $r->newLazyGhost(function ($o) {
	   var_dump("initializer");
	   (new ReflectionProperty($o::class, "name"))->setValue($o, "John");
	 });
	 (new ReflectionProperty(LazyUse::class, "id"))->setRawValueWithoutLazyInitialization($e, 123);
	 echo "virtual:\n"; var_dump($e->virtual);
	 echo "skipped:\n"; var_dump($e->getId());
	 echo "deferred:\n"; var_dump($e->getName());`,
	// A static property's storage belongs to the class that declared it, and so
	// does its initializer: a subclass's flattened copy of an inherited static
	// carries no default, so building the storage from *that* would give null
	// to a property the parent gave a value -- and poison the parent's too,
	// since they share one slot.
	`class StatBase { public static $aa = 4; private static $bb = 5; protected static $cc = 6; }
	 class StatSub extends StatBase { public $own = 7; }
	 var_dump(get_class_vars("StatSub"), get_class_vars("StatBase"));
	 var_dump(StatSub::$aa, StatBase::$aa);
	 StatSub::$aa = 9;
	 var_dump(StatSub::$aa, StatBase::$aa);`,
	// Reflection answers with the class that *wrote* a member, not the one the
	// lookup went through -- and a ReflectionNamedType renders as the type as
	// written, `?` and all, while getName() answers the name alone.
	`class ReflBase {
	   public int $typed = 1;
	   const KIND = "base";
	   protected function m() { return "m"; }
	 }
	 class ReflSub extends ReflBase { public ?string $own = null; }
	 var_dump((new ReflectionProperty("ReflSub", "typed"))->getDeclaringClass()->getName(),
	          (new ReflectionClassConstant("ReflSub", "KIND"))->getDeclaringClass()->getName(),
	          (new ReflectionMethod("ReflSub", "m"))->getDeclaringClass()->getName(),
	          (new ReflectionProperty("ReflSub", "own"))->getDeclaringClass()->getName());
	 var_dump((string) (new ReflectionProperty("ReflSub", "typed"))->getType(),
	          (string) (new ReflectionProperty("ReflSub", "own"))->getType(),
	          (new ReflectionProperty("ReflSub", "own"))->getType()->getName());`,
	// Only a Throwable can be thrown, and the refusal comes before anything is
	// in flight.
	`class NotThrowable {}
	 class MyThrown extends Error {}
	 foreach ([fn () => throw new NotThrowable, fn () => throw new Exception("ok"),
	           fn () => throw new MyThrown("mine")] as $f) {
	   try { $f(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
	 }`,
	// A static method never has a $this, however the callable named it: an
	// object in a [obj, method] pair supplies only the class.
	`class StaticThis {
	   public static function s() {
	     try { var_dump($this); } catch (Throwable $e) { echo "s: ", $e->getMessage(), "\n"; }
	   }
	   public function i() { var_dump(isset($this)); }
	 }
	 $o = new StaticThis;
	 array_map([$o, "s"], [1]);
	 call_user_func([$o, "s"]);
	 $o->s();
	 StaticThis::s();
	 (Closure::fromCallable([$o, "s"]))();
	 call_user_func([$o, "i"]);`,
	// An unset chain reads its way down and removes nothing it does not find:
	// a link that is not an object ends it silently, and an *uninitialized*
	// declared property is one of those. A read-modify-write descent reports
	// the uninitialized property before asking who may modify it, the same
	// order the direct forms use.
	`class NestedInner { public int $prop = 1; public array $array = []; }
	 class NestedOuter {
	   public private(set) NestedInner $prop;
	   public readonly NestedInner $ro;
	   public NestedInner $open;
	 }
	 $o = new NestedOuter;
	 $probe = [
	   'read'       => function ($o) { echo $o->prop->prop; },
	   'write'      => function ($o) { $o->prop->prop = 0; },
	   'read-write' => function ($o) { $o->prop->prop += 1; },
	   'append'     => function ($o) { $o->prop->array[] = 1; },
	   'isset'      => function ($o) { echo (int) isset($o->prop->prop); },
	   'unset'      => function ($o) { unset($o->prop->prop); },
	   'unset dim'  => function ($o) { unset($o->prop->array[0]); },
	   'unset open' => function ($o) { unset($o->open->array[0]); },
	 ];
	 foreach ($probe as $label => $fn) {
	   echo "$label: ";
	   try { $fn($o); echo "done\n"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 }
	 $null = null;
	 unset($null->a->b); echo "null chain done\n";
	 unset($undefinedChain->a[0]); echo "undefined chain done\n";`,
	// A first-class callable of a plain function reflects as that function, and
	// a function's name splits into a namespace and a short name the way a
	// class's does. A closure's name carries no namespace however it is spelt.
	`function reflectMe() {}
	 $byName = new ReflectionFunction("reflectMe");
	 $byCallable = new ReflectionFunction(reflectMe(...));
	 foreach ([$byName, $byCallable] as $r) {
	   var_dump($r->getName(), $r->getShortName(), $r->getNamespaceName(), $r->inNamespace());
	 }
	 $closure = new ReflectionFunction(function () {});
	 var_dump($closure->getNamespaceName(), $closure->inNamespace(),
	          $closure->getShortName() === $closure->getName());`,
	// A #[\Deprecated] closure that wraps a method is reported as that method,
	// with no `Class->` in front of the name; one with no scope keeps the
	// compiled closure name a trace would show.
	`class DepScope {
	   #[\Deprecated("m")] public function m() { echo "ran m\n"; }
	   #[\Deprecated] public static function s() { echo "ran s\n"; }
	 }
	 $plain = #[\Deprecated("why")] function () { echo "ran\n"; };
	 ob_start(); $plain(); $out = ob_get_clean();
	 echo str_replace(__FILE__, "FILE", $out);
	 ((new DepScope)->m(...))();
	 (Closure::fromCallable([new DepScope, "m"]))();
	 (DepScope::s(...))();`,
	// A ReflectionAttribute carries the attribute name in a declared property,
	// so var_dump shows it -- and every reflector refuses a write to $name or
	// $class from any scope, which is ext/reflection's own write handler rather
	// than a readonly flag.
	`#[Attribute] class AttrName { public function __construct(public int $x = 1) {} }
	 class AttrHost { public $p = 1; #[AttrName(5)] public function m() {} }
	 $attrs = (new ReflectionMethod("AttrHost", "m"))->getAttributes();
	 var_dump($attrs, $attrs[0]->name, $attrs[0]->getName());
	 $subjects = [$attrs[0], new ReflectionFunction("strlen"),
	              new ReflectionMethod("AttrHost", "m"), new ReflectionClass("AttrHost"),
	              new ReflectionProperty("AttrHost", "p")];
	 foreach ($subjects as $subject) {
	   foreach (["name", "class"] as $field) {
	     try { $subject->$field = "x"; printf("%s::$%s written\n", $subject::class, $field); }
	     catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	   }
	 }`,
	// ReflectionMethod on a closure answers with the trampoline the engine
	// builds for `Closure::__invoke`: the wrapped function's signature and
	// attributes under Closure's own name, as an internal declaration. A
	// closure's attributes are evaluated in the scope it was declared in --
	// the rebound one after bindTo.
	`#[Attribute] class ScopeAttr { public function __construct(public string $seen) {} }
	 class ScopeOne { const FOO = "one"; }
	 class ScopeTwo { const FOO = "two"; }
	 class ScopeHost {
	   const FOO = "host";
	   public function make(): Closure {
	     return #[ScopeAttr(self::FOO)] function () {};
	   }
	   public function target() { return "ran target"; }
	 }
	 $made = (new ScopeHost)->make();
	 $reflected = new ReflectionFunction($made);
	 var_dump($reflected->getAttributes()[0]->newInstance()->seen);
	 $rebound = new ReflectionFunction(Closure::bind($made, null, ScopeTwo::class));
	 var_dump($rebound->getAttributes()[0]->newInstance()->seen);
	 $wrapper = (new ScopeHost)->target(...);
	 $method = new ReflectionMethod($wrapper, "__invoke");
	 var_dump($method->getName(), $method->name, $method->class,
	          $method->getDeclaringClass()->getName(), $method->isDeprecated(),
	          (string) $method);
	 class ScopeDep { #[\Deprecated("gone")] public function d(int $z = 3) {} }
	 $deprecated = new ReflectionMethod((new ScopeDep)->d(...), "__invoke");
	 var_dump($deprecated->isDeprecated(), $deprecated->getAttributes()[0]->name,
	          $deprecated->getParameters()[0]->getName(), (string) $deprecated);`,
	// A closure declared inside a class keeps that scope, but an attribute
	// written on it targets a *function*: the engine asks whether the
	// declaration is a closure, not whether it has a scope. A first-class
	// callable of a method is a fake closure and targets a method.
	`#[Attribute(Attribute::TARGET_FUNCTION)] class OnlyFunction { public $n = 1; }
	 class TargetHost {
	   public function held(): Closure { return #[OnlyFunction] function () {}; }
	   public function staticHeld(): Closure { return #[OnlyFunction] static fn() => true; }
	   #[OnlyFunction] public function member() {}
	 }
	 $host = new TargetHost;
	 foreach ([$host->held(), $host->staticHeld()] as $each) {
	   var_dump((new ReflectionFunction($each))->getAttributes(OnlyFunction::class)[0]
	     ->newInstance()->n);
	 }
	 try {
	   (new ReflectionMethod("TargetHost", "member"))->getAttributes()[0]->newInstance();
	 } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 try {
	   (new ReflectionFunction($host->member(...)))->getAttributes()[0]->newInstance();
	 } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }`,
	// A reflector keeps the *real* class name, NUL suffix and all: an
	// anonymous class's reflector compares equal to get_class(). Only a
	// diagnostic cuts the name at the NUL.
	`class NameHost { public static function make() { return new class {}; } }
	 $anon = NameHost::make();
	 $r = new ReflectionObject($anon);
	 var_dump($r->getName() === get_class($anon), $r->name === get_class($anon),
	          $r->getShortName() === get_class($anon),
	          strlen($r->getName()) === strlen(get_class($anon)));
	 $plain = new ReflectionClass("NameHost");
	 var_dump($plain->getName(), $plain->name, $plain->getShortName());`,
	// A property the object grew at run time is reflectable through *that
	// object* and nowhere else, and reflection says so: not default, dynamic,
	// and printed with a <dynamic> marker. A static property is reachable
	// through the class name, which the instance-property lookup must not see.
	`#[AllowDynamicProperties] class DynHost {
	   public $a = 1;
	   public int $t;
	   public static $s = 2;
	 }
	 $host = new DynHost;
	 $host->dyn = 5;
	 $dyn = new ReflectionProperty($host, "dyn");
	 var_dump($dyn->getName(), $dyn->class, $dyn->isDynamic(), $dyn->isDefault(),
	          $dyn->isPublic(), $dyn->getValue($host), (string) $dyn);
	 $dyn->setValue($host, 9);
	 var_dump($host->dyn, $dyn->getValue($host));
	 $declared = new ReflectionProperty($host, "a");
	 var_dump($declared->isDynamic(), $declared->isDefault(), (string) $declared);
	 $static = new ReflectionProperty("DynHost", "s");
	 var_dump($static->isStatic(), $static->isDefault(), $static->isInitialized());
	 foreach (["dyn", "nope"] as $name) {
	   try { new ReflectionProperty("DynHost", $name); }
	   catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 }
	 $typed = new ReflectionProperty($host, "t");
	 var_dump($typed->isInitialized($host), $dyn->isInitialized($host));
	 $host->t = 3;
	 var_dump($typed->isInitialized($host));
	 try { $typed->isInitialized(); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 try { $typed->isInitialized(new stdClass); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }`,
	// skipLazyInitialization and setRawValueWithoutLazyInitialization refuse a
	// dynamic property before they look at whether the object is lazy at all.
	`#[AllowDynamicProperties] class LazyDyn { public $a = 1; }
	 $subject = new LazyDyn;
	 $subject->dyn = 1;
	 $reflector = new ReflectionProperty($subject, "dyn");
	 try { $reflector->skipLazyInitialization(new LazyDyn); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 try { $reflector->setRawValueWithoutLazyInitialization(new LazyDyn, 1); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }`,
	// Writing the raw value is still a write, so readonly holds once the slot
	// has one. skipLazyInitialization writes nothing and is never refused.
	`class RawReadonly { public readonly int $a; public $b; }
	 $reflector = new ReflectionClass(RawReadonly::class);
	 $ghost = $reflector->newLazyGhost(function () { throw new Exception("initializer"); });
	 $reflector->getProperty("a")->setRawValueWithoutLazyInitialization($ghost, 1);
	 try { $reflector->getProperty("a")->setRawValueWithoutLazyInitialization($ghost, 2); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 var_dump(!$reflector->isUninitializedLazyObject($ghost), $ghost);
	 $twice = $reflector->newLazyGhost(function () { throw new Exception("initializer"); });
	 $reflector->getProperty("a")->skipLazyInitialization($twice);
	 $reflector->getProperty("a")->skipLazyInitialization($twice);
	 var_dump($twice);`,
	// resetAsLazyGhost unsets the declared properties so the initializer may
	// write them again -- except a readonly one that already holds a value on
	// an object whose class is final, which no subclass could reach. That one
	// survives, is not deferred, and refuses the reconstructing write.
	`class ResetRo {
	   public readonly int $a;
	   public readonly int $b;
	   public int $c;
	   public function __construct($value, bool $setB = false) {
	     try { $this->a = $value; }
	     catch (Error $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	     if ($setB) { $this->b = $value; }
	     $this->c = $value;
	   }
	 }
	 final class ResetRoFinal extends ResetRo {}
	 foreach ([new ResetRo(1), new ResetRoFinal(1)] as $subject) {
	   $reflector = new ReflectionClass($subject::class);
	   $reflector->resetAsLazyGhost($subject, function ($subject) {
	     $subject->__construct(2, setB: true);
	   });
	   $reflector->initializeLazyObject($subject);
	   var_dump($subject);
	 }
	 // Every property kept means nothing is deferred, and the initializer
	 // never runs at all.
	 final class ResetRoOnly {
	   public readonly int $a;
	   public function __construct($value) { $this->a = $value; }
	 }
	 $only = new ResetRoOnly(1);
	 $onlyReflector = new ReflectionClass(ResetRoOnly::class);
	 $onlyReflector->resetAsLazyGhost($only, function ($only) { echo "ran\n"; });
	 var_dump($onlyReflector->isUninitializedLazyObject($only), $only);`,
	// An engine variadic does not collect names: only the handful of
	// forwarders declared with Z_PARAM_VARIADIC_WITH_NAMED do. A user variadic
	// keeps every name it is given.
	`try { array_merge([1, 2], a: [3, 4]); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 try { array_diff_key([1, 2], [3, 4], a: [5, 6]); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 try { printf("%s", zzz: 1); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 function namedTarget($a, $b) { var_dump($a, $b); }
	 call_user_func("namedTarget", b: 2, a: 1);
	 try { forward_static_call("namedTarget", zzz: 1); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 $rest = function (...$every) { var_dump($every); };
	 $rest(x: 1);
	 $rest->__invoke(y: 2);
	 // The parameter count is checked before the names are, so a variadic with
	 // too few arguments complains about the count.
	 try { call_user_func_array("array_multisort", ["" => 1]); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }`,
	// class_alias raises both of its diagnostics through zend_error rather
	// than as a builtin's own, so neither carries a `class_alias():` prefix.
	// A single underscore is on its way out as an alias.
	`class AliasSource {}
	 ob_start();
	 var_dump(class_alias("AliasSource", "AliasOk"));
	 var_dump(@class_alias("NotThere", "AliasBad"));
	 class_alias("NotThere", "AliasBad2");
	 class_alias("AliasSource", "_");
	 class_alias("AliasSource", "__");
	 var_dump(class_exists("_"), class_exists("__"));
	 var_dump(class_alias("AliasSource", "AliasOk"));
	 echo str_replace(__FILE__, "FILE", ob_get_clean());`,
	// `[$closure, "__invoke"]` reaches the closure through Closure's own
	// trampoline, and a diagnostic raised against the call names that -- while
	// a backtrace still shows the closure's compiled name. Called directly,
	// both are the closure.
	`$byRef = function (&$arg) {};
	 ob_start();
	 call_user_func([$byRef, "__invoke"], null);
	 call_user_func($byRef, null);
	 echo str_replace(__FILE__, "FILE", ob_get_clean());`,
	// Asymmetric visibility is checked before the hooks run: a set hook is the
	// implementation of the write, not a way around `private(set)`.
	`class HookVisible {
	   public private(set) string $written { set => $value; get => "g"; }
	   public private(set) string $backed { get => $this->backed; }
	   public private(set) string $plain = "x";
	   public protected(set) string $shared { set => $value; get => "h"; }
	   public function inside() { $this->written = "ok"; $this->written .= "y"; return "in"; }
	 }
	 class HookVisibleSub extends HookVisible {
	   public function sub() { $this->shared = "z"; return "sub"; }
	 }
	 $subject = new HookVisible;
	 foreach (["written", "backed", "plain", "shared"] as $name) {
	   try { $subject->$name = "hello"; }
	   catch (Error $e) { echo $e->getMessage(), "\n"; }
	 }
	 try { $subject->written .= "x"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
	 echo $subject->inside(), "\n";
	 echo (new HookVisibleSub)->sub(), "\n";`,
	// `self` in a callable is resolved where the *callable* is parsed, which is
	// a `callable` parameter and nothing else. A bare `$f()` looks the name up
	// as a class and does not find one.
	`class ScopeParent { static function m() { echo "parent\n"; } }
	 class ScopeChild extends ScopeParent {
	   static function main() {
	     $forms = [["self", "m"], ["parent", "m"], ["static", "m"], "self::m"];
	     ob_start();
	     foreach ($forms as $i => $one) {
	       try { $one(); } catch (Throwable $e) { echo $i, " ", $e->getMessage(), "\n"; }
	     }
	     foreach ($forms as $i => $one) {
	       try { call_user_func($one); }
	       catch (Throwable $e) { echo "cuf", $i, " ", $e->getMessage(), "\n"; }
	     }
	     var_dump(is_callable($forms[0]));
	     echo str_replace(__FILE__, "FILE", ob_get_clean());
	   }
	   static function m() { echo "child\n"; }
	 }
	 ScopeChild::main();`,
	// A reflector built from a Closure heads its dump with `Closure [`,
	// whatever the declaration behind it was -- including a first-class
	// callable, which is answered for by the name it wraps.
	`function dumpTarget(int $a, string &$b, Foo ...$c) {}
	 class Foo { public function m(int $a) {} }
	 $dumps = [new ReflectionFunction(dumpTarget(...)), new ReflectionFunction("dumpTarget"),
	           new ReflectionFunction((new Foo)->m(...)), new ReflectionMethod("Foo", "m")];
	 foreach ($dumps as $each) {
	   echo str_replace(__FILE__, "FILE", (string) $each), "\n";
	 }`,
	// `$GLOBALS["x"]` compiles to a fetch by name, and the fetch *type*
	// decides whether a missing name is reported: `??` and `isset()` ask, and
	// `unset($GLOBALS["x"]["k"])` neither reports nor creates.
	`ob_start();
	 $GLOBALS["gx"] ??= "x";
	 var_dump($GLOBALS["gx"]);
	 var_dump(isset($GLOBALS["gzz"]), $GLOBALS["gyy"] ?? "fallback");
	 unset($GLOBALS["gfoo"]["bar"]);
	 var_dump(array_key_exists("gfoo", $GLOBALS));
	 $gq = ["a" => 1, "b" => 2];
	 unset($GLOBALS["gq"]["a"]);
	 var_dump($gq);
	 unset($GLOBALS["gq"]);
	 var_dump(isset($gq));
	 echo $GLOBALS["gmissing"];
	 echo str_replace(__FILE__, "FILE", ob_get_clean());`,
	// A `static function () {}` written inside a method has no $this even
	// though one is in scope, so unbinding it is a no-op rather than the
	// warning a non-static closure using $this earns.
	`class UnbindHost {
	   public function method() {
	     $uses = function () { var_dump($this); };
	     $uses->bindTo(null);
	     $staticUses = static function () { var_dump($this); };
	     $staticUses->bindTo(null);
	     $ignores = function () { return 1; };
	     $ignores->bindTo(null);
	   }
	   public static function staticMethod() {
	     $uses = function () { var_dump($this); };
	     $uses->bindTo(null);
	   }
	 }
	 ob_start();
	 (new UnbindHost)->method();
	 UnbindHost::staticMethod();
	 echo str_replace(__FILE__, "FILE", ob_get_clean());`,
	// A closure's trace name carries what it is bound to: the scope class,
	// `Closure` when a $this survives but the scope was dropped, and nothing
	// at all when it has neither.
	`class TraceHost { public function make() { return function () { debug_print_backtrace(); }; } }
	 class TraceOther { private $x = 1; }
	 $made = (new TraceHost)->make();
	 ob_start();
	 $made();
	 ($made->bindTo(new TraceOther, null))();
	 ($made->bindTo(new TraceOther, "TraceOther"))();
	 (Closure::bind($made, null, "TraceOther"))();
	 (static function () { debug_print_backtrace(); })();
	 echo str_replace(__FILE__, "FILE", ob_get_clean());`,
	// The class a constructor refusal names is the one that *declared* the
	// constructor, which an inherited private one makes different from the
	// class being instantiated.
	`class CtorRoot { private function __construct() {} }
	 class CtorHeir extends CtorRoot { static function cheat() { return new CtorHeir; } }
	 class CtorProt { protected function __construct() {} }
	 class CtorProtHeir extends CtorProt {}
	 class CtorProtOwn extends CtorProt { protected function __construct() {} }
	 try { CtorHeir::cheat(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 try { new CtorRoot; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 try { new CtorProtHeir; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 try { new CtorProtOwn; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 try { (new ReflectionClass("CtorRoot"))->newInstance(); }
	 catch (Throwable $e) { echo $e->getMessage(), "\n"; }`,
	// A two-element array under the wrong keys is a *shaped* failure, not a
	// length one, and the callable check says which.
	`$wrongKeys = [1 => 0, 2 => 0];
	 try { $wrongKeys(); } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 try { call_user_func($wrongKeys); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 var_dump(is_callable($wrongKeys));
	 foreach ([[], [0 => "strlen"], [0 => "a", 1 => "b", 2 => "c"]] as $shape) {
	   try { $shape(); } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 }
	 $badMethod = [0 => new stdClass, 1 => 5];
	 try { call_user_func($badMethod); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }`,
	// The class in an undefined-method error is named as it was *declared*,
	// not as the callable spelled it.
	`class SpelledOut { }
	 foreach ([["stdclass", "b"], "stdclass::b", ["SPELLEDOUT", "b"]] as $each) {
	   try { $each(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 }
	 $onObject = [new stdClass, "b"];
	 try { $onObject(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }`,
	// `$v::class` takes an object and nothing else -- not even a class-name
	// string, which every other `$v::` form accepts.
	`class ClassOf { function m() { var_dump(self::class, static::class, $this::class); } }
	 foreach ([null, 0, 1.5, true, false, [], "ClassOf", new ClassOf] as $each) {
	   try { var_dump($each::class); }
	   catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 }
	 (new ClassOf)->m();`,
	// Appending to a string is refused in its own words, before the complaint
	// about the operator: `$s[] .= x` is about the `[]`.
	`foreach ([1, 2, 3, 4] as $which) {
	   $text = "abc";
	   try {
	     switch ($which) {
	       case 1: $text[] += $text; break;
	       case 2: $text[] = "x"; break;
	       case 3: $text[0] += 1; break;
	       case 4: $text[]++; break;
	     }
	   } catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 }`,
	// Closure::bindTo and Closure::bind share their parameters and differ only
	// in where they sit. `object|string|null` in weak mode takes an int as a
	// class name that does not exist; an array has no string form at all.
	`$subject = function () {};
	 ob_start();
	 foreach ([5, 1.5, true, "A", []] as $each) {
	   try { $subject->bindTo($each); }
	   catch (Throwable $e) { printf("this: %s\n", $e->getMessage()); }
	 }
	 foreach ([5, 1.5, true, []] as $each) {
	   try { $subject->bindTo(null, $each); }
	   catch (Throwable $e) { printf("scope: %s\n", $e->getMessage()); }
	 }
	 try { Closure::bind($subject, null, []); }
	 catch (Throwable $e) { printf("bind: %s\n", $e->getMessage()); }
	 foreach ([[], 5] as $each) {
	   try { $subject->call($each); }
	   catch (Throwable $e) { printf("call: %s\n", $e->getMessage()); }
	 }
	 try { $subject->call(); }
	 catch (Throwable $e) { printf("call: %s: %s\n", $e::class, $e->getMessage()); }
	 echo str_replace(__FILE__, "FILE", ob_get_clean());`,
	// zend.exception_ignore_args is read where the trace is *captured*, and it
	// omits the `args` key rather than emptying it -- which is how a dropped
	// argument list differs from a call that had none.
	`function ignoredArgs($a, $b) { throw new Exception("x"); }
	 function noArgs() { throw new Exception("y"); }
	 $show = function (Throwable $e) {
	   $frame = $e->getTrace()[0];
	   printf("%d %s %s\n", count($frame), $frame["function"],
	     array_key_exists("args", $frame) ? json_encode($frame["args"]) : "-");
	   echo str_replace(__FILE__, "FILE", $e->getTraceAsString()), "\n";
	 };
	 try { ignoredArgs("secret", 2); } catch (Throwable $e) { $show($e); }
	 try { noArgs(); } catch (Throwable $e) { $show($e); }
	 var_dump(ini_set("zend.exception_ignore_args", 1));
	 try { ignoredArgs("secret", 2); } catch (Throwable $e) { $show($e); }
	 try { noArgs(); } catch (Throwable $e) { $show($e); }
	 var_dump(ini_set("zend.exception_ignore_args", 0));
	 try { ignoredArgs("secret", 2); } catch (Throwable $e) { $show($e); }`,
	// A destructor run by the end-of-request sweep is called by the engine, so
	// a trace taken inside it shows a frame with no file and no line -- unlike
	// one whose object died at a point in the program. (An exception handler
	// is the same shape; see the note above about what this harness can ask.)
	`class ShutdownTrace {
	   public function __destruct() { echo str_replace(__FILE__, "FILE",
	     (new Exception("d"))->getTraceAsString()), "\n"; }
	 }
	 $keepAlive = new ShutdownTrace;`,
	// A variadic slot has no declared name for this diagnostic --
	// `func->common.num_args` does not count the variadic parameter -- and the
	// whole ` ($name)` clause is dropped rather than left empty. A method is
	// always spelled with `::` here, whether or not the call had an instance.
	`function byRefVariadic(&...$args) {}
	 function byRefPlain(&$x) {}
	 function byRefMixed($a, &...$rest) {}
	 class ByRefHost { function m(&...$a) {} static function s(&$q) {} }
	 $forms = [
	   fn() => byRefVariadic(1), fn() => byRefPlain(1), fn() => byRefMixed(1, 2),
	   fn() => (new ByRefHost)->m(1), fn() => ByRefHost::s(1),
	 ];
	 foreach ($forms as $each) {
	   try { $each(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 }`,
	// A constant-expression array evaluates its key before its value, even
	// though the AST holds the key second. Only visible when both fail.
	`function constKeyFirst($a = [KEY_MISSING => VALUE_MISSING]) {}
	 try { constKeyFirst(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 function constLaterKey($a = [1 => FIRST_MISSING, SECOND_MISSING => 2]) {}
	 try { constLaterKey(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 const CONST_OK = 5;
	 function constFine($a = [CONST_OK => CONST_OK, "x" => CONST_OK, CONST_OK]) {
	   var_dump($a);
	 }
	 constFine();`,
	// An internal function takes null for a non-nullable parameter only where
	// the parameter is a *scalar*: that allowance comes from the coercing ZPP
	// macros, so object, array and class types refuse it outright.
	`$nulls = [
	   "strlen" => fn() => strlen(null),
	   "abs" => fn() => abs(null),
	   "count" => fn() => count(null),
	   "get_class" => fn() => get_class(null),
	   "str_repeat" => fn() => str_repeat("a", null),
	   "spl_object_id" => fn() => spl_object_id(null),
	 ];
	 ob_start();
	 foreach ($nulls as $named => $each) {
	   try { $each(); echo $named, " ok\n"; }
	   catch (Throwable $e) { printf("%s -> %s: %s\n", $named, $e::class, $e->getMessage()); }
	 }
	 echo str_replace(__FILE__, "FILE", ob_get_clean());`,
	// Reaching a closure through Closure::__invoke puts an engine-built
	// trampoline between the script and the body, so an argument TypeError has
	// no call site to name and leaves the `, called in ...` clause off.
	`class WantedType {}
	 class GivenType {}
	 $typed = function (WantedType $a) {};
	 function typedTarget(WantedType $a) {}
	 $given = new GivenType;
	 $arrayForm = [$typed, "__invoke"];
	 $ways = [
	   fn() => $typed($given), fn() => $typed->__invoke($given),
	   fn() => call_user_func([$typed, "__invoke"], $given),
	   fn() => call_user_func($typed, $given), fn() => typedTarget($given),
	   fn() => $arrayForm($given),
	 ];
	 foreach ($ways as $i => $each) {
	   try { $each(); }
	   catch (Error $e) { echo $i, " ", str_replace(__FILE__, "FILE", $e->getMessage()), "\n"; }
	 }`,
	// A compound assignment checks the offset type before it uses it, the same
	// way a plain read does: an array has no key form, so there is nothing to
	// look up and nothing to report missing.
	`$container = [];
	 $arrayKey = [1];
	 try { $container[$arrayKey] += 1; }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 var_dump($container);
	 try { $container[new stdClass] .= "x"; }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 $fine = ["x" => 1];
	 $fine["x"] += 1;
	 var_dump($fine);`,
	// The compiler folds any constant expression that raises no diagnostic, so
	// the ones that reach the evaluator are exactly the ones that do -- and
	// the evaluator has to know every operator, not just the arithmetic ones.
	// The result is kept on the declaring class, so a subclass reading the
	// same constant does not evaluate it again.
	`class ConstOps {
	   const MOD = 1 % 1.5;
	   const DIV = 7 / 1.5;
	   const SHIFTED = 1 << 1.5;
	 }
	 class ConstHeir extends ConstOps {}
	 var_dump(ConstOps::MOD, ConstOps::DIV, ConstOps::SHIFTED);
	 var_dump(ConstHeir::MOD, ConstHeir::DIV, ConstHeir::SHIFTED);`,
	// A __toString that throws while a value is being coerced wins over the
	// type error everywhere except a *return*, where the TypeError still comes
	// out with the exception hanging off it as the cause.
	`class ThrowingString { public function __toString() { throw new Exception("from toString"); } }
	 class TypedTarget { public string $prop; public static string $shared; }
	 function typedReturn(): string { return new ThrowingString; }
	 function typedArgument(string $x) {}
	 try { typedReturn(); }
	 catch (Throwable $e) {
	   printf("ret %s: %s / previous %s\n", $e::class, $e->getMessage(),
	     $e->getPrevious()?->getMessage() ?? "none");
	 }
	 try { typedArgument(new ThrowingString); }
	 catch (Throwable $e) { printf("arg %s: %s\n", $e::class, $e->getMessage()); }
	 $target = new TypedTarget;
	 try { $target->prop = new ThrowingString; }
	 catch (Throwable $e) { printf("prop %s: %s\n", $e::class, $e->getMessage()); }
	 var_dump($target);
	 try { TypedTarget::$shared = new ThrowingString; }
	 catch (Throwable $e) { printf("static %s: %s\n", $e::class, $e->getMessage()); }
	 try { strlen(new ThrowingString); }
	 catch (Throwable $e) { printf("builtin %s: %s\n", $e::class, $e->getMessage()); }
	 // The ordinary coercion still runs at every one of those sites.
	 class PlainString { public function __toString() { return "ok"; } }
	 function plainReturn(): string { return new PlainString; }
	 var_dump(plainReturn());
	 $target->prop = new PlainString;
	 TypedTarget::$shared = new PlainString;
	 var_dump($target->prop, TypedTarget::$shared, strlen(new PlainString));`,
	// settype($v, "string") is the same conversion the language uses
	// everywhere else: an array warns, and an object goes through __toString.
	`class SetTypeString { function __toString() { return "10"; } }
	 ob_start();
	 $subjects = [new SetTypeString, [1, 2], true, null, 9876545, 0.10, "kept"];
	 foreach ($subjects as $each) {
	   settype($each, "string");
	   var_dump($each);
	 }
	 $refused = new stdClass;
	 try { settype($refused, "string"); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 echo str_replace(__FILE__, "FILE", ob_get_clean());`,
	// A failed include raises two diagnostics in a fixed order: the stream
	// failure and then the include failure -- a warning for the optional forms
	// and an Error for the mandatory ones. Only the *bodies* are compared:
	// this harness rewrites the function-name prefix of an engine diagnostic
	// to "PHP Request Startup", so the keyword each one names is checked by
	// offset_string's neighbours in the corpus instead.
	`$body = function (string $text) {
	   $at = strpos($text, "Failed");
	   echo $at === false ? $text : substr($text, $at), "\n";
	 };
	 set_error_handler(function ($no, $str) use ($body) { $body($str); return true; });
	 include "/nope/a.php";
	 include_once "/nope/c.php";
	 var_dump(@include "/nope/d.php");
	 try { require "/nope/e.php"; }
	 catch (Throwable $e) { printf("%s: ", $e::class); $body($e->getMessage()); }`,
	// Reflection describes static properties too, which the instance lookup
	// deliberately hides. A static is written as `setValue(null, $value)`; the
	// one-argument spelling still works and is deprecated, and that
	// deprecation is about the argument count rather than about statics.
	`class StaticHost {
	   public static $shared = "stat";
	   public $each = "inst";
	   private static $hidden = "priv";
	 }
	 $reflector = new ReflectionClass("StaticHost");
	 $shared = $reflector->getProperty("shared");
	 var_dump($shared->getName(), $shared->isStatic(), $shared->isDefault(),
	          $shared->getValue());
	 var_dump($reflector->getProperty("hidden")->getValue());
	 var_dump(count($reflector->getProperties()));
	 ob_start();
	 $shared->setValue("one-argument");
	 echo str_replace(__FILE__, "FILE", ob_get_clean());
	 var_dump(StaticHost::$shared);
	 $shared->setValue(null, "two-argument");
	 var_dump(StaticHost::$shared);
	 $each = $reflector->getProperty("each");
	 var_dump($each->isStatic(), $each->getValue(new StaticHost));`,
	// A property is lazy while the object is still waiting for its
	// initializer *and* this property is one of the things being waited for.
	// A static belongs to the class, a virtual one has no storage, and a
	// dynamic one was written after the fact -- none is ever deferred.
	`#[AllowDynamicProperties] class LazyShapes {
	   public static $staticProp;
	   public int $typed;
	   public $untyped;
	   public $virtual { get {} }
	 }
	 $report = function (ReflectionClass $reflector, object $subject) {
	   foreach (["staticProp", "typed", "untyped", "virtual", "dynamic"] as $name) {
	     if ($name === "dynamic") {
	       $grown = new LazyShapes;
	       $grown->dynamic = 1;
	       $each = new ReflectionProperty($grown, $name);
	     } else {
	       $each = $reflector->getProperty($name);
	     }
	     printf("%s: %d\n", $name, $each->isLazy($subject));
	   }
	 };
	 $reflector = new ReflectionClass(LazyShapes::class);
	 echo "# ghost\n";
	 $ghost = $reflector->newLazyGhost(function () {});
	 $report($reflector, $ghost);
	 $reflector->getProperty("typed")->skipLazyInitialization($ghost);
	 printf("typed (skipped): %d\n", $reflector->getProperty("typed")->isLazy($ghost));
	 echo "# initialized ghost\n";
	 $reflector->initializeLazyObject($ghost);
	 $report($reflector, $ghost);
	 echo "# proxy\n";
	 $proxy = $reflector->newLazyProxy(fn() => new LazyShapes);
	 $report($reflector, $proxy);
	 echo "# initialized proxy\n";
	 $reflector->initializeLazyObject($proxy);
	 $report($reflector, $proxy);
	 echo "# not lazy at all\n";
	 $report($reflector, new LazyShapes);`,
	// A private method is per-class: the lookup prefers one the *calling
	// scope* declares over the one the object's class resolved to. It applies
	// to a private method a trait provided as much as to one written out.
	`trait PrivateTrait { private function t() { echo static::class, " -> ", __CLASS__, "\n"; } }
	 class PrivBase {
	   use PrivateTrait;
	   public function fromBase() { $this->t(); }
	 }
	 class PrivHeir extends PrivBase {
	   use PrivateTrait;
	   public function fromHeir() { $this->t(); }
	 }
	 class PrivPlain extends PrivBase {}
	 class PrivReaching extends PrivBase { public function reach() { $this->t(); } }
	 (new PrivBase)->fromBase();
	 (new PrivHeir)->fromBase();
	 (new PrivHeir)->fromHeir();
	 (new PrivPlain)->fromBase();
	 try { (new PrivReaching)->reach(); }
	 catch (Throwable $e) { echo $e->getMessage(), "\n"; }
	 class WrittenBase {
	   private function w() { echo "WrittenBase::w\n"; }
	   public function callIt() { $this->w(); }
	 }
	 class WrittenHeir extends WrittenBase {
	   private function w() { echo "WrittenHeir::w\n"; }
	   public function ownCall() { $this->w(); }
	 }
	 (new WrittenHeir)->callIt();
	 (new WrittenHeir)->ownCall();`,
	// func_get_arg checks the sign before it looks at how many arguments there
	// were, and the two bounds have different messages.
	`function argBounds($a, $b) {
	   foreach ([-1, 0, 1, 2, 5] as $at) {
	     try { var_dump(func_get_arg($at)); }
	     catch (Throwable $e) { printf("%d %s: %s\n", $at, $e::class, $e->getMessage()); }
	   }
	 }
	 argBounds(1, 2);
	 try { func_get_arg(0); }
	 catch (Throwable $e) { printf("top %s: %s\n", $e::class, $e->getMessage()); }`,
	// debug_backtrace and debug_print_backtrace both take (options, limit).
	// The limit counts frames from the innermost and zero means all of them;
	// DEBUG_BACKTRACE_IGNORE_ARGS drops the `args` key entirely and
	// DEBUG_BACKTRACE_PROVIDE_OBJECT, on by default, decides the `object` one.
	`function traceLeaf() {
	   var_dump(count(debug_backtrace()), count(debug_backtrace(0, 2)),
	            count(debug_backtrace(0, 0)));
	   $ignored = debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS);
	   $full = debug_backtrace();
	   var_dump(array_key_exists("args", $ignored[0]),
	            array_key_exists("args", $full[0]),
	            array_key_exists("object", $full[0]));
	   ob_start();
	   debug_print_backtrace(0, 1);
	   debug_print_backtrace(0, 2);
	   debug_print_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS);
	   echo str_replace(__FILE__, "FILE", ob_get_clean());
	 }
	 function traceMiddle($secret) { traceLeaf(); }
	 function traceOuter() { traceMiddle("secret"); }
	 traceOuter();`,
	// A class constant has visibility, and a protected one is measured against
	// the class that declared it -- except that `use T` makes a trait's
	// constant a member of the *using* class, so a private one is reachable
	// from there.
	`class ConstVisible {
	   protected const GUARDED = 1;
	   private const HIDDEN = 2;
	   public const OPEN = 3;
	 }
	 class ConstHeirVis extends ConstVisible {
	   public function reach() { return [static::GUARDED, self::GUARDED, parent::GUARDED]; }
	 }
	 class ConstSibling extends ConstVisible {
	   public function peek() { return static::GUARDED; }
	 }
	 class ConstOutsider { public function m() { return ConstVisible::GUARDED; } }
	 foreach (["GUARDED", "HIDDEN", "OPEN"] as $each) {
	   try { var_dump(constant("ConstVisible::" . $each)); }
	   catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 }
	 var_dump((new ConstHeirVis)->reach(), (new ConstSibling)->peek());
	 try { (new ConstOutsider)->m(); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 trait ConstTrait {
	   private const TRAIT_HIDDEN = "t";
	   public function fromTrait() { return self::TRAIT_HIDDEN; }
	 }
	 class ConstUser {
	   use ConstTrait;
	   public function fromClass() { return [self::TRAIT_HIDDEN, static::TRAIT_HIDDEN]; }
	 }
	 var_dump((new ConstUser)->fromTrait(), (new ConstUser)->fromClass());`,
	// A trace names the *declaration*, not the spelling the call site used --
	// for an engine function as much as for a user one.
	`function spelledOut() {
	   echo str_replace(__FILE__, "FILE", (new Exception)->getTraceAsString()), "\n";
	 }
	 class SpelledHost {
	   static function m() {
	     echo str_replace(__FILE__, "FILE", (new Exception)->getTraceAsString()), "\n";
	   }
	 }
	 call_user_func("SPELLEDOUT");
	 $indirect = "SpElLeDoUt"; $indirect();
	 call_user_func("spelledhost::M");
	 try { call_user_func("exploDe"); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }`,
	// `$this` is not a compiled variable, so a by-name access has to be
	// answered from the frame. Reading it works; *writing* it is refused, in a
	// class or not.
	`class ThisByName {
	   function m() {
	     $named = "this";
	     var_dump($$named === $this);
	     try { $$named = 0; }
	     catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	     try { $ref = &$$named; }
	     catch (Throwable $e) { printf("ref %s: %s\n", $e::class, $e->getMessage()); }
	   }
	 }
	 (new ThisByName)->m();
	 function thisOutside() {
	   $named = "this";
	   try { $$named = 1; }
	   catch (Throwable $e) { printf("outside %s: %s\n", $e::class, $e->getMessage()); }
	 }
	 thisOutside();`,
	// Only a class whose *root* ancestor is Exception or Error may carry
	// Throwable. An interface may extend it -- that is how a program declares
	// its own throwable contract -- and a class implementing such an interface
	// is refused just the same.
	`interface OwnThrowable extends Throwable {}
	 class FromException extends Exception implements Throwable {}
	 class DeepException extends FromException {}
	 var_dump((new DeepException) instanceof Throwable);`,
	// A readonly class may use a trait whose properties are themselves
	// readonly. One that is not is refused at link time, which is a compile
	// error and so cannot be a case here -- see readonly_classes/gh9285_error.
	`trait ReadonlyOk { public readonly int $kept; }
	 readonly class ReadonlyUser {
	   use ReadonlyOk;
	   public function __construct() { $this->kept = 5; }
	 }
	 $held = new ReadonlyUser;
	 var_dump($held->kept);
	 try { $held->kept = 6; }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }`,
	// htmlspecialchars reads its $flags and $double_encode. The single quote
	// is spelled `&#039;` for HTML 4.01 and `&apos;` for every other doctype,
	// and `$double_encode = false` leaves an *existing* entity alone -- which
	// for a named one means a name the doctype actually defines.
	`$subject = "a<b>&\"c'd &lt; &#65; &amp &notanentity; &#xZZ;";
	 foreach ([ENT_QUOTES, ENT_COMPAT, ENT_NOQUOTES, ENT_QUOTES | ENT_XML1,
	           ENT_QUOTES | ENT_XHTML] as $flags) {
	   var_dump(htmlspecialchars($subject, $flags));
	 }
	 var_dump(htmlspecialchars($subject, ENT_QUOTES, "UTF-8", false));
	 var_dump(htmlspecialchars("&lt;&#65;&amp;&x;&#;&nbsp;&#x41;&AMP;",
	   ENT_QUOTES, "UTF-8", false));
	 // A numeric reference has to be a code point, and a named one has to be
	 // a name the doctype defines -- case included.
	 foreach (["&#999999999999;", "&#1114111;", "&#1114112;", "&#0;",
	           "&#x10FFFF;", "&#x110000;", "&AMP;", "&apos;", "&amp2;",
	           "&a1b2;", "&copy;", "&euro;", "&#38;", "&lt", "&"] as $each) {
	   printf("%-18s %s\n", $each, htmlspecialchars($each, ENT_QUOTES, "UTF-8", false));
	 }`,
	// What a ReflectionProperty says about a hooked one. A *virtual* property
	// has no storage and so no default; a hooked property has none even when
	// the source wrote one; an untyped property defaults to null whether or
	// not it was written, and a typed one with no initializer has no default
	// at all.
	`class HookShapes {
	   public $virtual { get { return 1; } }
	   public $backed { get { return $this->backed; } set { $this->backed = $value; } }
	   public $writeOnly { set { $this->writeOnly = $value; } }
	   public $withDefault = 1;
	   public $untyped;
	   public int $typed;
	   public static $shared = 2;
	 }
	 $reflector = new ReflectionClass("HookShapes");
	 foreach (["virtual", "backed", "writeOnly", "withDefault", "untyped",
	           "typed", "shared"] as $name) {
	   $each = $reflector->getProperty($name);
	   printf("%-12s virtual=%d hooks=%d get=%d set=%d hasDefault=%d default=%s\n",
	     $name, $each->isVirtual(), $each->hasHooks(),
	     $each->hasHook(PropertyHookType::Get), $each->hasHook(PropertyHookType::Set),
	     $each->hasDefaultValue(), var_export($each->getDefaultValue(), true));
	 }
	 $hook = $reflector->getProperty("virtual")->getHook(PropertyHookType::Get);
	 var_dump($hook::class, $hook->getName());
	 var_dump($reflector->getProperty("withDefault")->getHook(PropertyHookType::Get));
	 #[AllowDynamicProperties] class HookDynamic {}
	 $grown = new HookDynamic;
	 $grown->dyn = 1;
	 $dynamic = new ReflectionProperty($grown, "dyn");
	 var_dump($dynamic->isVirtual(), $dynamic->hasHooks(), $dynamic->hasDefaultValue());`,
	// Neither lazy-object writer asks whether the object is lazy.
	// reflection_property_check_lazy_compatible refuses a dynamic, static or
	// virtual property and nothing else: on an ordinary object the raw write
	// is an ordinary write and skipping does nothing at all. Skipping a
	// property that is *already* not lazy does nothing either -- it does not
	// write the default over what the program has since put there.
	`#[AllowDynamicProperties] class LazyWriters {
	   public string $foo = "X";
	   public string $dummy;
	   public $virtual { get { return 1; } }
	   public static $shared;
	 }
	 $reflector = new ReflectionClass(LazyWriters::class);
	 $plain = new LazyWriters;
	 $plain->dyn = 1;
	 $reflector->getProperty("foo")->setRawValueWithoutLazyInitialization($plain, "Y");
	 $reflector->getProperty("dummy")->skipLazyInitialization($plain);
	 var_dump($plain);
	 foreach (["virtual", "shared"] as $name) {
	   try { $reflector->getProperty($name)->skipLazyInitialization($plain); }
	   catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 }
	 try { (new ReflectionProperty($plain, "dyn"))->skipLazyInitialization($plain); }
	 catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 $ghost = $reflector->newLazyGhost(function () { throw new Exception("initializer"); });
	 $reflector->getProperty("foo")->skipLazyInitialization($ghost);
	 $ghost->foo = "written";
	 $reflector->getProperty("foo")->skipLazyInitialization($ghost);
	 var_dump($ghost->foo);
	 $raw = $reflector->newLazyGhost(function () { throw new Exception("initializer"); });
	 $reflector->getProperty("foo")->setRawValueWithoutLazyInitialization($raw, "first");
	 $reflector->getProperty("foo")->setRawValueWithoutLazyInitialization($raw, "second");
	 var_dump($raw->foo);`,
	// The callable check's own reasons. The *first* array member is checked
	// before the second, and anything that is not a string, an array or an
	// object is "no array or string given" -- the engine says what it wanted
	// rather than what it got. Closure::fromCallable words the same reasons
	// its own way.
	`$report = function (callable $each) {
	   try { $each(); echo "ok\n"; }
	   catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 };
	 class ReasonHost { function m() {} }
	 foreach ([1, 1.5, true, null, [1, 2], [1.5, "m"], [null, "m"], [1, 2, 3],
	           [new stdClass, "nope"], ["NoSuchClass", "m"], "nope"] as $bad) {
	   $report(fn() => call_user_func($bad));
	   $report(fn() => Closure::fromCallable($bad));
	 }
	 $report(fn() => Closure::fromCallable(["ReasonHost", "m"]));
	 $report(fn() => Closure::fromCallable("strlen"));
	 $report(fn() => Closure::fromCallable());
	 $report(fn() => Closure::bind());
	 $report(fn() => Closure::bind(function () {}));
	 $report(fn() => Closure::bind(1, null));
	 $report(fn() => explode("", "a"));`,
	// A Throwable's constructor is a C function with a real signature:
	// `string $message`, `int $code`, `?Throwable $previous`. ErrorException
	// has a different one -- (message, code, severity, filename, line,
	// previous) -- so $previous is argument #6 there, and severity defaults to
	// E_ERROR rather than to zero.
	`$report = function (callable $each) {
	   try { $each(); echo "ok\n"; }
	   catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 };
	 class CtorEx extends Exception {}
	 ob_start();
	 $report(fn() => new CtorEx("m", "notanint"));
	 $report(fn() => new CtorEx("m", 1.5));
	 $report(fn() => new CtorEx("m", "42"));
	 $report(fn() => new CtorEx("m", 0, "notthrowable"));
	 $report(fn() => new CtorEx("m", 0, null));
	 $report(fn() => new Exception(new stdClass));
	 $report(fn() => new ErrorException("m", "notanint"));
	 echo str_replace(__FILE__, "FILE", ob_get_clean());
	 var_dump((new CtorEx("m", "42"))->getCode());
	 $previous = new Exception("p");
	 $wrapped = new ErrorException("m", 1, 2, "f.php", 3, $previous);
	 var_dump($wrapped->getCode(), $wrapped->getSeverity(), $wrapped->getFile(),
	          $wrapped->getLine(), $wrapped->getPrevious()?->getMessage());
	 var_dump((new ErrorException("x"))->getSeverity());`,
	// A named argument reaching a by-reference parameter passes a slot, the
	// same as a positional one -- including a name no parameter claims, which
	// a by-reference variadic collects. The order the names are written in
	// does not matter.
	`function namedByRef(&$a = null, $b = null, &$c = null) { $a++; $c++; }
	 function namedVariadicRef(&...$rest) { foreach ($rest as &$each) { $each++; } }
	 foreach ([["a", "b", "c"], ["c", "b", "a"], ["c", "a"], ["a", "c"]] as $order) {
	   $x = $y = $z = 0;
	   $by = ["a" => &$x, "b" => &$y, "c" => &$z];
	   $call = [];
	   foreach ($order as $name) { $call[$name] = &$by[$name]; }
	   namedByRef(...$call);
	   printf("%s: %d %d %d\n", implode(",", $order), $x, $y, $z);
	   unset($by, $call);
	 }
	 $p = $q = 0;
	 namedVariadicRef(first: $p, second: $q);
	 var_dump($p, $q);`,
	// A WeakMap has no key to invent for an append, and refuses the descent in
	// those words rather than asking offsetGet(null) -- which would complain
	// about the key instead.
	`$map = new WeakMap;
	 $report = function (callable $each) {
	   try { $each(); echo "ok\n"; }
	   catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 };
	 $report(function () use ($map) { $map[] = 1; });
	 $report(function () use ($map) { $map[][1] = 1; });
	 $report(function () use ($map) { $map[1] = 1; });
	 // The "not contained in WeakMap" message names an object *handle*, which
	 // this harness cannot compare -- the engine's counter runs across every
	 // case. weakmap_error_conditions covers it.`,
	// The same named-by-reference rule reached through an array element:
	// ZEND_CHECK_FUNC_ARG names the parameter and the FETCH that follows binds
	// the slot, so the name has to be resolved to a position there too.
	`function elementByRef(&$a = null, $b = null, &$c = null) { $a++; $c++; }
	 $held = [0 => 0, 1 => 0, 2 => 0];
	 elementByRef(c: $held[2], a: $held[0], b: $held[1]);
	 var_dump($held);`,
	// forward_static_call_array splits its array the way call_user_func_array
	// does: a string key names a parameter, and a positional element after one
	// is refused.
	`function forwardTarget(...$rest) { var_dump($rest); }
	 function forwardPair($a, $b) { var_dump($a, $b); }
	 $report = function (callable $each) {
	   try { $each(); }
	   catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	 };
	 $report(fn() => forward_static_call_array("forwardTarget", ["a" => "b", 1]));
	 $report(fn() => forward_static_call_array("forwardTarget", [1, "a" => "b"]));
	 $report(fn() => forward_static_call_array("forwardTarget", [1, 2]));
	 $report(fn() => forward_static_call_array("forwardPair", ["b" => 2, "a" => 1]));`,
	// A numeric string keeps the type it parses to when the union offers it:
	// `"42.0"` against int|float is a float and `"42"` is an int, where the
	// fixed weak-mode order would make both ints. Only a string source has
	// this rule.
	`function unionBoth(int|float $v) { var_dump($v); }
	 function unionInt(int|string $v) { var_dump($v); }
	 function unionFloat(float|string $v) { var_dump($v); }
	 function plainInt(int $v) { var_dump($v); }
	 function plainFloat(float $v) { var_dump($v); }
	 ob_start();
	 foreach (["42.0", "42", "42.5", "1e3", " 42 ", "0x1A", 42.0, 42, true, false]
	          as $each) {
	   foreach (["unionBoth", "unionInt", "unionFloat", "plainInt", "plainFloat"]
	            as $target) {
	     try { $target($each); }
	     catch (Throwable $e) { printf("%s: %s\n", $e::class, $e->getMessage()); }
	   }
	 }
	 echo str_replace(__FILE__, "FILE", ob_get_clean());`,
	// Binding a caught exception is an assignment: a variable that is a
	// reference to a typed property is still held to that type, and the
	// TypeError replaces the exception being caught rather than joining it.
	`class CatchTyped { public int $i = 42; public string $s = "str"; }
	 $subject = new CatchTyped;
	 $bound =& $subject->i;
	 try {
	   try { throw new Exception("ex"); }
	   catch (Exception $bound) { echo "unreachable\n"; }
	 } catch (TypeError $e) {
	   var_dump($subject->i);
	   echo $e->getMessage(), "\n";
	 }
	 $bound =& $subject->s;
	 try {
	   try { throw new Exception("ex"); }
	   catch (Exception $bound) { echo "unreachable\n"; }
	 } catch (TypeError $e) {
	   var_dump($subject->s);
	   echo $e->getMessage(), "\n";
	 }`,
	// `yield from` has two refusals and they are different errors. A generator
	// whose frame is gone -- one that returned normally as much as one an
	// exception escaped -- cannot be delegated to at all; one that is being
	// *resumed* cannot either, because draining it would re-enter a frame that
	// is already executing. The ClosedGeneratorException is a third thing: it
	// is for a generator that aborted *during* the delegation.
	`function yieldsThenReturns() { yield 1; return 5; }
	 function yieldsThenThrows() { yield 0; throw new Exception("x"); }
	 function delegate($to) { $r = yield from $to; var_dump($r); }
	 $finished = yieldsThenReturns();
	 foreach ($finished as $each) {}
	 try { delegate($finished)->current(); }
	 catch (Throwable $e) { printf("finished %s: %s\n", $e::class, $e->getMessage()); }
	 $failing = yieldsThenThrows();
	 try { delegate($failing)->next(); }
	 catch (Throwable $e) { printf("first %s: %s\n", $e::class, $e->getMessage()); }
	 try { delegate($failing)->current(); }
	 catch (Throwable $e) { printf("second %s: %s\n", $e::class, $e->getMessage()); }
	 function yieldsIntoItself() { yield from yield; }
	 try { ($self = yieldsIntoItself())->send($self); }
	 catch (Throwable $e) { printf("self %s: %s\n", $e::class, $e->getMessage()); }
	 function plainSource() { yield 1; yield 2; }
	 foreach (delegate(plainSource()) as $each) { var_dump($each); }`,

	// `__clone` is a frame of its own, so an exception raised in it is
	// reported at the `clone` site with the method above it.
	`class NoClone {
	   public function __clone() { throw new Exception("no"); }
	 }
	 function makeCopy($subject) { return clone $subject; }
	 try { makeCopy(new NoClone()); }
	 catch (Exception $e) { echo $e->getTraceAsString(), "\n"; }
	 class Parented { public function __clone() { debug_print_backtrace(); } }
	 class Inheriting extends Parented {}
	 function copyChild() { clone new Inheriting(); }
	 copyChild();`,

	// The private method of the *calling* scope wins over a subclass
	// override however the call is spelled -- including through a callable.
	`class PrivateBase {
	   private function pick() { return "base private"; }
	   public function all() {
	     $name = "pick";
	     return [$this->pick(), $this->$name(),
	             call_user_func([$this, $name]),
	             call_user_func([$this, "pick"]),
	             array_map([$this, $name], [0])[0]];
	   }
	 }
	 class PrivateChild extends PrivateBase {
	   protected function pick() { return "child override"; }
	 }
	 print_r((new PrivateBase)->all());
	 print_r((new PrivateChild)->all());
	 class ProtectedBase {
	   protected function pick() { return "base protected"; }
	   public function all() { return call_user_func([$this, "pick"]); }
	 }
	 class ProtectedChild extends ProtectedBase {
	   protected function pick() { return "child override"; }
	 }
	 var_dump((new ProtectedChild)->all());`,

	// Losing a delegating generator unwinds the chain innermost-first: the
	// engine releases the root of the `yield from` list before the outer
	// generator unwinds itself.
	`function innerSource() {
	   try { yield 1; yield 2; return true; } finally { echo "inner finally\n"; }
	 }
	 function middleSource() {
	   try { var_dump(yield from innerSource()); } finally { echo "middle finally\n"; }
	 }
	 function outerSource() {
	   try { yield from middleSource(); } finally { echo "outer finally\n"; }
	 }
	 $chain = outerSource();
	 var_dump($chain->current());
	 unset($chain);
	 echo "dropped\n";
	 function twoSources() {
	   try { yield from innerSource(); yield from innerSource(); }
	   finally { echo "two finally\n"; }
	 }
	 $second = twoSources();
	 var_dump($second->current());
	 $second->next(); $second->next();
	 var_dump($second->current());
	 unset($second);
	 echo "second dropped\n";`,

	// User stream filters are request-local factories with two independently
	// ordered chains.  A write callback receives a whole upstream bucket, may
	// mutate it in place, and is closed exactly once when its handle is removed;
	// a read filter then buffers the remainder of that transformed bucket across
	// smaller fread calls.
	`class TraceFilter extends php_user_filter {
	   public function onCreate(): bool {
	     echo "create:", $this->filtername, ":", $this->params, "\n";
	     return true;
	   }
	   public function filter($in, $out, &$consumed, bool $closing): int {
	     while ($bucket = stream_bucket_make_writeable($in)) {
	       $consumed += $bucket->datalen;
	       $bucket->data = $this->params . strtoupper($bucket->data);
	       stream_bucket_append($out, $bucket);
	     }
	     return PSFS_PASS_ON;
	   }
	   public function onClose(): void {
	     echo "close:", $this->filtername, "\n";
	   }
	 }
	 var_dump(stream_filter_register("trace.__FILTER_RUN__.*", TraceFilter::class));
	 var_dump(stream_filter_register("trace.__FILTER_RUN__.*", TraceFilter::class));
	 var_dump(array_slice(stream_get_filters(), -1));
	 $path = "/tmp/php2go-differential-filter-chain";
	 $stream = fopen($path, "w+");
	 $custom = stream_filter_append(
	   $stream, "trace.__FILTER_RUN__.up", STREAM_FILTER_WRITE, "A"
	 );
	 $rot13 = stream_filter_prepend(
	   $stream, "string.rot13", STREAM_FILTER_WRITE
	 );
	 var_dump(get_resource_type($custom));
	 var_dump(fwrite($stream, "abc"));
	 var_dump(stream_filter_remove($custom));
	 var_dump(fwrite($stream, "def"));
	 var_dump(stream_filter_remove($rot13));
	 rewind($stream);
	 $lower = stream_filter_append(
	   $stream, "string.tolower", STREAM_FILTER_READ
	 );
	 var_dump(fread($stream, 3), ftell($stream), fread($stream, 20), ftell($stream));
	 var_dump(rewind($stream), fread($stream, 3), ftell($stream));
	 var_dump(fseek($stream, 1, SEEK_CUR), ftell($stream), fread($stream, 3));
	 var_dump(stream_filter_remove($lower));
	 fclose($stream);
	 unlink($path);`,

	// FEED_ME consumes input without emitting it.  Removing the filter sends a
	// closing callback, and a bucket manufactured from $this->stream is flushed
	// through the remaining chain before the resource becomes invalid.
	`class HoldingFilter extends php_user_filter {
	   public $held = "";
	   public function filter($in, $out, &$consumed, bool $closing): int {
	     while ($bucket = stream_bucket_make_writeable($in)) {
	       $consumed += $bucket->datalen;
	       $this->held .= $bucket->data;
	     }
	     if (!$closing) {
	       return PSFS_FEED_ME;
	     }
	     $bucket = stream_bucket_new($this->stream, "<" . $this->held . ">");
	     stream_bucket_prepend($out, $bucket);
	     return PSFS_PASS_ON;
	   }
	 }
	 stream_filter_register("holding.__FILTER_RUN__", HoldingFilter::class);
	 $path = "/tmp/php2go-differential-filter-feed";
	 $stream = fopen($path, "w+");
	 $filter = stream_filter_append(
	   $stream, "holding.__FILTER_RUN__", STREAM_FILTER_WRITE
	 );
	 var_dump(fwrite($stream, "xy"), ftell($stream));
	 var_dump(stream_filter_remove($filter), ftell($stream));
	 rewind($stream);
	 var_dump(fread($stream, 20));
	 fclose($stream);
	 unlink($path);`,

	// The built-in conversion filters are streaming transforms, not close-time
	// conveniences.  Their partial groups, escape prefixes and chunk framing
	// survive across writes while complete output reaches the stream at once.
	`$path = "/tmp/php2go-differential-filter-convert";
	 foreach ([
	   "convert.base64-encode",
	   "convert.base64-decode",
	   "convert.quoted-printable-encode",
	   "convert.quoted-printable-decode",
	   "dechunk",
	 ] as $name) {
	   $stream = fopen($path, "w+");
	   $filter = stream_filter_append($stream, $name, STREAM_FILTER_WRITE);
	   $parts = match ($name) {
	     "convert.base64-decode" => ["YW", "Jj", "ZA=="],
	     "convert.quoted-printable-decode" => ["a=", "20b"],
	     "dechunk" => ["4\r\nWi", "ki\r\n0\r\n\r\n"],
	     default => ["ab", "c d"],
	   };
	   foreach ($parts as $part) {
	     var_dump(fwrite($stream, $part), ftell($stream));
	   }
	   var_dump(stream_filter_remove($filter), ftell($stream));
	   rewind($stream);
	   var_dump(fread($stream, 100));
	   fclose($stream);
	 }
	 $stream = fopen($path, "w+");
	 $filter = stream_filter_append(
	   $stream,
	   "convert.base64-encode",
	   STREAM_FILTER_WRITE,
	   ["line-length" => 5, "line-break-chars" => "|"]
	 );
	 fwrite($stream, "abcdefg");
	 stream_filter_remove($filter);
	 rewind($stream);
	 var_dump(fread($stream, 100));
	 fclose($stream);
	 $stream = fopen($path, "w+");
	 $filter = stream_filter_append(
	   $stream,
	   "convert.quoted-printable-encode",
	   STREAM_FILTER_WRITE,
	   ["line-length" => 4, "line-break-chars" => "|"]
	 );
	 fwrite($stream, "abc\ndefgh");
	 stream_filter_remove($filter);
	 rewind($stream);
	 var_dump(fread($stream, 100));
	 fclose($stream);
	 $stream = fopen($path, "w+");
	 fwrite($stream, "abcdefghij");
	 rewind($stream);
	 $filter = stream_filter_append(
	   $stream, "convert.base64-encode", STREAM_FILTER_READ
	 );
	 var_dump(fread($stream, 3), ftell($stream));
	 var_dump(fread($stream, 3), ftell($stream));
	 var_dump(fread($stream, 10), ftell($stream));
	 var_dump(stream_filter_remove($filter));
	 fclose($stream);
	 foreach ([
	   "convert.quoted-printable-decode" => "=Q",
	   "convert.base64-decode" => "Y=",
	 ] as $name => $invalid) {
	   $stream = fopen($path, "w+");
	   $filter = stream_filter_append($stream, $name, STREAM_FILTER_WRITE);
	   var_dump(fwrite($stream, $invalid));
	   var_dump(stream_filter_remove($filter));
	   fclose($stream);
	 }
	 unlink($path);`,

	// Locale mutation and every locale reader share one process-global libc
	// boundary. Restore the compound starting locale so both oracle arms begin
	// from the same state.
	`$old_locale = setlocale(LC_ALL, 0);
	 set_error_handler(static function (int $level, string $message): true {
	   echo "diagnostic:", $level, ":", $message, "\n";
	   return true;
	 });
	 $active = setlocale(LC_ALL, [
	   "php2go_LOCALE_DOES_NOT_EXIST", "de_DE.utf8", "C"
	 ]);
	 var_dump($active, setlocale(LC_ALL, "0"));
	 var_dump(localeconv());
	 foreach ([CODESET, DAY_1, MON_1, YESEXPR, CRNCYSTR] as $item) {
	   echo $item, ":", bin2hex(nl_langinfo($item)), "\n";
	 }
	 var_dump(strcoll("ä", "z") <=> 0);
	 var_dump(setlocale(999, "C"));
	 var_dump(setlocale(LC_ALL, [str_repeat("x", 255), $active]));
	 var_dump(setlocale(LC_COLLATE, "C\0ignored"));
	 var_dump(setlocale(LC_ALL, new class {
	   public function __toString(): string {
	     echo "locale-cast\n";
	     return "C";
	   }
	 }));
	 try {
	   setlocale(LC_ALL, new stdClass);
	 } catch (Throwable $error) {
	   echo get_class($error), ":", $error->getMessage(), "\n";
	 }
	 var_dump(setlocale(LC_ALL, [["C"]], "C"));
	 var_dump(setlocale(LC_ALL, $old_locale));
	 restore_error_handler();`,

	// chdir is a libc boundary: errno diagnostics, null-byte rejection, the
	// process cwd, and relative stream paths all have to move together.
	`$old = getcwd();
	 set_error_handler(static function (int $level, string $message): true {
	   echo "diagnostic:", $level, ":", $message, "\n";
	   return true;
	 });
	 foreach (["", "/tmp/php2go-chdir-does-not-exist", "/dev/null"] as $directory) {
	   var_dump(chdir($directory));
	 }
	 try {
	   chdir("/tmp\0tail");
	 } catch (Throwable $error) {
	   echo get_class($error), ":", $error->getMessage(), "\n";
	 }
	 var_dump(chdir(directory: "/tmp"), getcwd() === "/tmp");
	 $stream = fopen("php2go-chdir-relative", "w+");
	 fwrite($stream, "cwd");
	 rewind($stream);
	 var_dump(fread($stream, 8));
	 fclose($stream);
	 unlink("php2go-chdir-relative");
	 var_dump(chdir($old), getcwd() === $old);
	 restore_error_handler();`,

	// getimagesizefromstring shares php-src's complete linked image dispatch:
	// ordinary raster headers, both TIFF byte orders, raw and boxed JPEG 2000,
	// text XBM, RIFF WebP, SWF, and AVIF's associated item properties. This
	// build identifies compressed SWF but has no statically linked zlib image
	// handler, so that branch deliberately remains notice/false. JPEG alone
	// fills the optional APP-marker output array.
	`set_error_handler(static function (int $level, string $message): true {
	   echo "diagnostic:", $level, ":", bin2hex($message), "\n";
	   return true;
	 });
	 $images = [
	   "gif" => "4749463839610b000d0087",
	   "jpeg" => "ffd8ffe1000641424344ffc0000b080011000d03000000ffd9",
	   "png" => "89504e470d0a1a0a0000000d49484452000000110000001308",
	   "swf" => "46575309400000005000d2000fa00000000000000000000000000000000000000000000000000000000000000000",
	   "swc" => "4357530940000000789cca60609bc050cb400800020000ffff35b7017c",
	   "psd" => "38425053000000000000000000000000001d0000001b",
	   "bmp" => "424d00000000000000000000000028000000210000002300000001001800",
	   "tiff-ii" => "49492a0008000000020000010400010000002700000001010400010000002900000000000000",
	   "tiff-mm" => "4d4d002a00000008000201000004000000010000002b01010004000000010000002f00000000",
	   "jpc" => "ff4fff510029000000000013000000170000000000000000000000000000000000000000000000000003070101070101070101",
	   "jp2" => "0000000c6a5020200d0a870a0000003b6a703263ff4fff510029000000000013000000170000000000000000000000000000000000000000000000000003070101070101070101",
	   "iff" => "464f524d00000016494c424d424d48440000000a002d0031000000000800",
	   "wbmp" => "00003539",
	   "xbm" => "23646566696e652069636f6e5f77696474682035310a23646566696e652069636f6e5f6865696768742035330a",
	   "ico" => "000001000100373b0000010020000000000000000000",
	   "webp" => "5249464616000000574542505650385800000000000000003c0000400000",
	   "avif" => "0000001466747970617669660000000061766966000000716d657461000000000000000e7069746d0000000000010000000e69696e6600000000000000000049697072700000002c6970636f0000001469737065000000000000001f00000025000000107069786900000000030a0a0a0000001569706d6100000000000000010001020102",
	   "avif-tiled" => "00000014667479706176696600000000617669660000008e6d657461000000000000000e7069746d0000000000010000000e69696e660000000000000000001a69726566000000000000000e64696d670001000100020000004c697072700000002c6970636f0000001469737065000000000000001f00000025000000107069786900000000030a0a0a0000001869706d6100000000000000020001010100020102",
	 ];
	 foreach ($images as $name => $hex) {
	   $metadata = ["old" => 1];
	   echo $name, ":", var_export([getimagesizefromstring(hex2bin($hex), $metadata), $metadata], true), "\n";
	 }
	 foreach (["", "47", "89504e4778", "5249464673686f7274", "756e6b6e6f776e206279746573"] as $hex) {
	   $metadata = ["old" => 1];
	   echo "bad:", var_export([getimagesizefromstring(hex2bin($hex), $metadata), $metadata], true), "\n";
	 }
	 $metadata = ["old" => 1];
	 echo "uri:", var_export([getimagesize("data://application/octet-stream;base64,R0lGODlhCwANAIc=", $metadata), $metadata], true), "\n";
	 $metadata = ["old" => 1];
	 echo "missing:", var_export([getimagesize("php2go-getimagesize-does-not-exist", $metadata), $metadata], true), "\n";
	 $metadata = ["old" => 1];
	 try { getimagesize("bad\0path", $metadata); }
	 catch (Throwable $error) { echo get_class($error), ":", $error->getMessage(), ":", var_export($metadata, true), "\n"; }
	 restore_error_handler();`,

	// The source-rendering family shares lexical categories, not a PHP parser:
	// inline HTML, comments, value-bearing tokens, keywords/punctuation,
	// strings and whitespace. File forms use the same bytes, show_source is the
	// highlight_file alias, and the five colors remain live INI settings.
	`$sources = [
	   "empty" => "",
	   "html" => "<b>&\ttext</b>\n",
	   "basic" => "<?php\n// line\n/** doc */\nfunction f(int \$x = 12): string { return 'a&<b' . \"x{\$x}\"; }\n?>tail",
	   "short" => "<?= \$x ?? null ?>",
	   "comments" => "<?php \$a/**x*/=1;#y\n\$b=2;//z\r\n\$c=3;",
	   "heredoc" => "<?php\n\$x = <<<TXT\nhello \$name & <tag>\nTXT;\n\$y = <<<'NOW'\nraw \$name\nNOW;\n",
	   "invalid" => "<?php 'unterminated\nnext",
	   "attribute" => "<?php #[A(x: 1)] class C { public readonly string \$x; }",
	   "numbers" => "<?php \$x=1+2-1e-3+0xFF+0b10+0o7+1_000+2.5;",
	   "keyword-values" => "<?php yield from \$x; int string null true false;",
	 ];
	 foreach ($sources as $name => $source) {
	   echo $name, ":", bin2hex(highlight_string($source, true)), "\n";
	 }
	 $old = ini_set("highlight.keyword", "#123456");
	 echo "ini:", var_export($old, true), ":", bin2hex(highlight_string("<?php echo 1;", true)), "\n";
	 ini_set("highlight.keyword", $old);
	 ob_start(); $answer = highlight_string("<?php echo 1;", false); $written = ob_get_clean();
	 echo "write:", var_export($answer, true), ":", bin2hex($written), "\n";
	 $path = "/tmp/php2go-source-render-__FILTER_RUN__.php";
	 foreach (["basic", "comments", "heredoc"] as $name) {
	   file_put_contents($path, $sources[$name]);
	   echo "file-", $name, ":", bin2hex(highlight_file($path, true)), ":",
	        bin2hex(show_source($path, true)), ":", bin2hex(php_strip_whitespace($path)), "\n";
	 }
	 unlink($path);
	 set_error_handler(static function (int $level, string $message): true {
	   echo "diagnostic:", $level, ":", $message, "\n";
	   return true;
	 });
	 var_dump(highlight_file($path, true));
	 echo "strip-missing:", bin2hex(php_strip_whitespace($path)), "\n";
	 foreach (["highlight_file", "show_source", "php_strip_whitespace"] as $function) {
	   try { $function === "php_strip_whitespace" ? $function("bad\0path") : $function("bad\0path", true); }
	   catch (Throwable $error) { echo get_class($error), ":", $error->getMessage(), "\n"; }
	 }
	 restore_error_handler();`,
}

func TestDifferentialAgainstEngine(t *testing.T) {
	var ran, matched, skipped int
	runToken := strconv.FormatUint(differentialRun.Add(1), 10)
	for _, template := range differentialCases {
		body := strings.ReplaceAll(template, "__FILTER_RUN__", runToken)
		source := "<?php " + body

		// Both sides declare into the same engine, so each case starts clean --
		// otherwise a function declared by the oracle makes the harvest fail to
		// compile with "cannot redeclare".
		if err := engine.Reset(); err != nil {
			t.Fatal(err)
		}
		want, err := engine.Eval(body)
		if err != nil {
			t.Fatalf("engine could not evaluate %q: %v", body, err)
		}

		if err := engine.Reset(); err != nil {
			t.Fatal(err)
		}
		got, err := run(t, source)
		if err != nil {
			// An opcode with no handler is a known gap, not a wrong answer.
			// Counting these separately keeps the metric honest: coverage and
			// correctness are different things.
			var unsupported *interp.UnsupportedError
			if errorsAs(err, &unsupported) {
				skipped++
				t.Logf("not yet supported: %q needs %s", body, unsupported.Opcode)
				continue
			}
			t.Errorf("%q: %v", body, err)
			continue
		}
		ran++
		if normalizeHandles(got) == normalizeHandles(want) {
			matched++
			continue
		}
		if true {
			t.Errorf("%q\n  engine:      %q\n  interpreter: %q", body, want, got)
		}
	}
	// Report matches, not merely cases that ran: conflating the two would make
	// a mismatch look like a success.
	t.Logf("%d/%d cases matched the engine, %d awaiting opcode support",
		matched, len(differentialCases), skipped)
	if ran == 0 {
		t.Fatal("no case ran; the differential proves nothing")
	}
}
