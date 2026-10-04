<?php
// Reading a missing key from `$GLOBALS` has its **own** diagnostic wording:
// "Undefined global variable $x", not "Undefined variable $x".
//
// The wording follows the *syntax*, not the table. `$$n` at file scope reads
// the very same globals array and keeps the ordinary wording, so the two cannot
// be told apart by scope -- which is how they came to share one path here.
$n = "nope";
echo @$$n;                       // variable variable, file scope
echo $$n;                        // -> Undefined variable $nope
echo "--1\n";
echo $GLOBALS["nope"];           // -> Undefined global variable $nope
echo "--2\n";

function viaVarVar() { $m = "alsonope"; echo $$m; }
viaVarVar();                     // -> Undefined variable $alsonope
echo "--3\n";
function viaGlobals() { echo $GLOBALS["stillnope"]; }
viaGlobals();                    // -> Undefined global variable $stillnope
echo "--4\n";

// `??` suppresses it, and a defined global reads normally from both spellings.
function coalesced() { var_dump($GLOBALS["absent"] ?? "dflt"); }
coalesced();
$defined = 5;
function reads() { var_dump($GLOBALS["defined"]); $k = "defined"; var_dump($$k); }
reads();
echo "done\n";
