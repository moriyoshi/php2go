<?php
// Named arguments to a method only __call or __callStatic answers arrive in
// `$args` under their names, after the positional ones, and can be spread on.

class Magic
{
    public function __call($name, $args) { echo "$name: "; var_dump($args); }
    public static function __callStatic($name, $args) { echo "static $name: "; var_dump($args); }
}
$m = new Magic;
$m->go(1, b: 2, c: 3);
$m->only(x: "X");
Magic::stat(a: "A");
Magic::both(0, z: null);

class Forward
{
    public function __call($method, $args) { return $this->{'_' . $method}(...$args); }
    private function _pair($a = 'a', $b = 'b') { return "$a/$b"; }
}
var_dump((new Forward)->pair(b: 'B'), (new Forward)->pair('A', b: 'B'));
