<?php
// A DateInterval shows its state as ten ordinary properties, which every
// reader sees as the engine writes them.
$d = new DateInterval('P1Y2M3W4DT5H6M7S');
var_dump($d);
var_dump($d->d, $d->days, isset($d->y), isset($d->zz), isset($d->{""}));
print_r(get_object_vars(new DateInterval('PT36H')));
$w = new DateInterval('P2W');
foreach ($w as $k => $v) {
    echo $k, "=", var_export($v, true), " ";
}
echo "\n";
var_dump((array) new DateInterval('P0D'));
echo json_encode(new DateInterval('P01D')), "\n";
var_export(new DateInterval('P1M1W'));
echo "\n";
$c = clone $d;
var_dump($c->y, $c === $d, $d === $d);

// The spec: arity, type and the forms the engine refuses.
foreach (['', 'P', 'PT', 'P1DT', 'P1D1Y', 'PT1D', 'p1d', 'now', 'P00010203'] as $bad) {
    try {
        new DateInterval($bad);
    } catch (Exception $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
foreach ([[], ['P1D', 'P2D'], [[1]], [new stdClass]] as $args) {
    try {
        new DateInterval(...$args);
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
var_dump((new DateInterval(duration: 'PT1M'))->i);

// A subclass keeps its own properties ahead of the ten, and may write
// them; one that skips the parent constructor has none of the ten.
// (print_r rather than var_dump: object ids depend on when the
// exceptions above die.)
class Span extends DateInterval
{
    public $label = 'span';
}
$s = new Span('P3D');
$s->label = 'three days';
print_r($s);
class Bare extends DateInterval
{
    public $x = 1;
    public function __construct() {}
}
$b = new Bare;
print_r($b);
var_dump(@$b->y, isset($b->d));
var_dump(method_exists($d, 'format'), $d instanceof DateInterval, $s instanceof DateInterval);

// A DateInterval inside unserialize is made before its state is read, so
// a failure in a nested value is the error (bug70121).
try {
    unserialize('O:12:"DateInterval":1:{s:4:"days";O:9:"Exception":1:{s:19:"' . "\0" . 'Exception' . "\0" . 'previous";O:8:"stdClass":0:{}}}');
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    unserialize('O:9:"Exception":1:{s:7:"' . "\0" . '*' . "\0" . 'line";s:2:"12";}');
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump(unserialize('O:9:"Exception":1:{s:7:"' . "\0" . '*' . "\0" . 'line";i:12;}')->getLine());
