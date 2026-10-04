<?php
// An object or an array used as an array offset is not converted: PHP throws.
// Every *scalar* has a silent conversion -- null becomes "", true becomes 1 --
// so `ArrayKey` fell through to `StringKey(v.String())` for these too, and a
// Closure used as a key quietly became some string.
//
// The wording differs by what the offset was being used for, which is why these
// are three refusals rather than one.
$a = [1, 2, 3];
$o = new stdClass();

try { $x = $a[$o]; } catch (Error $e) { echo "read: ", get_class($e), ": ", $e->getMessage(), "\n"; }
try { $b = []; $b[$o] = 1; } catch (Error $e) { echo "write: ", get_class($e), ": ", $e->getMessage(), "\n"; }
try { $x = $a[[1]]; } catch (Error $e) { echo "array key: ", $e->getMessage(), "\n"; }
try { $t = []; $t[function(){}] = 1; } catch (Error $e) { echo "closure: ", $e->getMessage(), "\n"; }
// The undefined slot: PHP throws rather than autovivifying first.
try { $undef[$o] = 1; } catch (Error $e) { echo "autoviv: ", $e->getMessage(), "\n"; }
var_dump(isset($undef));

// The scalar keys stay silent, which is the other half of the claim.
$k = [];
$k[null] = "n";
$k[true] = "t";
$k[1.0] = "f";
$k["7"] = "s";
var_dump(array_keys($k));

// isset/empty and unset each have their own wording, so they are three
// refusals rather than one message reused.
$q = [1, 2];
try { var_dump(isset($q[$o])); } catch (Error $e) { echo "isset: ", $e->getMessage(), "\n"; }
try { var_dump(empty($q[$o])); } catch (Error $e) { echo "empty: ", $e->getMessage(), "\n"; }
try { unset($q[$o]); } catch (Error $e) { echo "unset: ", $e->getMessage(), "\n"; }
// The legal ones still work, which is the half a too-wide refusal would break.
var_dump(isset($q[0]), isset($q[9]), empty($q[0]));
unset($q[0]);
var_dump($q);
