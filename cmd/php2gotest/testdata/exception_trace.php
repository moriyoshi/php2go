<?php
// **An exception's trace omits the receiver**; debug_backtrace() supplies it.
// The rendering is the shared one, so both back-ends answer alike.
function inner($x) { throw new RuntimeException("boom", 7); }
function outer($a, $b) { inner($a); }

try {
    outer("one", 2);
} catch (RuntimeException $e) {
    foreach ($e->getTrace() as $i => $frame) {
        echo $i, ": ", $frame["function"], " args=", count($frame["args"] ?? []), "\n";
        echo "   has file=", isset($frame["file"]) ? "yes" : "no", " line=", isset($frame["line"]) ? "yes" : "no", "\n";
    }
    echo $e->getMessage(), " ", $e->getCode(), "\n";
    var_dump(count($e->getTrace()));
}

// A throw at the top level records no frames at all.
try { throw new Exception("top"); } catch (Exception $e) { var_dump($e->getTrace()); }

// A method's frame names the class.
class Holder {
    public function go() { throw new LogicException("m"); }
    public static function stat() { throw new LogicException("s"); }
}
foreach ([fn() => (new Holder)->go(), fn() => Holder::stat()] as $run) {
    try { $run(); } catch (LogicException $e) {
        $top = $e->getTrace()[0];
        echo $top["function"], " ", $top["class"] ?? "-", " ", $top["type"] ?? "-", "\n";
    }
}

// Two closures written on one line share `{closure:file:line}`, which is the
// name PHP gives them -- so a table keyed by that name keeps only one.
$pair = [fn() => "first", fn() => "second"];
foreach ($pair as $i => $f) { echo $i, "=", $f(), "\n"; }

// **A `#[\SensitiveParameter]` argument is not shown.** The frame still holds
// the value -- the trace renders a placeholder over it -- and the wrapper shows
// no properties, because the whole point is that nothing walking a table
// reaches the password.
function login($user, #[\SensitiveParameter] $password, $note) {
    throw new Exception("nope");
}
try {
    login("bob", "hunter2", "x");
} catch (Exception $e) {
    echo $e->getTraceAsString(), "\n";
    $args = $e->getTrace()[0]["args"];
    echo get_class($args[1]), " props=", count(get_object_vars($args[1])), "\n";
    var_dump($args[0], $args[2]);
}
