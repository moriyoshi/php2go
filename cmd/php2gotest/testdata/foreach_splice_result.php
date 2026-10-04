<?php
// A by-reference foreach over an array that array_splice rebuilds resumes
// where the engine's iterator lands -- which depends on whether the caller
// keeps the removed elements.
function walk(bool $keep) {
    $a = [0, 1, 2, 3, 4];
    $done = false;
    foreach ($a as &$v) {
        echo $v, " ";
        if (!$done && $v == 3) {
            $done = true;
            if ($keep) {
                $removed = array_splice($a, 1, 2);
                echo "[removed ", implode(",", $removed), "] ";
            } else {
                array_splice($a, 1, 2);
            }
        }
    }
    echo "\n";
}
walk(false);
walk(true);

$b = [0, 1, 2, 3, 4];
$once = false;
foreach ($b as &$w) {
    echo $w, " ";
    if (!$once && $w == 2) {
        $once = true;
        array_splice($b, 1, 3, ["x", "y"]);
    }
}
echo "\n";
