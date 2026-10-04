<?php
// `$o->p op= v` and `$o->p++` take the site cache's hit inline (gogen place.get
// and store): the read from the object's slot, the write back to the same
// slot. Each case here is one the inline path has to decline, or where the
// object changes between the read and the write.

class Counter
{
    public $n = 0;
    public int $typed = PHP_INT_MAX - 1;
    public $s = "a";

    public function bump($times)
    {
        for ($i = 0; $i < $times; $i++) {
            $this->n++;
        }
        return $this->n;
    }
}

class Wide extends Counter
{
    public $extra = 1;
}

// Two classes through one site: the cache switches layout.
$objs = [new Counter(), new Wide(), new Counter(), new Wide()];
foreach ($objs as $o) {
    $o->bump(3);
}
foreach ($objs as $o) {
    $o->n += 10;
    echo get_class($o), " ", $o->n, "\n";
}

// A reference bound to the property: every step goes through the box.
$c = new Counter();
$r = &$c->n;
$c->bump(2);
$r += 5;
$c->n++;
var_dump($r, $c->n);

// Unset mid-way: the slot is gone, the read warns and the write recreates it.
$u = new Counter();
$u->bump(1);
unset($u->n);
$u->n++;
var_dump($u->n);

// A typed property steps past PHP_INT_MAX: the engine refuses the float.
$t = new Counter();
try {
    for ($k = 0; $k < 3; $k++) {
        $t->typed++;
        var_dump($t->typed);
    }
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}

// The right-hand side demotes the object between the read and the write.
class Demoter
{
    public function __construct(public $target)
    {
    }

    public function __toString(): string
    {
        unset($this->target->extra);
        return "z";
    }
}
$w = new Wide();
$w->s .= new Demoter($w);
$w->s .= "!";
var_dump($w->s, property_exists($w, "extra"), get_object_vars($w));

// Demoted for real: wrapping the object in an ArrayObject takes its
// properties as a live table, between the read and the write-back.
class Wrapper
{
    public function __construct(public $target)
    {
    }

    public function __toString(): string
    {
        $GLOBALS['wrapped'] = new ArrayObject($this->target);
        return "w";
    }
}
$d = new Wide();
$d->s .= new Wrapper($d);
$d->s .= "?";
$d->n++;
var_dump($d->s, $d->n, count($GLOBALS['wrapped']));
