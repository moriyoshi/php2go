<?php
// Both declarations reached: the second is the engine's fatal, naming the
// class already bound by its spelling.
$n = strlen("ab");
if ($n > 1) {
    class Twice
    {
    }
}
echo "between\n";
if ($n > 1) {
    class TWICE
    {
        public $x;
    }
}
echo "not reached\n";
