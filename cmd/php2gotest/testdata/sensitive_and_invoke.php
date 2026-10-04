<?php
// `SensitiveParameterValue` shows **nothing** under var_dump even though its
// declared `$value` really holds the argument -- that is the whole point of the
// class, and `getValue()` is the only way through.
$secret = new SensitiveParameterValue('hunter2');
var_dump($secret);
var_dump($secret->getValue());

// `ReflectionMethod::invoke` takes the receiver as its **first** argument and
// the method's own arguments after it; `invokeArgs` takes them as an array. A
// static method takes null for the receiver.
class Calc
{
    public function add($a, $b)
    {
        return $a + $b;
    }

    public static function double($x)
    {
        return $x * 2;
    }

    private function hidden()
    {
        return 'private';
    }
}

$add = new ReflectionMethod('Calc', 'add');
var_dump($add->invoke(new Calc(), 2, 3));
var_dump($add->invokeArgs(new Calc(), [4, 5]));

$double = new ReflectionMethod('Calc', 'double');
var_dump($double->invoke(null, 7));

var_dump($add->getName(), $add->class, $add->getDeclaringClass()->getName());
