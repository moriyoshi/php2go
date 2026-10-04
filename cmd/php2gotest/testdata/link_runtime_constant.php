<?php
// Composing two traits declaring one property compares the defaults, which
// name a constant the script defines when it runs. A link tried before
// anything runs cannot evaluate it -- an exception, not a verdict -- so the
// class declares at run time as usual (traits/bug74922).

const VALUE = true;

trait Foo { public $var = VALUE; }
trait Bar { public $var = VALUE; }
class Baz { use Foo, Bar; }

var_dump((new Baz)->var);
