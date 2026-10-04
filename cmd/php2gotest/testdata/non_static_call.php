<?php
class Foo { public function Bar() { echo "Bar on ", get_class($this), "\n"; } public static function s() { echo "s\n"; } }
// The declaration's spelling, and the declaring class.
try { Foo::BAR(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
class Sub extends Foo {}
try { Sub::Bar(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
Foo::s();
// Static syntax on an instance method is legal when $this is compatible.
class Kid extends Foo { public function go() { Foo::Bar(); parent::Bar(); } }
(new Kid)->go();
class Other { public function go() { Foo::Bar(); } }
try { (new Other)->go(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
// It beats __callStatic rather than falling through to it.
class M { public function m() {} public static function __callStatic($n, $a) { echo "cs($n)\n"; } }
try { M::m(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
M::nope();
echo "done\n";
