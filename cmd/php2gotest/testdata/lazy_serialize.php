<?php
// `serialize` is one of the library functions that must run a lazy object's
// initializer before it looks. It calls `Ctx.Materialize`, whose hook the
// compiled back-end never installed -- so it serialized an empty shell.
class Point {
    public int $x = 0;
    public int $y = 0;
    public function __construct(int $x, int $y) { $this->x = $x; $this->y = $y; }
}

$r = new ReflectionClass(Point::class);

$ghost = $r->newLazyGhost(function (Point $p) {
    echo "ghost initializer\n";
    $p->__construct(3, 4);
});
echo serialize($ghost), "\n";

$proxy = $r->newLazyProxy(function (Point $p) {
    echo "proxy initializer\n";
    return new Point(5, 6);
});
echo serialize($proxy), "\n";

// A second serialize must not run the initializer again.
echo serialize($ghost), "\n";

// An already-initialized object and a plain one go through unchanged.
echo serialize(new Point(1, 2)), "\n";
echo serialize([1, "two", null, true]), "\n";

// json_encode and var_export share the same hook.
$j = $r->newLazyGhost(function (Point $p) { $p->__construct(7, 8); });
echo json_encode($j), "\n";
var_dump(unserialize(serialize($ghost)) == $ghost);
