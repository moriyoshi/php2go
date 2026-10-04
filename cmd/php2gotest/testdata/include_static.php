<?php
// A literal include at global scope runs the included file over the file
// body's variables, declares its functions and classes when it runs, and
// answers what the file returns.
$shared = "main";
$gone = "still here";
var_dump(function_exists('inc_fn'), class_exists('IncClass', false));
$r = include __DIR__ . '/include_static.inc';
var_dump($r, $fromInc, $shared, isset($gone), inc_fn(), new IncClass);
// Already included: the _once form answers true and runs nothing.
var_dump(include_once __DIR__ . '/include_static.inc');
var_dump(require_once __DIR__ . '/include_static_once.inc', require_once __DIR__ . '/include_static_once.inc');
