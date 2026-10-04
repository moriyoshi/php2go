<?php
// An attribute argument that is a constant expression is evaluated when the
// attribute is read -- getArguments(), newInstance() -- so a constant
// declared after the class is seen, and one that never exists is an Error
// there. Unevaluated it used to drop the whole attribute list.

#[Attribute]
class Tag { function __construct(public $v, public $w = null) {} }
#[Attribute]
class Loud { function __construct($v) { echo "Loud constructed\n"; } }

class C {
    #[Tag(1, LATER)]
    public $x, $y;
    #[Tag(self::K * 2, w: LATER . "!")]
    public function m() {}
    #[Tag(MISSING)]
    public $z;
    #[Loud(MISSING)]
    public $q;
    const K = 21;
}

const LATER = "late";

var_dump((new ReflectionProperty('C', 'x'))->getAttributes()[0]->getArguments());
var_dump(count((new ReflectionProperty('C', 'y'))->getAttributes()));
$m = (new ReflectionMethod('C', 'm'))->getAttributes()[0];
var_dump($m->getArguments(), $m->newInstance()->w);
$z = (new ReflectionProperty('C', 'z'))->getAttributes()[0];
var_dump($z->getName());
try { $z->getArguments(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { (new ReflectionProperty('C', 'q'))->getAttributes()[0]->newInstance(); } catch (Error $e) { echo $e->getMessage(), "\n"; }

// The attribute class's own `#[Attribute(...)]` flags are evaluated in that
// class's scope -- `parent::` is its parent -- and a class the expression
// names that does not exist is that Error, catchable at newInstance().
class FlagBase { protected const F = Attribute::TARGET_CLASS; }
#[Attribute(parent::F)]
class Scoped extends FlagBase {}
#[Scoped]
class UsesScoped {}
var_dump(get_class((new ReflectionClass(UsesScoped::class))->getAttributes()[0]->newInstance()));

#[Attribute(Nope::BAR)]
class BadExpr {}
#[BadExpr]
class UsesBadExpr {}
try { (new ReflectionClass(UsesBadExpr::class))->getAttributes()[0]->newInstance(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
