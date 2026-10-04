<?php
// debug_backtrace and trigger_error moved to runtime/stdlib: the Ctx has
// carried the call stack for a while, and the one thing that kept the trace in
// the interpreter -- replacing a `#[\SensitiveParameter]` argument with a
// SensitiveParameterValue -- is a Ctx hook now.
function inner($a, $b) {
    foreach (debug_backtrace() as $i => $frame) {
        echo $i, " ", $frame["function"],
            " line=", $frame["line"] ?? "-",
            " args=", count($frame["args"] ?? []),
            isset($frame["class"]) ? " class=" . $frame["class"] . $frame["type"] : "",
            "\n";
    }
}

function outer($x) { inner($x, "second"); }

class Holder {
    public function run() { outer(1); }
    public static function fire() { (new Holder)->run(); }
}

Holder::fire();

echo "--- ignore args ---\n";
function noargs() {
    $frames = debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS);
    var_dump(array_key_exists("args", $frames[0]));
}
noargs();

echo "--- limit ---\n";
function deep3() { var_dump(count(debug_backtrace(0, 2))); }
function deep2() { deep3(); }
function deep1() { deep2(); }
deep1();

echo "--- object key ---\n";
class Owner {
    public function ask() {
        $frames = debug_backtrace();
        var_dump(isset($frames[0]["object"]));
        $bare = debug_backtrace(0);
        var_dump(isset($bare[0]["object"]));
    }
}
(new Owner)->ask();

echo "--- trigger_error ---\n";
trigger_error("a notice");
trigger_error("a warning", E_USER_WARNING);
trigger_error("deprecated now", E_USER_DEPRECATED);
try {
    trigger_error("bad level", 12345);
} catch (ValueError $e) {
    echo $e->getMessage(), "\n";
}
// E_USER_ERROR is not exercised here: it ends the request, and the oracle runs
// the engine through `zend_eval_string`, which bails out rather than handing
// back output to compare against -- so a fixture containing one grades as
// `noreference`, not as a pass. Its compiled behaviour was checked against the
// interpreter by hand instead; see JOURNAL.md.
