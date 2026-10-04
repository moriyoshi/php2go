<?php
// A `finally` with its own try/finally, reached by a `return` or by falling
// off the end of the try, continues where that exit was going once both
// finallys have run: the return value survives, and the code after the try
// runs exactly once.

function ret() {
    try {
        return 5;
    } finally {
        try {
            echo 1;
        } finally {
            echo 2;
        }
    }
}
var_dump(ret());

function fall() {
    try {
        echo "a";
    } finally {
        try {
            echo "b";
        } finally {
            echo "c";
        }
    }
    echo "d\n";
    return "after";
}
var_dump(fall());

function loop() {
    $out = [];
    for ($i = 0; $i < 3; $i++) {
        try {
            if ($i == 1) {
                continue;
            }
            $out[] = $i;
        } finally {
            try {
                $out[] = "f$i";
            } finally {
                $out[] = "g$i";
            }
        }
    }
    return implode(",", $out);
}
var_dump(loop());
