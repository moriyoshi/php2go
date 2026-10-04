<?php
// Destructors at script end.
//
// The engine fires `__destruct` at three points: `unset()` on the sole
// reference, a function local at return, and script end for the survivors.
// Compiled output does the **third** only -- the first two need to know that a
// reference was the last one, which is the refcount problem and is not solved
// yet. So this fixture keeps every object alive to the end, which is the case
// that is right rather than the case that is merely present.
//
// Before this, compiled output ran no destructor at all.
class Noisy
{
    public function __construct(public string $tag)
    {
        echo "make {$this->tag}\n";
    }

    public function __destruct()
    {
        echo "destruct {$this->tag}\n";
    }
}

$first = new Noisy('first');
$second = new Noisy('second');
$third = new Noisy('third');
echo "body done\n";

// A class with no destructor registers nothing and costs nothing.
class Quiet
{
}

$quiet = new Quiet();
var_dump($quiet instanceof Quiet);

// A shutdown function runs **before** teardown, and still sees the objects.
register_shutdown_function(function () {
    echo "shutdown\n";
});
echo "end of script\n";

// **A constructor that throws means no destructor.** The object was allocated,
// but PHP does not destroy it -- so registering for teardown has to happen
// after construction succeeds, not when the object is made.
class Throws
{
    public function __construct()
    {
        throw new Exception('boom');
    }

    public function __destruct()
    {
        echo "must not run\n";
    }
}

try {
    new Throws();
} catch (Exception $e) {
    echo "caught {$e->getMessage()}\n";
}
