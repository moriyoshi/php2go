<?php
class D {
    public $real = 1;
    private $hidden = "nope";
    public function __debugInfo() {
        return [7 => "seven", "k" => "vee"];
    }
}
var_dump(new D());
