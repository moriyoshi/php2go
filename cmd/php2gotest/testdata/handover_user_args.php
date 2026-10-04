<?php
// A fresh object passed straight to a user function dies among the
// callee's variables when it returns -- parameters first, then locals --
// and one a thrown exception's trace holds dies with that exception.
class D {
    function __construct(public string $n) { echo "+{$this->n} "; }
    function __destruct() { echo "-{$this->n} "; }
}
class P { public $v = 1; }

// Numbers come back in the engine's order, without destructors too.
function numbers($a) { var_dump(new P); }
numbers(new P);
var_dump(new P);

function two($a, $b) { $x = new D('x'); echo "body "; }
two(new D('a'), new D('b'));
echo "| after two\n";

function reads($a) { echo "reads {$a->n} "; return strlen($a->n); }
echo reads(new D('r')), " | after reads\n";

// A non-owned position next to an owned one: `$k` is returned.
function keeps($a, $k) { echo "keeps "; return $k; }
$kept = keeps(new D('o'), new D('k'));
echo "| after keeps\n";

// Nested: the inner call owns its argument, the outer one its own.
function inner($a) { echo "inner "; }
function outer($a) { inner(new D('i')); echo "outer "; }
outer(new D('u'));
echo "| after nested\n";

// Thrown: the trace holds the argument, and a catch without a variable
// frees the exception -- and the argument with it -- at the catch.
function fails($a) { echo "fails "; throw new Exception('x'); }
try { fails(new D('t')); } catch (Exception) { echo "caught "; }
echo "| after fails\n";
// The exception's number comes back after its trace's: the next object
// takes the exception's.
var_dump(new P);

// So a local of the thrower that is not in the trace dies first, as the
// exception leaves the frame.
function fails2($a) { $l = new D('l'); echo "fails2 "; throw new Exception('y'); }
try { fails2(new D('t2')); } catch (Exception) { echo "caught2 "; }
echo "| after fails2\n";

// A local handed to a call that threw is in the trace too.
function test(D $o, bool $throw) { if ($throw) { throw new Exception('Error'); } }
function wrapper(string $id, bool $throw) { $o = new D($id); $z = new D("z$id"); test($o, $throw); echo "wrapped "; }
wrapper('w1', false);
echo "| after w1\n";
try { wrapper('w2', true); } catch (Exception) { echo "caught w2 "; }
echo "| after w2\n";

// A catch below the owner that keeps the exception to itself is fine.
function below() { try { throw new Exception('deep'); } catch (Exception $e) { echo $e->getMessage(), " "; } }
function owner($a) { below(); echo "owner "; }
owner(new D('p'));
echo "| after owner\n";

// {main}'s own catch variable holds only exceptions that left every owner.
try { fails(new D('m')); } catch (Exception $e) { echo "main caught "; }
echo "| end\n";
