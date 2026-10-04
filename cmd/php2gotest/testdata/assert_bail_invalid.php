<?php
// A callback that cannot be called, under ASSERT_BAIL: the failure warning,
// then the Error reported as a warning, then the end of the script.
@assert_options(ASSERT_EXCEPTION, 0);
@assert_options(ASSERT_BAIL, 1);
@assert_options(ASSERT_CALLBACK, "no_such_function");
try {
    assert(false);
} catch (Error $e) {
    echo "caught\n";
}
echo "after\n";
