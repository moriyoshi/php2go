<?php
// ArrayIterator and ArrayObject are engine classes: nothing is harvested to
// execute, so compiled output implements them itself out of the same
// `php.ArrayStore` the interpreter uses. What is checked here is that every
// way a program reaches one of them lands on that store -- construction,
// `foreach`, `count()`, `$o[$k]` in all four of its forms, `instanceof`
// against the engine's own interfaces, the aggregate hop through
// `getIterator()`, and a subclass reaching the engine body through `parent::`.
$it = new ArrayIterator(['a' => 1, 'b' => 2, 3]);
foreach ($it as $k => $v) {
    echo "$k => $v\n";
}
echo count($it), "\n";
var_dump($it['a'], isset($it['z']), isset($it['a']));
$it['c'] = 9;
$it[] = 10;
unset($it['b']);
var_dump($it->getArrayCopy());

// The engine's interface hierarchy, which no program declares: `Iterator
// extends Traversable` is the engine's own edge.
var_dump($it instanceof Traversable, $it instanceof ArrayAccess,
         $it instanceof Countable, $it instanceof Iterator,
         $it instanceof ArrayIterator, $it instanceof ArrayObject);

// An ArrayObject is an aggregate: it is not itself the iterator, and
// getIterator() hands back a fresh ArrayIterator over the same contents.
$ao = new ArrayObject([1, 2]);
var_dump($ao instanceof IteratorAggregate, $ao instanceof Iterator);
foreach ($ao as $v) {
    echo "ao $v\n";
}
$inner = $ao->getIterator();
var_dump(get_class($inner));
foreach ($inner as $k => $v) {
    echo "in $k=$v\n";
}

// var_dump names the storage as the private property the engine calls it;
// var_export writes the contents flat instead. The two disagree on purpose.
var_dump($ao);
print_r($ao);
var_export($ao);
echo "\n";

// Neither class is final. A subclass inherits the store, its own method wins
// over the engine's, and `parent::` reaches the one it overrode.
class Numbered extends ArrayIterator
{
    public $extra = 0;

    public function __construct($a)
    {
        parent::__construct($a);
        $this->extra = count($a);
    }

    public function current(): mixed
    {
        return '[' . parent::current() . ']';
    }
}

$n = new Numbered(['x' => 1, 'y' => 2]);
foreach ($n as $k => $v) {
    echo "n $k=$v\n";
}
var_dump($n->extra, $n['x'], count($n), get_parent_class($n),
         $n instanceof ArrayIterator, $n instanceof Traversable);

class Counted extends ArrayObject
{
    public function count(): int
    {
        return 99;
    }
}

var_dump(count(new Counted([5, 6, 7])), (new Counted([5, 6]))->getArrayCopy());

// SeekableIterator, whose refusal is an OutOfBoundsException a program can
// catch and which names the position it was given rather than the bound.
$s = new ArrayIterator([3, 1, 2]);
$s->seek(2);
var_dump($s->current(), $s->key(), $s instanceof SeekableIterator);
foreach ([9, -1] as $p) {
    try {
        $s->seek($p);
    } catch (Throwable $e) {
        var_dump(get_class($e), $e->getMessage());
    }
}

// An object argument becomes a **view** on its property table: writing through
// the ArrayObject reaches the object. `clone` shares the storage rather than
// copying it -- measured, not assumed.
$backing = new stdClass();
$backing->p = 1;
$view = new ArrayObject($backing);
$view['q'] = 2;
// `get_object_vars` rather than `var_dump($backing)`: the object's *handle*
// would differ here for an unrelated reason -- php2go does not reuse the handle
// of a temporary the engine has already freed -- and that gap is tracked
// separately.
var_dump(get_object_vars($backing), $view->getArrayCopy());

$orig = new ArrayIterator([1, 2]);
$copy = clone $orig;
$copy[] = 3;
var_dump($orig->getArrayCopy(), $copy->getArrayCopy());

// The constructor's declared type is `array|object`, and anything else is a
// TypeError naming the **engine** class even when a subclass is what was
// written. Last in the file on purpose: each refused `new` allocates a handle
// the engine then reuses and php2go does not, so an object number printed after
// this point would differ for that reason and not for this one.
class SubObject extends ArrayObject
{
}

foreach ([['ArrayIterator', 's'], ['ArrayObject', 42], ['ArrayIterator', null],
          ['ArrayObject', 1.5], ['ArrayIterator', true], ['SubObject', 'x']] as [$c, $v]) {
    try {
        new $c($v);
        echo "$c ok\n";
    } catch (Throwable $e) {
        echo get_class($e), ': ', $e->getMessage(), "\n";
    }
}
