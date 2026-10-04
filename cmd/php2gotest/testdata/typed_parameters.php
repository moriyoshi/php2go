<?php
// A class-typed parameter admits null only when the declaration says so: the
// `?` it carries, or the one an `= null` default confers. So a bare class type
// given null is a TypeError, and the declaration is the whole truth about it.
class Real {}
function plain(Real $a) {}
function nullable(?Real $a) {}
function defaulted(Real $a = null) {}
try { plain(null); echo "plain: accepted\n"; } catch (TypeError $e) { echo "plain: ", $e->getMessage(), "\n"; }
try { nullable(null); echo "nullable: accepted\n"; } catch (TypeError $e) { echo "nullable: ", $e->getMessage(), "\n"; }
try { defaulted(null); echo "defaulted: accepted\n"; } catch (TypeError $e) { echo "defaulted: ", $e->getMessage(), "\n"; }

// **A by-reference parameter is verified too**, and the coercion is written back
// through the box -- what would break the link the `&` makes is copying the
// slot, not writing through it.
function byRefInt(int &$r) { var_dump($r); }
$s = "21";
byRefInt($s);
var_dump($s);
$bad = [1];
try { byRefInt($bad); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($bad);

// A by-reference parameter given null against a class type, reached the two
// ways an argument can arrive: as a box, and by value through an unpack.
function byRefClass(Real &$r) {}
$n = null;
try { byRefClass($n); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
function yieldNull() { yield null; }
try { byRefClass(...yieldNull()); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }

// A satisfied declaration is untouched.
$ok = new Real;
byRefClass($ok);
var_dump($ok instanceof Real);
