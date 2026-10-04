<?php
// A write through `$GLOBALS[...]` is an assignment, so it goes through a
// reference the global holds -- and a parameter bound to that global by
// reference sees it. Plus two small questions about Closure and constant().

class Test { public int $x = 42; }

function typed(Test &$o)
{
    $GLOBALS['r'] = new stdClass;
    var_dump($o);
    $GLOBALS['r']->x = 3.141;
    var_dump(is_float($o->x));
}
$r = new Test;
typed($r);
var_dump($r);

function untyped(&$o)
{
    $GLOBALS['s'] = [1, 2];
    var_dump($o);
    $GLOBALS['s'][] = 3;
    var_dump($o);
}
$s = "before";
untyped($s);

// Rebinding through a reference taken to a global.
$g = 1;
$alias = &$g;
function rebind()
{
    $GLOBALS['g'] = "via globals";
}
rebind();
var_dump($alias);

// Closure's __invoke, asked of the class and of an instance.
var_dump(method_exists('Closure', '__invoke'), method_exists('closure', '__INVOKE'));
$f = function () { return "called"; };
var_dump(is_callable([$f, '__invoke']), call_user_func([$f, '__invoke']));

// `static::` in a constant name is late static binding.
class Base
{
    static function probe()
    {
        return [defined('static::K'), defined('self::K'), constant('static::NAME')];
    }
    const NAME = "base";
}
class Child extends Base
{
    const K = 1;
    const NAME = "child";
}
var_dump(Base::probe(), Child::probe());
