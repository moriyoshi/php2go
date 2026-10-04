<?php
// `?->` short-circuits, and the opcode's extended_value carries **two** facts
// about the chain, not one: the low two bits say what the short circuit yields
// -- null for an expression, false for isset, true for empty -- and bit 2 says
// whether the chain is read silently. Measured for all four forms.

echo "bare: "; var_dump($a?->foo);
echo "coalesce: "; var_dump($b?->foo ?? "d");
echo "isset: "; var_dump(isset($c?->foo));
echo "empty: "; var_dump(empty($d?->foo));

$e = null;
echo "null bare: "; var_dump($e?->foo);
echo "null isset: "; var_dump(isset($e?->foo));
echo "null call: "; var_dump($e?->m());

class F { public $p = 1; public function m() { return 2; } }
$f = new F;
echo "object: "; var_dump($f?->p, $f?->m());
echo "object isset: "; var_dump(isset($f?->p), empty($f?->p));

// Both messages name a **bool by value**: `on false`, never `on bool`.
foreach ([false, true, 1, 1.5, "s", [], null] as $v) {
    try { $v->bar(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
    $x = $v->p;
}
