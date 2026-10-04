<?php
$name = "world";
$n = 42;
$pi = 3.5;
try {
    echo $name, $n, $pi;
} catch (RuntimeException $e) {
    echo $e;
} finally {
    echo "done";
}
