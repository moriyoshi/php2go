<?php
abstract class Shape {
    public $name = "shape";
    abstract public function area();
    public function describe() {
        return $this->name;
    }
}
class Square extends Shape {
    public $name = "square";
    public $side = 3;
    public function area() {
        return $this->side * $this->side;
    }
}
$s = new Square();
echo $s->describe(), " area=", $s->area(), " side=", $s->side, "\n";
