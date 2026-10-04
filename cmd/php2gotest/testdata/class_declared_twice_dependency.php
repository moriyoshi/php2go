<?php
// A failing declaration a variance check came to depend on while it was
// linking -- `Y::t(): B` over `X::t(): K`, answered through B's own
// `implements K` -- can no longer be unlinked: its failure is the engine's
// fatal (variance/loading_exception1, probed).

interface K {}
interface L extends K {}

spl_autoload_register(function ($c) {
    throw new LogicException("cannot load $c");
});

try {
    class B extends Gone {}
} catch (LogicException $e) {
    echo $e->getMessage(), "\n";
}

spl_autoload_register(function ($c) {
    echo "first for $c\n";
    class X { public function t(): K {} }
    class Y extends X { public function t(): B {} }
}, true, true);

try {
    class B extends Gone implements L {}
} catch (LogicException $e) {
    echo "not reached\n";
}
