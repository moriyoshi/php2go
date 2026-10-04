<?php
// One function per way of defeating the analysis. Each must be refused, and for
// the stated reason -- a promotion that fires on any of these changes what the
// program prints, not how fast it runs.

function promotable(array $rows): string {
    $out = '';
    foreach ($rows as $r) {
        $out .= $r;
    }
    return $out;
}

function byReference(array $rows): string {
    $out = '';
    $alias =& $out;
    foreach ($rows as $r) {
        $out .= $r;
    }
    return $alias;
}

function capturedByClosure(array $rows): string {
    $out = '';
    $spy = function () use (&$out) { return $out; };
    foreach ($rows as $r) {
        $out .= $r;
    }
    return $out . $spy();
}

function passedByReference(array $rows): string {
    $out = '';
    foreach ($rows as $r) {
        $out .= $r;
    }
    \settype($out, 'string');
    return $out;
}

function boundToAGlobal(array $rows): string {
    global $out;
    $out = '';
    foreach ($rows as $r) {
        $out .= $r;
    }
    return $out;
}

function boundToAStatic(array $rows): string {
    static $out = '';
    foreach ($rows as $r) {
        $out .= $r;
    }
    return $out;
}

function readInsideTheLoop(array $rows): string {
    $out = '';
    foreach ($rows as $r) {
        $out .= $r;
        echo $out;
    }
    return $out;
}

function unsetMidway(array $rows): string {
    $out = '';
    foreach ($rows as $r) {
        $out .= $r;
        unset($out);
    }
    return $out ?? '';
}

function variableVariables(array $rows, string $name): string {
    $out = '';
    foreach ($rows as $r) {
        $out .= $r;
    }
    return $out . $$name;
}

function extractsIntoTheFrame(array $rows): string {
    $out = '';
    \extract($rows);
    foreach ($rows as $r) {
        $out .= $r;
    }
    return $out;
}
