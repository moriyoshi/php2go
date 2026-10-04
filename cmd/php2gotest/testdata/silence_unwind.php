<?php
// **An exception leaves every `@` it escapes from.** The engine restores the
// error-reporting level as the exception leaves the suppressed expression --
// whether it is caught in the same function or propagates out -- because the
// expression `@` was suppressing is over however it ended.
//
// Without that, the mask stays at the fatal-only 4437 for the rest of the
// request, and every later diagnostic is swallowed. The visible symptom is
// `var_dump(error_reporting())` answering 4437 after a caught throw; the
// invisible one is far worse, since warnings simply stop appearing.
error_reporting(E_ALL);
$all = error_reporting();
var_dump($all === error_reporting());

function boom() { throw new Exception("boom"); }

// Caught in the same function as the `@`.
try { @boom(); } catch (Exception $e) { }
var_dump(error_reporting() === $all);

// Propagating out of a function that opened the `@`.
function wrapper() { @boom(); }
try { wrapper(); } catch (Exception $e) { }
var_dump(error_reporting() === $all);

// An Error, not an Exception.
try { @(1 % 0); } catch (\DivisionByZeroError $e) { }
var_dump(error_reporting() === $all);

// Nested `@`, inner one throwing.
try { @foo(@boom()); } catch (Throwable $e) { }
var_dump(error_reporting() === $all);

// The ordinary path still restores, and still suppresses while open.
@$undefined;
var_dump(error_reporting() === $all);

// And suppression still works after all that unwinding.
$x = @$alsoUndefined;
var_dump($x === null, error_reporting() === $all);
echo "Done\n";
