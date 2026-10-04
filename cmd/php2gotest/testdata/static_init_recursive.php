<?php
// A static initializer that calls its own function recurses into the same
// declaration: the innermost call stores first, and every outer one finds the
// static already set and keeps it.

function depth($i)
{
    static $a = $i < 3 ? depth($i + 1) : "bottom $i";
    echo "$i: $a\n";
    return "returned $i";
}
depth(0);
depth(7);

class Counter
{
    public function level($i)
    {
        static $first = $i < 2 ? $this->level($i + 1) . "!" : "leaf";
        return $first;
    }
}
var_dump((new Counter)->level(0));
