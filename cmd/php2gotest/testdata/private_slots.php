<?php
// A private property is one slot **per declaring class**. The engine keys it
// `\0Owner\0name`, so a subclass declaring the same name gets its own slot
// rather than overwriting the ancestor's -- and `var_dump` labels each with the
// class that wrote it.
//
// Two facts the harvest does not hand over directly:
//   - a child's property table lists its inherited public and protected
//     properties and **not** an ancestor's private, which it cannot see by
//     name, so the ancestor chain has to be walked;
//   - the ancestor's slot comes **first**, because the engine hands out slots
//     in declaration order with the parent's leading, and that order shows in
//     every dump and foreach.

class Base
{
    private $hello = 'base';
    protected $shared = 'base-shared';
    public $open = 'base-open';

    public function baseSees()
    {
        return $this->hello;
    }
}

class Middle extends Base
{
    private $hello = 'middle';

    public function middleSees()
    {
        return $this->hello;
    }
}

class Leaf extends Middle
{
    private $hello = 'leaf';

    public function leafSees()
    {
        return $this->hello;
    }
}

$leaf = new Leaf;
var_dump($leaf);

// Each scope sees its own slot, and writing one leaves the others alone.
var_dump($leaf->baseSees(), $leaf->middleSees(), $leaf->leafSees());

// A child that redeclares nothing still carries the ancestor's slot.
//
// Held in variables rather than dumped inline: the engine reuses the handle of
// a temporary that has died and php2go's are still monotonic, so an inline
// `new` differs on `#N` for a reason this fixture is not about.
class Quiet extends Base {}
$quiet = new Quiet;
var_dump($quiet);
var_dump($quiet->baseSees());

// Protected and public are shared, not split: redeclaring keeps one slot.
class Sharer extends Base
{
    protected $shared = 'sharer-shared';
    public $open = 'sharer-open';
}
$sharer = new Sharer;
var_dump($sharer);

// The storage key must not escape into anything the program can see. A
// shadowed private is held under its declaring class, and `foreach` yields the
// name as PHP spells it -- the visibility question is asked of the storage key,
// which carries the declaring class, while the key handed to the loop is the
// plain one. Using one spelling for both leaked `\0B\0c` as a loop key.
class Outer
{
    private $c = 'outer';
    public $open = 'shared';
}

class Inner extends Outer
{
    private $c = 'inner';

    public function walk()
    {
        foreach ($this as $k => $v) {
            echo 'in ', $k, '=', $v, "\n";
        }
    }
}

$inner = new Inner;
$inner->walk();

// From outside, neither private is visible and only the public remains.
foreach ($inner as $k => $v) {
    echo 'out ', $k, '=', $v, "\n";
}
