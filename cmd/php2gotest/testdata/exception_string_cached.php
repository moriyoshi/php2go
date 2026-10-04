<?php
// Converting a throwable to a string keeps the result in its private
// `string` property -- on the outer exception, not on the ones it wraps.
function dump(Throwable $t) {
    $a = (array) $t;
    foreach ($a as $k => $v) {
        if (str_ends_with($k, "string")) {
            var_dump(strlen($v) > 0 ? substr($v, 0, 20) : "(empty)");
        }
    }
}
$e = new RuntimeException("outer", 1, new LogicException("inner"));
dump($e);
$text = $e->__toString();
dump($e);
dump($e->getPrevious());
$err = new Error("an error");
echo strlen((string) $err) > 0 ? "converted\n" : "";
dump($err);
