<?php
// Everything the engine keeps as an AST and evaluates later: class constants,
// property and static-property defaults, and default parameters. They reach the
// same renderer, so each is a second path to value semantics that the ordinary
// emitter already has -- which is where a *silently wrong* answer hides.

const BASE = 10;
const ARR = [1, 2, "k" => "v", BASE => "keyed"];
class Holder {
    const INNER = ARR["k"];
    const NESTED = [ARR[0], ARR["k"], self::INNER];
    const COND = BASE > 5 ? "big" : "small";
    const ELVIS = "" ?: "fallback";
    const COALESCE = null ?? "defaulted";
    const SPREAD = [...ARR, "extra"];
    const NEG = -BASE;
    const CHAIN = self::INNER . self::COND;
}
enum E: int { case A = 1; const FIRST = self::A; }
class Uses {
    const FROM_ENUM = E::A->value;
    const ENUM_NAME = E::A->name;
}
function defaults(
    $arr = ARR,
    $inner = Holder::INNER,
    $nested = Holder::NESTED,
    $cond = Holder::COND,
    $elvis = Holder::ELVIS,
    $coalesce = Holder::COALESCE,
    $spread = Holder::SPREAD,
    $neg = Holder::NEG,
    $chain = Holder::CHAIN,
    $fromEnum = Uses::FROM_ENUM,
    $enumName = Uses::ENUM_NAME,
) { var_dump($arr, $inner, $nested, $cond, $elvis, $coalesce, $spread, $neg, $chain, $fromEnum, $enumName); }
defaults();
echo "--- dim on a missing key\n";
class Missing { const M = ARR["nope"] ?? "absent"; }
var_dump(Missing::M);
echo "--- new in an initializer\n";
class Made { public function __construct(public int $n = 1) {} }
function withNew($o = new Made(5)) { var_dump($o->n); }
withNew();
echo "--- static property initializer\n";
class Statics { public static $s = BASE * 2; public static array $a = [BASE, BASE + 1]; }
var_dump(Statics::$s, Statics::$a);
echo "--- property initializer\n";
class Props { public $p = BASE . "!"; public array $q = [1 => BASE]; }
$pp = new Props; var_dump($pp->p, $pp->q);

// A static property's initializer is evaluated on **first use**, not at
// declaration: the constant may be declared after the class, or defined at run
// time, and an undefined one is an Error raised at the access with the program
// carrying on.
class Later { public static $v = DECLARED_AFTER * 2; }
const DECLARED_AFTER = 10;
var_dump(Later::$v);

class Runtime { public static $v = DEFINED_AT_RUNTIME; }
define("DEFINED_AT_RUNTIME", "defined later");
var_dump(Runtime::$v);

class Absent { public static $v = NEVER_DECLARED; }
try { var_dump(Absent::$v); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "still running\n";

// It runs once: a second read sees the value, not a second evaluation.
var_dump(Later::$v, Later::$v);
Later::$v = "assigned";
var_dump(Later::$v);
