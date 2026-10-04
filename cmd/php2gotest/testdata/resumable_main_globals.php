<?php
// The file body makes a call that may suspend (`inner()` below, at the top
// level), so CPS turns it into a state machine whose variables are frame
// fields. They are still the globals: `global`, `$GLOBALS`,
// `get_defined_vars()` at file scope, an include at file scope and `$$name`
// all reach the same slots. Before, nothing registered them and
// `global $x` in a function read NULL.
function inner() { return Fiber::suspend('s'); }
function show($label) {
    global $x, $made;
    echo $label, ": ", var_export($x, true), " ", var_export($made ?? null, true), "\n";
}
function make() {
    global $made;
    $made = "made in a function";
    $GLOBALS['viaGlobals'] = 'written through $GLOBALS';
}

$x = 5;
show("before");
make();
var_dump($made, $viaGlobals);
var_dump($GLOBALS['x']);

$name = 'dyn';
$$name = 'variable variable';
var_dump($dyn);
// A name only `$$` ever spells has no slot here: it lives in the table alone.
$other = 'onlyDynamic';
$$other = 'no slot of its own';
function readDynamic() { global $onlyDynamic; return $onlyDynamic; }
var_dump(readDynamic());
$vars = get_defined_vars();
var_dump($vars['x'], $vars['dyn'], array_key_exists('made', $vars));

include __DIR__ . '/resumable_main_globals.inc';
var_dump($fromInclude, $x);

$f = new Fiber(function () { show("in fiber"); inner(); show("resumed"); });
$f->start();
// The call that makes the file body resumable: outside a fiber it throws.
try {
    inner();
} catch (FiberError $e) {
    echo $e->getMessage(), "\n";
}
$x = 'after the split';
$f->resume();
show("end");
