<?php
// A generator still suspended when the script ends is destroyed then, and
// runs its finally blocks; one never started runs nothing.
function gen($name) {
    try {
        echo "$name starts\n";
        yield 1;
    } finally {
        echo "$name finally\n";
    }
}
$a = gen("a");
$a->current();
$b = gen("b");
function keep() {
    static $kept;
    $kept = gen("kept");
    $kept->current();
}
keep();
echo "end of script\n";
