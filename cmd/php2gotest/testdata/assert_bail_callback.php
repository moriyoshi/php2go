<?php
// Under ASSERT_BAIL an exception thrown by the assertion callback is
// reported as a warning and the script ends: no catch sees it, no finally
// runs, and the shutdown functions still do.
register_shutdown_function(function () {
    echo "shutdown: ", error_get_last()["message"] ?? "none", "\n";
});
@assert_options(ASSERT_EXCEPTION, 0);
@assert_options(ASSERT_BAIL, 1);
function cb($file, $line, $unused, $description) {
    throw new LogicException("from the callback: $description");
}
@assert_options(ASSERT_CALLBACK, "cb");
function check() {
    try {
        assert(1 > 2);
    } catch (Throwable $e) {
        echo "caught\n";
    } finally {
        echo "finally\n";
    }
}
check();
echo "after\n";
