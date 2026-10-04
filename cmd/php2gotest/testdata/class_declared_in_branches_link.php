<?php
// The classes after a declaration in two branches are linked against the
// one bound: an incompatible override is still the engine's fatal.
$n = strlen("ab");
if ($n > 1) {
    class B
    {
        public function m(int $a)
        {
        }
    }
} else {
    class B
    {
    }
}
echo "linked\n";
class Y extends B
{
    public function m(string $a)
    {
    }
}
echo "not reached\n";
