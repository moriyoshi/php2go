<?php
// Objects made while the request ends, and a fatal error among them.
//
// **An object a destructor makes is destroyed in the same teardown**, once the
// walk that made it is done and before the shutdown functions: `first` makes
// one and it is destroyed right after `first`.
//
// **A bailout forgets every object still waiting**: the one `third` made is
// never destroyed. The shutdown functions still run, and an object one of them
// makes is destroyed after them.
class Noisy
{
    public function __construct(public string $tag)
    {
    }

    public function __destruct()
    {
        echo "destruct {$this->tag}\n";
        if ($this->tag === 'third') {
            $GLOBALS['quiet'] = new Noisy('made by third');
        }
        if ($this->tag === 'first') {
            $GLOBALS['loud'] = new Noisy('made by first');
        }
        if ($this->tag === 'made by first') {
            trigger_error('made by first gave up', E_USER_ERROR);
        }
    }
}

$first = new Noisy('first');
$second = new Noisy('second');
$third = new Noisy('third');
register_shutdown_function(function () {
    echo "shutdown\n";
    $GLOBALS['late'] = new Noisy('made by shutdown');
});
echo "body done\n";
