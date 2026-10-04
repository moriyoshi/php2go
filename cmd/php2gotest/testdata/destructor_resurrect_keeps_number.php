<?php
// A destructor that stores `$this` keeps its object alive, and with it the
// handle number: the next object made does not get it.
class R { public static $keep = []; function __construct(public $n) {} function __destruct() { echo "resurrect {$this->n}\n"; R::$keep[] = $this; } }
function r() { new R("r1"); }
r();
var_dump(spl_object_id(new stdClass));
var_dump(spl_object_id(R::$keep[0]));
echo "end\n";
