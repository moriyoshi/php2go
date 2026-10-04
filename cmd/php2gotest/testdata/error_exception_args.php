<?php
// ErrorException's own constructor arguments: `$severity` defaults to E_ERROR,
// a `$filename` replaces the file and resets the line to `$line` or 0, and a
// `$line` alone sets only the line. A subclass answers getSeverity() too.

class MyErr extends ErrorException {}
$cases = [
    new ErrorException(),
    new ErrorException("a", 0, E_WARNING, null, 123),
    new ErrorException("b", 0, E_NOTICE, "abc.php"),
    new ErrorException("c", 0, E_ERROR, "abc.php", 7),
    new MyErr("d", 3, E_USER_ERROR),
];
foreach ($cases as $e) {
    var_dump($e->getSeverity(), basename($e->getFile()), $e->getLine() > 0 ? "line>0" : $e->getLine());
}
var_dump($cases[1]->getLine());
