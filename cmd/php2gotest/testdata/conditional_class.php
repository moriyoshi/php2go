<?php
if (true) { class Yes { public $v = 1; } }
if (false) { class No {} }
$y = new Yes();
echo $y->v, "\n";
try {
    $n = new No();
    echo "unreachable\n";
} catch (Error $e) {
    echo "caught: ", $e->getMessage(), "\n";
}
