<?php
// `Exception::__toString` is the **engine's**, not a method the program
// declares, so `hasMethod` said no and `echo $e` reported that the object could
// not be converted to a string -- which is what PHP says for a class that is
// not a Throwable, so the diagnostic was wrong twice over.
//
// This file also covers `try { throw ... } catch`, which did not work at all:
// the `throw` terminator returned instead of routing to the enclosing handler,
// so an exception thrown inside its own `try` left the function. A raise from a
// *called* function was routed correctly, which is why every test that caught
// exceptions caught ones the runtime raised.
class MyEx extends Exception {}
class Loud extends Exception { public function __toString(): string { return "LOUD:" . $this->getMessage(); } }
try { throw new Exception("boom"); } catch (Exception $e) { echo $e, "\n---\n"; }
try { throw new MyEx("mine"); } catch (Exception $e) { echo "$e\n---\n"; }
try { throw new Loud("l"); } catch (Exception $e) { echo $e, "\n---\n"; }
try { throw new Exception(); } catch (Exception $e) { echo $e, "\n---\n"; }
$o = new stdClass();
try { echo $o; } catch (Error $e) { echo "caught: ", $e->getMessage(), "\n"; }

// The handler is reached from inside its own try, from a called function, and
// with a finally in between.
function raises() { throw new RuntimeException("from f"); }
try { raises(); } catch (Exception $e) { echo "outer: ", $e->getMessage(), "\n"; }
try {
    try { throw new Exception("inner"); } finally { echo "finally ran\n"; }
} catch (Exception $e) { echo "after finally: ", $e->getMessage(), "\n"; }
