<?php
// class_alias onto a reserved type name is a fatal error once the aliased
// class is found; `_` is only deprecated, and `callable` or `array` are
// ordinary names (scalar_reserved*_class_alias, gh15976/alias-names).

var_dump(class_alias('stdClass', '_'));
var_dump(class_alias('stdClass', 'callable'), class_alias('stdClass', 'Array'));
var_dump(class_alias('NoSuch', 'int'));
register_shutdown_function(function () { echo "shutdown\n"; });
try {
    class_alias('stdClass', 'Int');
} catch (Throwable $t) {
    echo "caught\n";
}
echo "never\n";
