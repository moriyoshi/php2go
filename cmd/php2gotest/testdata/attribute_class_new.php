<?php
// `Attribute` is a class like any other: `new Attribute($flags)` holds one
// public int, coerced as an internal parameter, and an `#[Attribute]`
// attribute reached through reflection instantiates it (gh10014).
var_dump(new Attribute);
var_dump(new Attribute(Attribute::TARGET_CLASS | Attribute::IS_REPEATABLE));
var_dump(new Attribute(flags: 2));
try { new Attribute("x"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { new Attribute(1, 2); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$a = new Attribute(4); $a->flags = 8; var_dump($a->flags);
#[Attribute] class Mine {} #[Mine] class U {}
print_r((new ReflectionClass("Mine"))->getAttributes()[0]->newInstance());
print_r(new Attribute(null));
var_dump(get_class(new Attribute), (new Attribute) instanceof Attribute);
