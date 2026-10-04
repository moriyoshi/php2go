<?php
// A class-like declared inside a function is declared each time the
// function runs; the second time is a fatal error naming its kind and where
// it was first declared (declare_already_in_use). An interface says so.

register_shutdown_function(function () { echo "shutdown\n"; });
function once() {
    interface Shape {}
    class Box implements Shape {}
}
var_dump(class_exists('Box'), interface_exists('Shape'));
once();
var_dump(class_exists('Box'), new Box instanceof Shape);
once();
echo "never\n";
