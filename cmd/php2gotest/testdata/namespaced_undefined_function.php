<?php
// An unqualified call inside a namespace compiles to the **namespaced** name
// with the global one as a fallback, and PHP resolves the namespaced one first.
// When neither exists, the error names the one the source wrote:
//
//   namespace Ns; nope();   ->   Call to undefined function Ns\nope()
//
// Substituting the fallback before raising named a function the source never
// mentioned -- and the substitution is right whenever the fallback *does*
// exist, which is why it was applied unconditionally.
namespace Ns;

// The fallback path still works: no Ns\strtoupper exists, so the global one runs.
var_dump(strtoupper("ab"));
var_dump(\strlen("abc"));

try { nope(); } catch (\Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { alsoNope(1, 2); } catch (\Error $e) { echo $e->getMessage(), "\n"; }

// A fully qualified miss names exactly what was written.
try { \globalNope(); } catch (\Error $e) { echo $e->getMessage(), "\n"; }

// A namespaced function that DOES exist wins over the global spelling.
function strtolower(string $s): string { return "ns:" . $s; }
var_dump(strtolower("AB"));
echo "done\n";
