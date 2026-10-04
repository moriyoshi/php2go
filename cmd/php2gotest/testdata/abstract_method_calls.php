<?php
abstract class T {
    abstract static function Meth();
    abstract function inst();
}
// The message quotes the DECLARATION's spelling, where the visibility refusals
// quote the call site's.
try { T::METH(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
abstract class U extends T {}
try { U::Meth(); } catch (Error $e) { echo $e->getMessage(), "\n"; }

class V extends T {
    static function Meth() { echo "V::Meth\n"; }
    function inst() {}
    public function go() { parent::inst(); }
}
V::meth();
try { (new V)->go(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { (new V)->nope(); } catch (Error $e) { echo $e->getMessage(), "\n"; }

interface Tramp { public static function __callStatic($m, $a); }
try { Tramp::bar(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
interface Tramp2 { public function __call($m, $a); }
abstract class W implements Tramp2 {}
echo "done\n";
