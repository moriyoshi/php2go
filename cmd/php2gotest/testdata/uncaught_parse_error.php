<?php
// An uncaught ParseError is reported as the parse error it stands for: one
// line, no trace, no previous chain, and error_get_last() says E_PARSE.
register_shutdown_function(function () {
    var_dump(error_get_last());
});
class Sub extends ParseError {}
try {
    throw new Sub("a subclass is an ordinary exception");
} catch (ParseError $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
function fail() {
    throw new ParseError("broken on purpose", 3, new Exception("previous"));
}
fail();
