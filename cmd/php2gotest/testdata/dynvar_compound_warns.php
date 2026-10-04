<?php
// A compound assign through a **variable variable** warns when it reads an
// undefined name, exactly as the plain spelling does:
//
//   $$n .= "x"   on an unset $a   ->   Warning: Undefined variable $a
//
// It was silent here because `dynvarSlot` *creates* the slot before the read,
// so by the time the read runs there is storage holding undef and nothing left
// to notice. The name has to travel on the place for the read to say it.
$n = "a";
$$n .= "x";
var_dump($a);

// A plain assignment through one reads nothing, so it says nothing.
$m = "b";
$$m = 5;
var_dump($b);

// Arithmetic compound assigns warn the same way.
$p = "c";
$$p += 1;
var_dump($c);
$q = "d";
$$q .= "!";
var_dump($d);

// A name that IS defined warns about nothing.
$r = "e";
$e = "seed";
$$r .= "!";
var_dump($e);

// The ordinary spelling behaves identically.
$u .= "y";
var_dump($u);
$v = "start";
$v .= "-more";
var_dump($v);

// Reading through `$$n` without assigning still warns once.
$w = "absent";
var_dump(@$$w);
echo "done\n";
