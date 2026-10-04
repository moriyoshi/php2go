<?php
// `++` and `--` have their own table per type, and several entries diagnose
// rather than step. `runtime/php` owns the table and reports the refusals as
// **sentinels** -- naming the class in `Cannot increment Foo` needs a class
// table it does not have -- so the layer above maps them. Unmapped, the
// sentinel escaped the generated program as a Go error and it printed nothing
// at all.

class Foo {}

$values = [
    'bool true' => true,
    'bool false' => false,
    'null' => null,
    'int' => 1,
    'float' => 1.5,
    'numeric string' => '3',
    'alpha string' => 'a',
    'empty string' => '',
    'array' => [1],
];

foreach ($values as $label => $v) {
    $x = $v;
    echo $label, ' ++ ';
    try {
        $x++;
        var_dump($x);
    } catch (\TypeError $e) {
        echo $e->getMessage(), "\n";
    }
    $y = $v;
    echo $label, ' -- ';
    try {
        $y--;
        var_dump($y);
    } catch (\TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}

// An object names its class, which is the whole reason the refusal cannot be
// built below.
$o = new Foo;
try {
    $o++;
} catch (\TypeError $e) {
    echo $e->getMessage(), "\n";
}
$p = new Foo;
try {
    $p--;
} catch (\TypeError $e) {
    echo $e->getMessage(), "\n";
}
echo "still running\n";
