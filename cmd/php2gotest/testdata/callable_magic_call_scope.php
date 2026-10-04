<?php
// A class-string callable naming a method the class lacks is callable
// through `__callStatic`, or through `__call` only with a compatible `$this`
// in scope; an object reaches `__call` directly. An empty class name is
// refused without asking the autoloader.

spl_autoload_register(function ($class) { echo "autoload(\"$class\")\n"; });

class OnlyCall {
    public function __call($m, $a) { return "__call $m"; }
    public function inside() {
        var_dump(is_callable(['OnlyCall', 'foo']));
        echo call_user_func(['OnlyCall', 'foo']), "\n";
    }
}
class OnlyStatic {
    public static function __callStatic($m, $a) { return "__callStatic $m"; }
}

var_dump(is_callable(['OnlyCall', 'foo']));
var_dump(is_callable([new OnlyCall, 'foo']));
var_dump(is_callable(['OnlyStatic', 'foo']));
(new OnlyCall)->inside();
try { call_user_func(['OnlyCall', 'foo']); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { call_user_func(['', 'foo']); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
