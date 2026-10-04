<?php
// A string offset's key follows three rules that are not the array-key rules,
// and isset asks a fourth question again. Every answer here came from the
// engine.
$s = "hello";

echo "read\n";
foreach ([1, -1, 1.5, true, null, "1", " 1", "1 ", "-1", "+1", "1x", "0x1", "1_0", "1.5", "1e0", "1.5x", "x", "", " ", 9, [1], new stdClass] as $k) {
    try {
        var_dump($s[$k]);
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

echo "isset\n";
foreach ([1, -1, 1.5, true, null, "1", "-1", "1x", "1.5", "x", 9, [1], new stdClass] as $k) {
    var_dump(isset($s[$k]));
}

echo "coalesce\n";
foreach ([1, 1.5, "1", "1x", "1.5", "x", 9] as $k) {
    try {
        var_dump($s[$k] ?? "d");
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

echo "write\n";
foreach (["1", "1x", "1.5", "x"] as $k) {
    $t = "hello";
    try {
        $t[$k] = "Z";
        var_dump($t);
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
