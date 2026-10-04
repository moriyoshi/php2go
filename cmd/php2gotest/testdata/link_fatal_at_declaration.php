<?php
// A class whose declaration the engine cannot link -- here an enum with a
// magic method enums may not have -- is a fatal error **where it is
// declared**: what ran before it printed, nothing after it runs, and the
// shutdown functions still do. The harvest asks the engine's own link, so
// the message is the engine's.

register_shutdown_function(function () {
    echo "shutdown\n";
});
echo "before\n";

enum Suit {
    case Hearts;
    public function __clone() {}
}

echo "never\n";
