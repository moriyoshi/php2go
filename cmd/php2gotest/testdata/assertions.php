<?php
// assert()'s options are request state on the Ctx, and the body moved to
// runtime/stdlib -- what kept it in the interpreter is that a failing assertion
// can invoke a callback, which is the Ctx call hook.
//
// Only the defaults are exercised here: `assert_options()` is a **userland PHP**
// function, which compiled code cannot reach until internal/userland is linked
// into the output. The options themselves are covered by the differential
// suite, whose interpreter arm does have it.
var_dump(assert(1 == 1));
var_dump(assert(true));
var_dump(assert("non-empty"));

try {
    assert(1 == 2);
} catch (AssertionError $e) {
    echo get_class($e), ": ", var_export($e->getMessage(), true), "\n";
}
try {
    assert(false, "a description");
} catch (AssertionError $e) {
    echo $e->getMessage(), "\n";
}
// A description that is an object is thrown as it stands, rather than becoming
// an AssertionError describing it.
try {
    assert(false, new RuntimeException("thrown as given"));
} catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
echo "still running\n";
