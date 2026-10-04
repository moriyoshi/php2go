<?php
// A virtual hooked property has no storage, so unserialize has nowhere to
// put a value serialized for one: a warning naming it, the offset warning,
// and false (property_hooks/unserialize). A backed hooked property takes
// its value. (An inherited virtual one is named by its declaring class,
// which the runtime's hook does not carry yet -- see TODO.)

class Test {
    public $backed = 1;
    public $virt {
        get { return 'v'; }
        set { echo "set\n"; }
    }
    public $withHook = 2 {
        set => $value * 10;
    }
}
class Child extends Test {}

var_dump(unserialize('O:4:"Test":1:{s:4:"virt";i:42;}'));
print_r(unserialize('O:4:"Test":2:{s:6:"backed";i:5;s:8:"withHook";i:7;}'));
echo "\n";
print_r(unserialize(serialize(new Test)));
