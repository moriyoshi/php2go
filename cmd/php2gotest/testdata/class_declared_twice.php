<?php
// Two declarations of one class at top level: the second is the fatal the
// engine's trial link reports, and the first is the class the program has
// until then (name_collision_01).

echo "before\n";
class A { const V = 1; }
class A { const V = 2; }
echo "never\n";
