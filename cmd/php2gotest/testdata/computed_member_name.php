<?php
// A computed member name is converted as PHP converts a string: an object
// without __toString is an Error, one with it names the member, an array
// warns and names "Array", an int names its digits.

class Named { function __toString(): string { return "fromObject"; } }
#[AllowDynamicProperties]
class Target { public $fromObject = "via __toString"; public $Array = "via array"; }

$t = new Target;
var_dump($t->{new Named});
var_dump($t->{[]});
$t->{1} = "one";
var_dump($t->{1});
foreach ([function () {}, new class {}] as $bad) {
    try {
        var_dump($t->{$bad});
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
    try {
        $t->{$bad} = 1;
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
$t->{new Named} = "written";
var_dump($t->fromObject);
