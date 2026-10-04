<?php
// var_export renders a value as PHP source: the output has to parse back to
// the same value, which is what separates it from var_dump. The indentation is
// the engine's and is graded byte for byte.
var_export(null); echo "\n";
var_export(true); echo "\n";
var_export(false); echo "\n";
var_export(0); echo "\n";
var_export(-12); echo "\n";
var_export(2.0); echo "\n";
var_export(0.1); echo "\n";
var_export(1e100); echo "\n";
var_export(INF); echo "\n";
var_export(-INF); echo "\n";
var_export(NAN); echo "\n";
var_export("plain"); echo "\n";
var_export("it's \\ a \"quote\""); echo "\n";
var_export("nul\0byte"); echo "\n";
var_export([]); echo "\n";
var_export([1, 2, 3]); echo "\n";
var_export(["a" => 1, 5 => [2, ["deep" => true]]]); echo "\n";

$o = new stdClass;
$o->a = 1;
$o->nested = new stdClass;
$o->nested->b = [1, 2];
var_export($o); echo "\n";

class Holder { public $pub = 1; protected $prot = 2; private $priv = 3; }
var_export(new Holder); echo "\n";
var_export([new Holder]); echo "\n";

enum Suit: string { case Hearts = 'H'; case Spades = 'S'; }
var_export(Suit::Hearts); echo "\n";
var_export([Suit::Spades]); echo "\n";

// The second argument hands the text back instead of printing it.
$text = var_export([1, "two"], true);
var_dump($text);

echo "--- debug_zval_dump ---\n";
debug_zval_dump(1, "s", [1, 2], null, 2.5, true);
// An *object* is not dumped here: it would print its handle number, and this
// file declares an enum -- whose cases the generated program builds at start-up
// where the engine builds them all at once on first access. See TODO.md.
