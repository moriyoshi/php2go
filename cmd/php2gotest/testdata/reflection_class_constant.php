<?php
// getReflectionConstant() answers a ReflectionClassConstant: its `name` and
// `class`, its value, visibility and attributes. The declaring class is where
// the constant was written -- an ancestor or an interface for an inherited
// one -- except that a trait's constant belongs to the class composing it,
// because the engine copies it in (traits/constant_019). An absent name is
// false, not an exception; the constructor throws instead.

#[Attribute(Attribute::TARGET_ALL | Attribute::IS_REPEATABLE)]
class Tag {
    function __construct(public $v) {}
}

interface I {
    const FROM_I = "i";
}

trait T {
    #[Tag(v: 123), Tag(self::class)]
    public const FROM_T = 42;
}

class P implements I {
    #[Tag("p")]
    protected const FROM_P = [1, 2];
}

class C extends P {
    use T;
    #[Tag(self::class), Tag(self::OWN)]
    private const OWN = "own";
    final public const FIN = 1.5;
}

enum E {
    case One;
    const ALIAS = self::One;
}

$r = new ReflectionClass(C::class);
foreach (["OWN", "FIN", "FROM_P", "FROM_T", "FROM_I"] as $n) {
    $c = $r->getReflectionConstant($n);
    print_r($c);
    var_dump($c->getName(), $c->name, $c->class, $c->getValue());
    var_dump($c->getDeclaringClass()->getName());
    var_dump($c->isPublic(), $c->isProtected(), $c->isPrivate(), $c->isFinal(), $c->isEnumCase());
    foreach ($c->getAttributes() as $a) {
        var_dump($a->getName(), $a->getArguments(), $a->newInstance()->v);
    }
    var_dump(count($c->getAttributes("Nope")));
}
var_dump($r->getReflectionConstant("nope"));

$t = new ReflectionClass(T::class);
var_dump($t->getReflectionConstant("FROM_T")->getDeclaringClass()->getName());
var_dump($t->getReflectionConstant("FROM_T")->getAttributes()[0]->getArguments());

$e = new ReflectionClass(E::class);
var_dump($e->getReflectionConstant("One")->isEnumCase(), $e->getReflectionConstant("ALIAS")->isEnumCase());
var_dump($e->getReflectionConstant("ALIAS")->getValue());

$k = new ReflectionClassConstant(C::class, "FROM_P");
var_dump($k->getValue(), $k->class);
$k = new ReflectionClassConstant(new C, "FIN");
var_dump($k->getValue());
try {
    new ReflectionClassConstant(C::class, "nope");
} catch (ReflectionException $x) {
    echo $x->getMessage(), "\n";
}
try {
    new ReflectionClassConstant("Nope", "X");
} catch (ReflectionException $x) {
    echo $x->getMessage(), "\n";
}

// An attribute's target check names a class constant as such.
#[Attribute(Attribute::TARGET_CLASS)]
class OnlyClass {}
class U {
    #[OnlyClass]
    const X = 1;
}
$a = (new ReflectionClass(U::class))->getReflectionConstant("X")->getAttributes()[0];
var_dump($a->getTarget() === Attribute::TARGET_CLASS_CONSTANT);
try {
    $a->newInstance();
} catch (Error $x) {
    echo $x->getMessage(), "\n";
}

// An ancestor's private constant is not the subclass's.
class PP { private const HIDDEN = 1; }
class CC extends PP {}
var_dump((new ReflectionClass(CC::class))->getReflectionConstant("HIDDEN"));
var_dump((new ReflectionClass(PP::class))->getReflectionConstant("HIDDEN")->class);
