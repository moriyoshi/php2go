<?php
// `$GLOBALS["g"]` is the **same opcode** as `$$n` naming a different symbol
// table, and only the fetch scope tells them apart. Reading it as a local made
// a function see its own undefined `$g` instead of the file body's.

$g = 1;
$shared = "top";

function readsGlobal() {
    var_dump($GLOBALS["g"]);
    $GLOBALS["g"] = 2;
    // A name the file body never mentioned is created in the globals table,
    // the same way `global $new` creates one.
    $GLOBALS["created"] = "here";
}
readsGlobal();
var_dump($g);

function seesCreated() { global $created; var_dump($created); }
seesCreated();

// isset over a *name* rather than a slot, in both tables.
function issetByName() {
    var_dump(isset($GLOBALS["g"]), isset($GLOBALS["nope"]), empty($GLOBALS["g"]));
    $a = 5;
    $n = "a";
    var_dump(isset($$n), empty($$n));
    $n = "zz";
    var_dump(isset($$n), empty($$n));
}
issetByName();

// Array unpacking: integer keys renumber, string keys keep their name, and a
// later string key overwrites an earlier one.
$nums = [1, 2];
$named = ["x" => 10, "y" => 20];
var_dump([...$nums], [0, ...$nums, 9], [...$named], [...$nums, ...$named]);
var_dump(["x" => "first", ...$named], [...$named, "x" => "last"]);

// A saturated key is not a saturation problem here: the spread renumbers.
$big = [PHP_INT_MAX => "m"];
var_dump([...$big, ...[1]]);

// A generator is drained through the ordinary protocol, keys and all.
function gen() { yield 1; yield "s" => 2; yield 3; }
var_dump([...gen()]);

// The two refusals are different classes.
$n = 5;
$o = new stdClass;
try { var_dump([...$n]); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { var_dump([...$o]); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
