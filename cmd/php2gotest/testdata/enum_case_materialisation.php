<?php
// A pure enum builds a case's object when that case is first used; a
// backed enum builds all of them together on first use. The handles each
// takes show it, as does the next object after them.
enum P { case A; case B; case C; }
enum K: int { case A = 1; case B = 2; case C = 3; }
var_dump(new stdClass);
$x = P::B;
var_dump(new stdClass);
$y = P::C;
var_dump(new stdClass);
$z = K::B;
var_dump(new stdClass);
$w = K::A;
var_dump(new stdClass);
var_dump(count(P::cases()), new stdClass);
