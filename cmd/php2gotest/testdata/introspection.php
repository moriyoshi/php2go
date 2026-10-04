<?php
// `get_mangled_object_vars` reports the table under the engine's **storage
// keys** -- `"\0*\0b"` for protected, `"\0C\0c"` for private -- which is what
// separates it from `get_object_vars`: that one asks what the calling scope can
// reach, and this one asks what is there.
class Holder {
    public $a = 1;
    protected $b = 2;
    private $c = 3;
    public int $never;
    public ?string $null = null;
}
var_dump(get_mangled_object_vars(new Holder));

class Child extends Holder { private $d = 4; }
var_dump(get_mangled_object_vars(new Child));

$plain = new stdClass;
$plain->x = 1;
$plain->{"a b"} = 2;
var_dump(get_mangled_object_vars($plain));
var_dump(get_mangled_object_vars(new stdClass));

// The engine supplies no traits of its own, so this one is answerable where
// get_declared_classes() is not: that lists every internal class first.
trait First {}
trait Second { public function m() {} }
var_dump(get_declared_traits());

// A resource names its type.
$handle = fopen("/dev/null", "r");
var_dump(get_resource_type($handle));
fclose($handle);
var_dump(get_resource_type($handle));
