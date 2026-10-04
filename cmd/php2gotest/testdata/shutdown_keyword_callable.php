<?php
// A keyword callable given to register_shutdown_function is resolved when
// it is registered: the deprecation is reported there, and the class it
// meant is the one called at shutdown.
class Base {
    public static function down() { echo "Base::down\n"; }
    public static function who() { echo static::class, "::who\n"; }
}
class Child extends Base {
    public static function down() { echo "Child::down\n"; }
    public static function register() {
        register_shutdown_function(["self", "down"]);
        register_shutdown_function("parent::down");
        register_shutdown_function(["static", "who"]);
    }
}
class Grand extends Child {}
Grand::register();
echo "end\n";
