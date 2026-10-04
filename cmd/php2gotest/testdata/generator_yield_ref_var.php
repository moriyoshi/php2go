<?php
// A `function &gen()` yields a variable as its box: `foreach ($gen as &$v)`
// binds to the generator's variable, so a write to `$v` reaches it and
// whatever it is itself bound to. A by-value reader -- `current()`, a
// by-value loop -- sees the value.

$b = 1;
function &pairs() {
    global $b;
    foreach (['1', &$b] as $k => &$item) {
        $item = "in$k";
        yield $item;
        echo "after $k: ";
        var_dump($b);
    }
}
foreach (pairs() as $k => &$v) {
    echo "got $k: ";
    var_dump($v);
    $v = 8;
}
unset($v);
var_dump($b);

function &counter() {
    $n = 0;
    while ($n < 3) {
        yield $n;
        $n++;
    }
}
foreach (counter() as &$n) {
    $n++;
    echo $n, " ";
}
echo "\n";

$g = counter();
var_dump($g->current());
$g->next();
var_dump($g->current());
foreach (counter() as $n) {
    echo $n, " ";
}
echo "\n";
// A by-value loop's variable is a copy: writing it leaves the counter alone.
foreach (counter() as $n) {
    echo $n, " ";
    $n = 5;
}
echo "\n";
$g = counter();
$c = $g->current();
$c = 7;
$g->next();
var_dump($g->current());
// A consumer that keeps what it is handed keeps values, not the one box.
$g = counter();
$kept = [$g->current()];
function bump($x) { $x++; return $x; }
var_dump(bump($g->current()));
$g->next();
$kept[] = $g->current();
foreach (counter() as $n) {
    $kept[] = $n;
}
var_dump($kept);
