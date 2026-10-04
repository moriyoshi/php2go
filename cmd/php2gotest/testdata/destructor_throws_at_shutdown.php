<?php
// A destructor that throws while the request ends.
//
// Nothing can catch it, so it is uncaught: the fatal block, with the engine
// as the caller -- `#0 [internal function]: Loud->__destruct()`. Compiled
// output dropped the throw and printed nothing.
//
// **The fatal ends the walk, not the request.** The survivors are destroyed in
// reverse order before the shutdown functions; the first fatal stops that, the
// shutdown functions run -- a fatal in one of those ends *their* list -- and
// then the objects left over are destroyed in a second walk, which the next
// fatal ends for good. So `first` is never destroyed, while the object `third`
// made on its way out is, newest first.
class Quiet
{
    public function __destruct()
    {
        echo "destruct quiet\n";
    }
}

class Loud
{
    public function __construct(public string $tag)
    {
    }

    public function __destruct()
    {
        echo "destruct {$this->tag}\n";
        if ($this->tag === 'third') {
            $GLOBALS['quiet'] = new Quiet();
        }
        throw new Exception("from {$this->tag}");
    }
}

$first = new Loud('first');
$second = new Loud('second');
$third = new Loud('third');
register_shutdown_function(function () {
    echo "shutdown one\n";
    throw new RuntimeException('from shutdown');
});
register_shutdown_function(function () {
    echo "shutdown two\n";
});
echo "body done\n";
