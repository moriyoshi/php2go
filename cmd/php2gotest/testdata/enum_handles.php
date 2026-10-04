<?php
// **Touching one enum case builds them all**, in declaration order, on first
// access -- not at start-up. The handles they take are observable, and building
// them eagerly gave every object in a program that declares an enum the wrong
// number.
enum E: int { case A = 1; case B = 2; case C = 3; }
$o = new stdClass;
var_dump(spl_object_id($o));
$x = E::B;
var_dump(spl_object_id($x), spl_object_id(E::A), spl_object_id(E::C));
$p = new stdClass;
var_dump(spl_object_id($p));

enum F { case Z; }
$q = new stdClass;
var_dump(spl_object_id($q));
// cases() materialises them too: it hands the objects out.
var_dump(count(F::cases()), spl_object_id(F::Z));
$r = new stdClass;
var_dump(spl_object_id($r));

// Identity: a case is a singleton, whichever way it is reached.
var_dump(E::A === E::A, E::from(2) === E::B, E::tryFrom(9));
