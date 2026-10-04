<?php
// With assertions throwing and ASSERT_BAIL on, the AssertionError is a fatal
// error at once: the catch around it never runs.
register_shutdown_function(function () {
    $last = error_get_last();
    echo "shutdown: ", $last["type"] ?? "none", "\n";
});
@assert_options(ASSERT_EXCEPTION, 1);
@assert_options(ASSERT_BAIL, 1);
try {
    assert(false, "must hold");
} catch (AssertionError $e) {
    echo "caught\n";
}
echo "after\n";
