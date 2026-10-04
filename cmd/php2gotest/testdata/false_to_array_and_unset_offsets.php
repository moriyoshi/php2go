<?php
// `false` is the **one** scalar that still autovivifies into an array, and it
// says so. Everything else refuses, each with its own wording.

function tryAppend(string $label, $v): void {
    try { $v[] = 42; echo $label, ": "; var_dump($v); }
    catch (Throwable $e) { echo $label, ": ", get_class($e), ": ", $e->getMessage(), "\n"; }
}
$false = false;      tryAppend("false ", $false);
$null = null;        tryAppend("null  ", $null);
$empty = "";         tryAppend("empty ", $empty);
$zero = 0;           tryAppend("zero  ", $zero);
$true = true;        tryAppend("true  ", $true);
$float = 1.5;        tryAppend("float ", $float);
$string = "ab";      tryAppend("string", $string);

// The keyed form converts too, and the deprecation comes before the element
// lands.
$keyed = false;
$keyed["k"] = 1;
var_dump($keyed);

// A **compound** assignment to an append has no key operand at all, which is a
// place kind of its own rather than a dimension fetch with a missing key.
$compound = false;
$addend = 42;
$compound[] &= $addend;
var_dump($compound);

$nested = false;
$nested[] .= "text";
var_dump($nested);

// `unset` deprecates and then leaves the variable alone: the conversion belongs
// to the fetch, and an unset has nothing to convert for.
$unsetFalse = false;
unset($unsetFalse[0]);
var_dump($unsetFalse);
$unsetNull = null;
unset($unsetNull[0]);
var_dump($unsetNull);

$unsetScalar = 5;
try { unset($unsetScalar[0]); } catch (Throwable $e) { echo "scalar: ", $e->getMessage(), "\n"; }
var_dump($unsetScalar);

$unsetString = "ab";
try { unset($unsetString[0]); } catch (Throwable $e) { echo "string: ", $e->getMessage(), "\n"; }
var_dump($unsetString);

// An **empty string is still a string**, not a null in disguise: the append is
// refused, a string key is a TypeError, and an integer key writes the offset.
$emptyKeyed = "";
try { $emptyKeyed["k"] = 1; } catch (Throwable $e) { echo "empty keyed: ", get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump($emptyKeyed);
$emptyOffset = "";
$emptyOffset[0] = "z";
var_dump($emptyOffset);
