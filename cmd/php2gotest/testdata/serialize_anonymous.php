<?php
// An anonymous class is not serializable, directly or inside an array.

$anon = new class('arg') {
    public $p = 1;
    public function __construct($i) {}
};
foreach ([$anon, ['nested' => $anon]] as $value) {
    try {
        var_dump(serialize($value));
    } catch (Exception $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
var_dump(serialize(new class { public $ok = true; } instanceof stdClass));
