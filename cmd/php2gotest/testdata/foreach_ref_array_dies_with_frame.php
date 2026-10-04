<?php
// A by-reference foreach over a local array makes each element a reference.
// When the function returns, the array dies and the property bound to the
// element is its only holder, so var_dump prints no `&` (bug80037). An array
// that outlives the frame keeps the marker on both sides.
//
// The objects are made and kept by the caller so their handles do not depend
// on how long temporaries live.

function bindAll(stdClass $me, array $props) {
    foreach ($props as $k => &$v) {
        $me->{$k} = &$v;
    }
    return $me;
}
$o1 = new stdClass;
var_dump(bindAll($o1, ['a' => 'foo', 'b' => 2]));

function bindAllUnset(stdClass $me, array $props) {
    foreach ($props as $k => &$v) {
        $me->{$k} = &$v;
    }
    unset($v);
    return $me;
}
$o2 = new stdClass;
var_dump(bindAllUnset($o2, ['a' => 'foo']));

function bindLocal(stdClass $me) {
    $props = ['x' => [1], 'y' => 'bar'];
    foreach ($props as $k => &$v) {
        $me->{$k} = &$v;
    }
    return $me;
}
$o3 = new stdClass;
var_dump(bindLocal($o3));

// Walked twice by an outer loop, binding a property each time: the array is
// still one holder going away, and the two properties keep each other's `&`.
function bindTwice(stdClass $me, array $props) {
    for ($i = 0; $i < 2; $i++) {
        foreach ($props as $k => &$v) {
            $me->{$k . $i} = &$v;
        }
    }
    return $me;
}
$o4 = new stdClass;
var_dump(bindTwice($o4, ['a' => 'foo']));

// The array is returned: it is still a holder.
function bindAndKeep(stdClass $me) {
    $props = ['a' => 'foo'];
    foreach ($props as $k => &$v) {
        $me->{$k} = &$v;
    }
    return [$me, $props];
}
$o5 = new stdClass;
var_dump(bindAndKeep($o5));

// Aliased by reference after the loop: the alias is what survives.
function bindAndAlias(stdClass $me) {
    $props = ['a' => 'foo'];
    foreach ($props as $k => &$v) {
        $me->{$k} = &$v;
    }
    unset($v);
    $alias = &$props;
    return [$me, $alias];
}
$o6 = new stdClass;
var_dump(bindAndAlias($o6));

// A global's array outlives the call.
function bindGlobal(stdClass $me) {
    global $gprops;
    foreach ($gprops as $k => &$v) {
        $me->{$k} = &$v;
    }
    unset($v);
    return $me;
}
$gprops = ['a' => 'foo'];
$o7 = new stdClass;
var_dump(bindGlobal($o7));

// A by-reference parameter's array is the caller's.
function bindByRef(stdClass $me, array &$props) {
    foreach ($props as $k => &$v) {
        $me->{$k} = &$v;
    }
    unset($v);
    return $me;
}
$callerProps = ['a' => 'foo'];
$o8 = new stdClass;
var_dump(bindByRef($o8, $callerProps));

// A loop that never ran never separated the caller's array, whose own
// references are not the callee's to drop.
function maybeWalk(stdClass $me, array $props, bool $walk) {
    if ($walk) {
        foreach ($props as $k => &$v) {
            $me->{$k} = &$v;
        }
    }
    return $me;
}
$x = 'shared';
$arr = ['a' => &$x];
$o9 = new stdClass;
var_dump(maybeWalk($o9, $arr, false));
var_dump($arr);

// Replaced after the loop by an array another name still holds: what the
// local holds at the end is not this frame's to release.
function bindThenReplace(stdClass $me, array $props, array $other) {
    foreach ($props as $k => &$v) {
        $me->{$k} = &$v;
    }
    unset($v);
    $props = $other;
    return $me;
}
$y = 'kept';
$arr2 = ['b' => &$y];
$o10 = new stdClass;
bindThenReplace($o10, ['a' => 'foo'], $arr2);
var_dump($arr2);

// The array itself is returned.
function bindAndReturn(stdClass $me) {
    $props = ['a' => 'foo'];
    foreach ($props as $k => &$v) {
        $me->{$k} = &$v;
    }
    unset($v);
    return $props;
}
$o11 = new stdClass;
$returned = bindAndReturn($o11);
var_dump($o11, $returned);
