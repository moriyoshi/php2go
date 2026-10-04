<?php
$fromInc = "set by the include";
$shared .= "+include";
unset($gone);
function inc_fn() { return "inc_fn"; }
class IncClass { public $p = 1; }
return "include result";
