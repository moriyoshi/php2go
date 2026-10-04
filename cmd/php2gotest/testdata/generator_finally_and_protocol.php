<?php
// A generator's `finally` runs when the generator is **destroyed**, and PHP
// resumes an abandoned one for that reason alone. The timing is ownership:
// measured, a generator held only by a `foreach` unwinds when the loop ends,
// one held in a variable when that variable goes, and one held to the end of
// the script at shutdown. Only the first is decidable without a refcount, and
// it is the common shape.

function completes() { try { yield 1; yield 2; } finally { echo "finally-complete\n"; } }
foreach (completes() as $v) { echo "v$v\n"; }
echo "after complete\n";

function abandoned() { try { yield 1; yield 2; } finally { echo "finally-abandoned\n"; } }
foreach (abandoned() as $v) { echo "a$v\n"; break; }
echo "after abandoned\n";

function throwsOut() { try { yield 1; } finally { echo "finally-thrown\n"; } }
$t = throwsOut();
$t->current();
try { $t->throw(new Exception("x")); } catch (Exception $e) { echo "caught out\n"; }
echo "after thrown\n";

// The unwind matches no `catch` clause -- that is what makes it an unwind
// rather than an exception -- so a `catch` inside the generator does not see it.
function guarded() {
    try { yield 1; yield 2; }
    catch (Throwable $e) { echo "NOT REACHED\n"; }
    finally { echo "finally-guarded\n"; }
}
foreach (guarded() as $v) { break; }
echo "after guarded\n";

// The protocol itself.
function protocol() { $x = yield 1; echo "got $x\n"; yield 2 => "b"; return "ret"; }
$p = protocol();
var_dump($p->current(), $p->key());
var_dump($p->send("sent"), $p->key(), $p->valid());
$p->next();
var_dump($p->valid(), $p->getReturn());

// `yield from` delegates keys and the delegate's return value.
function inner() { yield 1; yield 2; return "inner-ret"; }
function outer() { $r = yield from inner(); echo "delegated returned $r\n"; yield 3; }
foreach (outer() as $k => $v) { echo "$k=$v\n"; }
function fromArray() { yield from [10, 20]; yield 30; }
foreach (fromArray() as $k => $v) { echo "$k=$v\n"; }

// Keys: explicit, automatic, and how an explicit integer moves the counter.
function keyed() { yield 'k' => 'v'; yield 'auto'; yield 7 => 'seven'; yield 'after'; }
foreach (keyed() as $k => $v) { var_dump($k, $v); }

// Rewind is refused only once the generator is past its first yield.
function twice() { yield 1; yield 2; }
$r = twice();
$r->rewind();
var_dump($r->current());
$r->next();
try { $r->rewind(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
