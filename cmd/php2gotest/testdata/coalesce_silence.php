<?php
// `??` asks whether a value is set, and asking must not warn.
//
// The test used to be fused into the terminator as `br.null`, which made the
// one part of the coalesce that is an operation look like a kind of branch. It
// is `is_set` now, and converting it turned up a separate bug: the IS access
// mode never reached `propGet`, so every `??` over a property warned where PHP
// says nothing at all.
// `??` takes the right side only when the left is unset or null, and never warns.
function f($a, $arr, $o) {
    var_dump($undef ?? "d", $a ?? "d", $arr["missing"] ?? "d", $arr["k"] ?? "d");
    var_dump($o->missing ?? "d", $o->p ?? "d", $undef["x"]["y"] ?? "d");
    var_dump(null ?? "d", 0 ?? "d", "" ?? "d", false ?? "d", [] ?? "d");
    $n = null;
    var_dump($n ?? "d", $n ?? null ?? "d");
    // The right side must not be evaluated when the left is set.
    var_dump($a ?? sideEffect());
}
function sideEffect() { echo "[evaluated]"; return "R"; }
class C { public $p = "P"; }
f("A", ["k" => "K"], new C());
