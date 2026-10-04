<?php
// A compile-time fatal is the program's whole output: php prints the
// diagnostic and runs nothing, not even the echo written before it. The
// engine reports it by bailing out, and that is an answer to replay rather
// than a gap.

echo "never printed\n";

function f() {}
function f() {}
