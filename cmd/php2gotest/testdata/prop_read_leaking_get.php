<?php
// A __get that stores $this keeps a fresh receiver alive past the read.
class Magic {
    public static $seen = [];
    public function __get($name) {
        self::$seen[] = $this;
        return $name;
    }
}
var_dump((new Magic)->anything);
var_dump(new stdClass);
var_dump(count(Magic::$seen));
