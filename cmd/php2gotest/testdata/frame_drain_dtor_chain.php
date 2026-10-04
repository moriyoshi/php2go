<?php
// A local destroyed as its frame ends while an exception unwinds: what its
// destructor throws is thrown instead, with the in-flight exception appended
// to its previous chain, and the frame's other locals are still destroyed
// (each destructor throw chains onto the last). Echoing a local converts it
// to a string, which keeps it only when its class's __toString stores $this.

function chain($e) {
    $out = [];
    for (; $e; $e = $e->getPrevious()) {
        $out[] = get_class($e) . ":" . $e->getMessage();
    }
    return implode(" <- ", $out);
}

class D {
    public $n;
    function __construct($n) { $this->n = $n; }
    function __destruct() { echo "dtor {$this->n}\n"; throw new Exception("d{$this->n}"); }
}

class Quiet {
    public $n;
    function __construct($n) { $this->n = $n; }
    function __destruct() { echo "quiet {$this->n}\n"; }
}

function one() { $a = new D(1); throw new Error("body"); }
try { one(); } catch (Throwable $e) { echo chain($e), "\n"; }

function two() { $a = new D(2); $b = new D(3); throw new Error("body two"); }
try { two(); } catch (Throwable $e) { echo chain($e), "\n"; }

function mixed_() { $a = new Quiet(1); $b = new D(4); $c = new Quiet(2); throw new RuntimeException("mixed"); }
try { mixed_(); } catch (Throwable $e) { echo chain($e), "\n"; }

class P {
    function __destruct() { echo "dtor P\n"; throw new Exception("p", 0, new LogicException("inner")); }
}
function withPrev() { $a = new P; throw new Error("body three"); }
try { withPrev(); } catch (Throwable $e) { echo chain($e), "\n"; }

function returns() { $a = new D(5); return 1; }
try { returns(); } catch (Throwable $e) { echo chain($e), "\n"; }

function returnsTwo() { $a = new D(6); $b = new D(7); return 1; }
try { returnsTwo(); } catch (Throwable $e) { echo chain($e), "\n"; }

function caughtInside() {
    try { $a = new D(8); throw new Error("inner body"); } finally { echo "finally\n"; }
}
try { caughtInside(); } catch (Throwable $e) { echo chain($e), "\n"; }

// Echo of a local.
class Keeps {
    public static $kept = [];
    public $n;
    function __construct($n) { $this->n = $n; }
    function __toString(): string { self::$kept[] = $this; return "K{$this->n}"; }
    // Only the subclass reports, so the kept objects' shutdown order (an
    // open gap) does not show.
    function __destruct() { if ($this->n == 2) echo "dK{$this->n}\n"; }
}
class Prints {
    public $n;
    function __construct($n) { $this->n = $n; }
    function __toString(): string { return "T{$this->n}"; }
    function __destruct() { echo "dT{$this->n}\n"; }
}
class PrintsChild extends Prints {}
class KeepsChild extends Keeps {}
class NoString { function __destruct() { echo "dN\n"; } }

function echoKeeps() { $s = new Keeps(1); echo $s, "\n"; echo "keeps end\n"; }
function echoPrints() { $t = new Prints(1); echo $t, "\n"; echo "prints end\n"; }
function echoKeepsChild() { $s = new KeepsChild(2); echo $s, "\n"; echo "keeps child end\n"; }
function echoChild() { $t = new PrintsChild(2); echo $t, "\n"; echo "child end\n"; }
function echoNone() {
    $x = new NoString;
    try { echo $x; } catch (Error $e) { echo $e->getMessage(), "\n"; }
    echo "none end\n";
}
function echoNoneThrows() { $x = new NoString; echo "x"; echo $x; }

echoKeeps(); echo "after keeps\n";
echoPrints(); echo "after prints\n";
echoChild(); echo "after child\n";
echoKeepsChild(); echo "after keeps child\n";
echoNone(); echo "after none\n";
try { echoNoneThrows(); } catch (Error $e) { echo "\ncaught ", $e->getMessage(), "\n"; }

class A2 {
    public static $i = 0;
    function __destruct() {
        if (self::$i++ != 0) throw new Exception("second");
        $b = new A2;
        echo $b;
    }
}
function drive() { $x = new A2; }
try { drive(); } catch (Throwable $e) { echo chain($e), "\n"; }
// A destructor that a shutdown destructor calls still chains; only the
// shutdown-called destructor's own frame reports each throw separately
// (destructor_throws_at_shutdown_handler covers that side).
class CallsF {
    function __destruct() {
        try { one(); } catch (Throwable $e) { echo "in shutdown dtor: ", chain($e), "\n"; }
    }
}
$callsF = new CallsF;
echo "end\n";
