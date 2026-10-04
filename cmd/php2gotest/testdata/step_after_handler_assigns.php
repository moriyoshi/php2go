<?php
// `++$o->p` on an unset property warns `Undefined property`, and the step
// is applied to what the property holds once the error handler has run: a
// handler assigning an object makes it `Cannot increment stdClass`, one
// assigning a number makes it that number plus one
// (in-de-crement/unset_property_converted_to_obj_in_error_handler).

class C {
    public $a;
    public $b;
    public $mode;
    function handler() {
        if ($this->mode === 'obj') {
            $this->a = new stdClass();
        } else {
            $this->b = 41;
        }
    }
}
$c = new C;
set_error_handler([$c, 'handler']);
unset($c->a, $c->b);
$c->mode = 'obj';
try {
    ++$c->a;
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump($c->a);
$c->mode = 'int';
var_dump($c->b++, $c->b);
