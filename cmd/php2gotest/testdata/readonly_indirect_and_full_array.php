<?php
// Two refusals that only fire at the edge of a legal operation: reaching a
// readonly property as storage, and appending to an array with no next key.

class Holder {
    public function __construct(
        public readonly array $items,
        public readonly object $inner,
    ) {}

    public function push(): void {
        // Refused **inside the declaring class** too -- an indirect write is
        // not the one initialising assignment readonly allows.
        try { $this->items[] = "inside"; } catch (Error $e) { echo "inside: ", $e->getMessage(), "\n"; }
    }
}

$h = new Holder([1, 2], new stdClass);
try { $h->items[] = 3; } catch (Error $e) { echo "append: ", $e->getMessage(), "\n"; }
try { $h->items[0] = 9; } catch (Error $e) { echo "offset: ", $e->getMessage(), "\n"; }
try { $r = &$h->items; } catch (Error $e) { echo "bind:   ", $e->getMessage(), "\n"; }
$h->push();
var_dump($h->items);

// The object a readonly property *holds* is not itself readonly: writing
// through the handle is an ordinary write.
$h->inner->p = 1;
var_dump($h->inner->p);

class Uninit { public readonly array $never; }
$u = new Uninit;
// No exception for "not yet written": indirect modification is refused in
// every state, which is what separates it from the direct write.
try { $u->never[] = 1; } catch (Error $e) { echo "uninit: ", $e->getMessage(), "\n"; }

class Hidden {
    private readonly array $v;
    public function __construct() { $this->v = [1]; }
}
$hid = new Hidden;
// Visibility is asked **first**, so this is an access failure and not a
// readonly one.
try { $hid->v[] = 2; } catch (Error $e) { echo "private: ", $e->getMessage(), "\n"; }

// There is not always a next key. Once the largest integer key is taken the
// engine refuses rather than wrapping, and the element is not added.
$big = [PHP_INT_MAX => "last"];
try { $big[] = "next"; } catch (Error $e) { echo "full:   ", $e->getMessage(), "\n"; }
var_dump(count($big));

$grown = [];
$grown[PHP_INT_MAX] = "last";
try { $grown[] = "next"; } catch (Error $e) { echo "grown:  ", $e->getMessage(), "\n"; }
var_dump(count($grown));

// A smaller largest key still has a next one.
$ok = [5 => "five"];
$ok[] = "six";
var_dump($ok);

// Every path that appends asks the same question, and there are four.
$lit = PHP_INT_MAX;
try { var_dump([$lit => 42, new stdClass]); } catch (Error $e) { echo "literal:  ", $e->getMessage(), "\n"; }
try { var_dump([PHP_INT_MAX - 1 => 0, ...[1, 2, 3]]); } catch (Error $e) { echo "unpack:   ", $e->getMessage(), "\n"; }
function gen() { yield 1; yield 2; }
try { var_dump([PHP_INT_MAX => 0, ...gen()]); } catch (Error $e) { echo "iterator: ", $e->getMessage(), "\n"; }

// A string key does not append, so it never asks.
var_dump([PHP_INT_MAX => 0, ...["k" => 1]]);

// The **source**'s integer keys renumber, which is a different question from
// whether the destination has a next key.
var_dump([...[PHP_INT_MAX => "m"], ...[1]]);

function val() { return 42; }
$var = 24;
$sat = [PHP_INT_MAX => "foo"];
// A target that refuses takes precedence over the `Only variables` notice: the
// notice belongs to the bind, and the bind never happens.
try { var_dump($sat[] = &$var); } catch (Error $e) { echo "bindvar:  ", $e->getMessage(), "\n"; }
var_dump(count($sat));
try { var_dump($sat[] = &val()); } catch (Error $e) { echo "bindval:  ", $e->getMessage(), "\n"; }
var_dump(count($sat));

// With room, the notice is still spoken and the binding degrades to a copy.
$room = [];
$room[] = &val();
var_dump($room);
