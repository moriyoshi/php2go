<?php
// Reaching a property of a non-object refuses with **three different verbs**,
// and the operation picks which -- the same shape `private(set)` has. `+=` is
// an assign and `++` is not, which is the distinction that costs something:
// both lower to the same read-step-write triple.
foreach ([null, false, true, 1, 1.5, "s", []] as $v) {
  echo gettype($v), " assign: ";
  try { $v->d = 1; echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
  echo gettype($v), " modify: ";
  try { $v->d[] = 1; echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
  echo gettype($v), " inc: ";
  try { $v->d++; echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
$u = null;
echo "undef assign: "; try { $u->d = 1; var_dump($u); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// Every operator, against null.
foreach (['+=', '.=', '++', '--', '[]', 'ref', 'read'] as $op) {
    $v = null;
    echo $op, ": ";
    try {
        switch ($op) {
            case '+=': $v->d += 1; break;
            case '.=': $v->d .= "x"; break;
            case '++': $v->d++; break;
            case '--': $v->d--; break;
            case '[]': $v->d[] = 1; break;
            case 'ref': $r = &$v->d; break;
            case 'read': var_dump($v->d); break;
        }
        echo "ok\n";
    } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}

// An object is untouched by any of it.
class O { public $d = 0; public $a = []; }
$o = new O;
$o->d = 1; $o->d += 1; $o->d++; $o->a[] = 2; $r = &$o->d; $r = 9;
var_dump($o->d, $o->a);
