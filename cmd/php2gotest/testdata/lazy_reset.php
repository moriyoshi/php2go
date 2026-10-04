<?php
// Resetting an object back to lazy.
//
// A file of its own, with **no object temporaries before it**: php2go does not
// reuse the handle of a temporary the engine has already freed, so an object
// number printed after one differs for that reason rather than for anything
// this fixture is about. These cases dump whole objects, so the handles cannot
// be avoided.
// `resetAsLazyGhost` unsets the declared properties so the initializer may
// write them again. One exception, and it is the whole difference between the
// two halves below: a **readonly** property already holding a value survives
// when the object's class is **final** -- with no subclass able to reach it
// there is nothing left that could legitimately write it again, so the value
// stands and the reconstructing `__construct` is refused. A non-final class
// clears it and `__construct` may set it once more.
class ResetRo
{
    public readonly int $a;
    public readonly int $b;
    public int $c;

    public function __construct($value, bool $setB = false)
    {
        try {
            $this->a = $value;
        } catch (Error $e) {
            printf("%s: %s\n", $e::class, $e->getMessage());
        }
        if ($setB) {
            $this->b = $value;
        }
        $this->c = $value;
    }
}

final class ResetRoFinal extends ResetRo
{
}

foreach ([new ResetRo(1), new ResetRoFinal(1)] as $subject) {
    $reflector = new ReflectionClass($subject::class);
    $reflector->resetAsLazyGhost($subject, function ($subject) {
        $subject->__construct(2, setB: true);
    });
    $reflector->initializeLazyObject($subject);
    var_dump($subject);
}

// Keeping a property means it is not deferred either. Reset a final class whose
// only property is a kept readonly one and **nothing** is deferred, so the
// object is not lazy and the initializer never runs.
final class ResetRoOnly
{
    public readonly int $a;

    public function __construct($value)
    {
        $this->a = $value;
    }
}

$only = new ResetRoOnly(1);
$onlyReflector = new ReflectionClass(ResetRoOnly::class);
$onlyReflector->resetAsLazyGhost($only, function ($only) {
    echo "never\n";
});
var_dump($onlyReflector->isUninitializedLazyObject($only), $only->a);
