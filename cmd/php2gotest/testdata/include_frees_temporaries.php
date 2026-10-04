<?php
// A file body that includes another still frees a temporary once its
// consumer is done with it: the include reaches variables, not temporaries.
require_once __DIR__ . '/include_static_once.inc';
class Maker {
    public static function make() { return new Maker; }
}
var_dump(Maker::make());
var_dump(new stdClass);
