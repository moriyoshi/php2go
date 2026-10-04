<?php
// __debugInfo returning anything but an array or null is a **fatal error**
// at the var_dump -- no catch sees it -- named at the script's line; the
// shutdown functions still run (debug_info-error-*).

class C {
    public function __construct(public $val) {}
    public function __debugInfo() { return $this->val; }
}
register_shutdown_function(function () { echo "shutdown\n"; });
var_dump(new C(null));
var_dump(new C(["shown" => 1]));
try {
    var_dump(new C(0));
} catch (Throwable $t) {
    echo "caught\n";
}
echo "never\n";
