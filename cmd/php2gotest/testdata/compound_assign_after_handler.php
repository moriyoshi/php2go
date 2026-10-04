<?php
// A compound assignment to an undefined property reads it with a warning,
// and an error handler that assigns the property meanwhile is what the
// operation then works on -- the engine's fetch points at the slot the
// handler wrote (oss_fuzz_61712b), as `++` does.

#[AllowDynamicProperties]
class C {
    function error($_, $msg) {
        echo $msg, "\n";
        $this->a = 12345;
        $this->s = "pre";
    }
}

$c = new C;
set_error_handler([$c, 'error']);
$c->a %= 10;
var_dump($c->a);
$c->s .= "-post";
var_dump($c->s);
unset($c->a);
$c->a += 1;
var_dump($c->a);
$c->b ??= 7;
var_dump($c->b);
