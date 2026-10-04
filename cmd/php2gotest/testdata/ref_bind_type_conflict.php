<?php
// Binding a typed property to a box another typed property already holds:
// the value must fit the new declaration as it stands. One it could only
// coerce is refused in the reference's words (coercing would change what the
// first property sees); one it cannot take at all is a plain `Cannot assign`;
// an exact fit binds, and both declarations then guard the box.

class T {
    public int|string $x = 1;
    public float $f = 1.5;
    public string $s = "a";
    public ?int $n = null;
    public int $y = 0;
    public static int $si = 0;
    public static string $ss = "5";
}

$t = new T;
foreach ([['f', 'x'], ['s', 'x'], ['x', 'y'], ['n', 's']] as [$to, $from]) {
    try {
        $t->$to = &$t->$from;
        echo "$to =& $from bound\n";
    } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
}
try { $t->x = "str"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($t->x, $t->y);

try { T::$si = &T::$ss; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(T::$si, T::$ss);
try { T::$si = &$t->s; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
$five = "5";
T::$si = &$five;
var_dump(T::$si, $five);
