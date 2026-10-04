<?php
// Where a local's destructor is called from when its function ends.
//
// **After the frame has gone**: the trace has no frame for the function whose
// local it was, and the call site is that function's own -- whether it returned
// or threw. Compiled output destroyed the local from inside the frame, so every
// such trace had one frame too many.
class Inner
{
    public function __destruct()
    {
        throw new Exception('from inner');
    }
}

class Outer
{
    public function __construct(public bool $throws)
    {
    }

    public function __destruct()
    {
        $inner = new Inner();
        if ($this->throws) {
            throw new Exception('from outer');
        }
    }
}

class Tracer
{
    public function __destruct()
    {
        debug_print_backtrace();
    }
}

function show(Throwable $e): void
{
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
}

function holds(bool $throws): void
{
    $outer = new Outer($throws);
}

function traced(): void
{
    $tracer = new Tracer();
    echo "in traced\n";
}

try {
    holds(true);
} catch (Exception $e) {
    show($e);
}
try {
    holds(false);
} catch (Exception $e) {
    show($e);
}
traced();
