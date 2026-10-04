<?php
// A deprecated closure is named in the deprecation by the path the program
// refers to itself by -- the same name its trace and reflector use.

$f = #[\Deprecated] function () { return 1; };
$g = #[\Deprecated("use h")] fn () => 2;
var_dump($f(), $g());
