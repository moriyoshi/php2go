<?php
// A class with no constructor drops positional arguments but refuses a
// named one -- through a dynamic `new` and through an attribute's
// newInstance() alike -- and the name does not leak into the next call.
// `#[Attribute(...)]`'s flags are Attribute::__construct(int $flags)'s
// argument, checked strictly: even a numeric string is refused.

class NoCtor {}
$c = 'NoCtor';
try { new $c(y: 2); echo "made\n"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
new $c(1, 2);
function f($a = 'default') { var_dump($a); }
f();

#[Attribute]
class Plain {}
#[Plain(named: 1)]
class UsesPlain {}
try { (new ReflectionClass(UsesPlain::class))->getAttributes()[0]->newInstance(); } catch (Error $e) { echo $e->getMessage(), "\n"; }

#[Attribute("foo")]
class BadFlags {}
#[BadFlags]
class UsesBad {}
try { (new ReflectionClass(UsesBad::class))->getAttributes()[0]->newInstance(); } catch (Error $e) { echo $e->getMessage(), "\n"; }

#[Attribute("1")]
class NumericFlags {}
#[NumericFlags]
class UsesNumeric {}
try { (new ReflectionClass(UsesNumeric::class))->getAttributes()[0]->newInstance(); } catch (Error $e) { echo $e->getMessage(), "\n"; }

#[Attribute(-1)]
class NegFlags {}
#[NegFlags]
class UsesNeg {}
#[Attribute(128)]
class HighFlags {}
#[HighFlags]
class UsesHigh {}
#[Attribute(Attribute::TARGET_CLASS | Attribute::IS_REPEATABLE)]
class OkFlags {}
#[OkFlags] #[OkFlags]
class UsesOk {}
foreach (['UsesNeg', 'UsesHigh', 'UsesOk'] as $c) {
    try { var_dump(get_class((new ReflectionClass($c))->getAttributes()[0]->newInstance())); } catch (Error $e) { echo $e->getMessage(), "\n"; }
}
