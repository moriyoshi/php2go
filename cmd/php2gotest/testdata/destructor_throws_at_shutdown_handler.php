<?php
// A destructor that throws while the request ends, with an exception handler.
//
// The handler answers for each throw in turn and the walk goes on; only a
// throw from the handler itself is fatal, and it ends the walk. A destructor
// whose own local dies throwing reports **two** uncaught exceptions at
// shutdown, the local's first, neither chained to the other.
class Loud
{
    public function __construct(public string $tag)
    {
    }

    public function __destruct()
    {
        echo "destruct {$this->tag}\n";
        throw new Exception("from {$this->tag}");
    }
}

class Nests
{
    public function __construct(public string $tag)
    {
    }

    public function __destruct()
    {
        echo "destruct {$this->tag}\n";
        $inner = new Loud("{$this->tag} inner");
        throw new Exception("from {$this->tag}");
    }
}

set_exception_handler(function (Throwable $e) {
    $previous = $e->getPrevious()?->getMessage() ?? 'none';
    echo "handled {$e->getMessage()}, previous {$previous}\n";
    if ($e->getMessage() === 'from second') {
        throw new LogicException('from the handler');
    }
});
register_shutdown_function(function () {
    echo "shutdown\n";
});

$first = new Loud('first');
$second = new Loud('second');
$third = new Nests('third');
$fourth = new Loud('fourth');
echo "body done\n";
