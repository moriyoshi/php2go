<?php
// `catch (E)` with no variable releases only what nothing else holds. Here
// the thrown object is also in `$saved`, so it lives on past the catch and
// its destructor runs at the end.

class E extends Exception {
    public function __destruct() { echo "dtor E\n"; }
}

$saved = new E("s");
try {
    throw $saved;
} catch (E) {
    echo "caught\n";
}
echo "still alive: ", $saved->getMessage(), "\n";

var_dump(spl_object_id(new stdClass));
echo "end\n";
