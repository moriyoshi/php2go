<?php
// `__CLASS__` in a trait's property defaults -- instance and static -- names
// the class that composed the trait, through an intermediate trait too; and a
// static default naming `self::` resolves against the class owning it.

trait T
{
    public static $staticName = __CLASS__;
    public $name = __CLASS__;
}
trait Via { use T; }
class Direct { use T; }
class Indirect { use Via; }
class Consts
{
    const K = "k-value";
    public static $fromSelf = self::K . "!";
}

var_dump(Direct::$staticName, (new Direct)->name);
var_dump(Indirect::$staticName, (new Indirect)->name);
var_dump(Consts::$fromSelf);
