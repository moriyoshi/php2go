<?php
// A __toString that throws inside a sort comparison: the object is the greater
// side whichever operand it is, and no further __toString runs once the
// exception is pending (zend_std_compare_objects cast failure; probed).
class Boom { public $n; function __construct($n){$this->n=$n;} function __toString(): string { echo "ts{$this->n} "; throw new Exception('t'); } }
foreach ([['x'=>'b','y'=>new Boom(1),'z'=>'a'], ['y'=>new Boom(1),'x'=>'b','z'=>'a'], ['x'=>'b','z'=>'a','y'=>new Boom(1)], ['a'=>'d','b'=>'c','y'=>new Boom(1),'c'=>'b','d'=>'a']] as $arr) {
  try { asort($arr); } catch (Exception $e) { echo "caught "; }
  echo implode(',', array_keys($arr)), "\n";
}
