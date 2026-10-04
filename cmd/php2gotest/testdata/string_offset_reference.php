<?php
// A reference to a string offset is refused, and the offset is read first:
// a leading-integer key warns `Illegal string offset` before the Error.

$a = 'aaa';
foreach (['2bbb', '1', 1] as $k) {
    try {
        $r = &$a[$k];
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
