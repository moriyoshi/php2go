<?php
// Two renderings that are only visible on an **uncaught** throwable.
//
// A userland argument TypeError's message already names the *call site*, so the
// position appended after it -- where the function was declared -- is joined
// with `and defined in` rather than the plain ` in` every other throwable takes.
// And an anonymous class reaches the diagnostic already joined as
// `Two@anonymous\0FILE:LINE$N::a`; the engine interpolates the class and the
// method separately, so it keeps the `::a` that cutting at the NUL would drop.

class Two { public function a(): object {} }
$three = new class extends Two {
    public function a(): object { return 12345; }
};

function plain(int $i) {}
function ret(): int { return "x"; }

echo "caught argument: ";
try { plain("x"); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
echo "caught return: ";
try { ret(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
echo "caught anon: ";
try { $three->a(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
echo "trace:\n";
try { $three->a(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n"; }

// Uncaught, so the whole block renders.
plain("x");
