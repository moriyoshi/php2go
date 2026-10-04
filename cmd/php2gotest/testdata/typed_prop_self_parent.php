<?php
// A property typed `self` or `parent` takes an instance of the declaring
// class or its parent, and refuses anything else in the declared spelling.
// In a trait `self` is the trait's user -- or the trait itself when it is
// reached directly -- and a trait has no parent.

class P {}
class A extends P {
    public self $i;
    public parent $pi;
    public ?self $ni = null;
    public static self $s;
    public static ?parent $ps = null;
}
class B extends A {}

$a = new A;
foreach (['i', 'pi', 'ni'] as $n) {
    try { $a->$n = new stdClass; echo "accepted $n\n"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
}
try { A::$s = new stdClass; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { A::$ps = new stdClass; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
$a->i = new B;
$a->pi = new A;
$a->ni = null;
A::$s = new B;
A::$ps = new P;
var_dump(get_class($a->i), get_class($a->pi), get_class(A::$s), get_class(A::$ps));

trait T {
    public static self $ts;
    public static parent $tp;
}
class Base {}
class User extends Base { use T; }
try { @T::$ts = new stdClass; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { @T::$tp = new Base; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
User::$ts = new User;
User::$tp = new Base;
try { User::$ts = new Base; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(get_class(User::$ts), get_class(User::$tp));
