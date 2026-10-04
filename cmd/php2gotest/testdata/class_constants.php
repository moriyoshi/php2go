<?php
class B { const N = 1; const S = "b"; protected const P = 2;
          public function reachP() { return self::P; } }
class D extends B { const N = 10; }
interface I { const IC = 7; }
class E implements I {}
echo B::N, D::N, D::S, "\n";
echo E::IC, "\n";
$b = new B();
echo $b->reachP(), "\n";
try { echo B::NOPE; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { echo B::P; } catch (Error $e) { echo $e->getMessage(), "\n"; }
class Z {}
try { echo Z::$nope; } catch (Error $e) { echo $e->getMessage(), "\n"; }
