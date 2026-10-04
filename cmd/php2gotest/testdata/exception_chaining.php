<?php
// When a `finally` throws while an exception is in flight, the finally's
// exception wins and the interrupted one becomes its **ancestor** -- attached
// at the end of the chain, not the head. Settled by asking the engine for every
// shape below rather than reasoning about one.
//
// The last two matter most: linking must not close a loop, and the loop can be
// closed from either direction. `throw $b` interrupting
// `new Exception("Bar2", 0, $b)` is the case where the *thrown* exception is
// already an ancestor of the in-flight one -- a guard that only looks the other
// way passes case 1 and hangs here.
function show($e) { $n = 0; $p = $e; while ($p) { echo $n, ":", get_class($p), "(", $p->getMessage(), ")\n"; $n = $n + 1; $p = $p->getPrevious(); } }
$a = new Exception("Foo");
try { try { throw new Exception("Bar", 0, $a); } finally { throw new Exception("Dummy"); } } catch (Exception $e) { echo "case1:\n"; show($e); }
$b = new Exception("Foo2");
try { try { throw new Exception("Bar2", 0, $b); } finally { throw $b; } } catch (Exception $e) { echo "case2:\n"; show($e); }
$c = new Exception("C");
try { try { throw new Exception("InFlight"); } finally { throw new Exception("Fin", 0, $c); } } catch (Exception $e) { echo "case3:\n"; show($e); }
try { try { echo "ok\n"; } finally { throw new Exception("Alone"); } } catch (Exception $e) { echo "case4:\n"; show($e); }
try { try { try { throw new Exception("A"); } finally { throw new Exception("B"); } } finally { throw new Exception("C2"); } } catch (Exception $e) { echo "case5:\n"; show($e); }
