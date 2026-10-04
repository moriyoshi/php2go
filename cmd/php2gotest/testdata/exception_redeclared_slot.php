<?php
// A subclass redeclaring the engine's `$message` has one slot for it, and
// calling the constructor again writes that slot rather than adding a second
// `message` beside it.
class Plain extends Exception {
    protected $message = "default";
}
$p = new Plain("given");
$p->__construct("again");
var_dump($p);
