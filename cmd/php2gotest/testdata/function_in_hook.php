<?php
// A function declared inside a property hook exists once the hook runs.
class P {
    public $x {
        get { if (true) { function inHookFn() { return "fn"; } } return 1; }
    }
}
var_dump(function_exists('inHookFn'));
$p = new P;
$p->x;
var_dump(function_exists('inHookFn'), inHookFn());
