<?php
// A generator's body runs with its own frame on the stack, positioned at the
// line that **drove** it. Where that driver is an explicit `Generator` method,
// the body is entered from inside the engine and is located at
// `[internal function]` rather than at the `->next()` line -- the same rule
// `PushCall` states for every other internal entry, reached by marking the
// driver's own frame.
//
// A `foreach` drives without one of those frames, so it keeps the loop's own
// location. Both spellings appear below.

function g2() { yield 1; throw new Exception("b2"); }
$it = g2();
echo $it->current(), "\n";
try { $it->next(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }

function g3() { yield 1; throw new Exception("b3"); }
try { foreach (g3() as $v) echo $v, "\n"; } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }

function g4() { yield 1; throw new Exception("b4"); }
$i4 = g4();
$i4->rewind();
try { $i4->send("x"); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }

// A generator refusal raised by the driver itself still names the driver.
function g5() { yield 1; }
$i5 = g5();
try { $i5->getReturn(); } catch (Throwable $e) { echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n--\n"; }

// And the ordinary path still answers.
function g6() { yield 1; yield 2; return 9; }
$i6 = g6();
foreach ($i6 as $v) echo $v, "\n";
var_dump($i6->getReturn());

// The body's frame carries the **call's** arguments, on every resume: the
// generator keeps them, because the frame is pushed fresh each time and names
// the original call each time.
function g7($o, $n) { yield 1; throw new Exception("b7"); }
try { foreach (g7(new stdClass, 5) as $v) echo $v, "\n"; } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }
$i8 = g7(new stdClass, 6);
$i8->current();
try { $i8->next(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }
