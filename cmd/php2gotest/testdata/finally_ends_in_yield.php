<?php
// A finally whose last statement is a `yield` resumes into what running off
// its end means: back after the try on the normal path, on with the
// unwinding on the exceptional one -- the exception kept across the
// suspension.
function returns($f, $t) {
    for ($i = $f; $i <= $t; $i++) {
        try {
            return;
        } finally {
            yield $i;
        }
    }
}
foreach (returns(1, 5) as $x) {
    echo "returns: $x\n";
}

function throws() {
    try {
        throw new Exception("kept");
    } finally {
        $got = yield 1;
        echo "sent: $got\n";
    }
}
$g = throws();
var_dump($g->current());
try {
    $g->send("s");
} catch (Exception $e) {
    echo "caught: ", $e->getMessage(), "\n";
}

function falls() {
    try {
        echo "body\n";
    } finally {
        yield 2;
    }
    echo "after\n";
    yield 3;
}
foreach (falls() as $v) {
    var_dump($v);
}
