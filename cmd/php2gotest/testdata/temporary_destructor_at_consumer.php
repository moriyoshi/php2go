<?php
// A temporary whose class has a destructor dies at the operation that
// consumes it -- an engine function's argument, a method's receiver, a
// property read's receiver, a clone's operand, a cast's operand -- and its
// destructor runs there, before the next statement. A destructor that
// throws throws there. Objects something else holds (a trace's arguments,
// a static a method stored `$this` in, a shared factory result) stay
// alive: the next object's number shows it.

class D {
    public $p = 'p';
    public function __construct(public string $name) {}
    public function __destruct() { echo "dtor {$this->name}\n"; }
    public function m() { echo "m {$this->name}\n"; return 1; }
    public function fail() { throw new Exception("fail {$this->name}"); }
    public static function make(string $name) { return new D($name); }
}

class T {
    public function __destruct() { throw new Exception("from dtor"); }
    public function m() { return 2; }
}

class Tr {
    public function __destruct() {}
}

class K {
    public static $kept;
    public function __destruct() { echo "dtor K\n"; }
    public function keep() { self::$kept = $this; return 3; }
}

class S {
    private static $one;
    public $p = 's';
    public static function get() { return self::$one ??= new S; }
    public function __destruct() {}
}

echo "-- engine function argument\n";
var_dump(new D('a'));
echo "after var_dump\n";
print_r([new D('b'), new D('c')]);
echo "\nafter print_r\n";

echo "-- method receiver\n";
echo (new D('r'))->m(), "\n";
echo "after method\n";

echo "-- receiver of a method that throws\n";
try {
    (new D('t'))->fail();
} catch (Exception $e) {
    echo "caught ", $e->getMessage(), "\n";
}
unset($e);

echo "-- property read\n";
echo (new D('q'))->p, "\n";
echo "after read\n";
echo D::make('made')->p, "\n";
echo "after factory read\n";

echo "-- clone and cast\n";
echo (clone new D('orig'))->name, "\n";
echo "after clone\n";
$arr = (array) new D('cast');
echo count($arr), "\n";

echo "-- destructor throws at the consumer\n";
try {
    var_dump(spl_object_id(new T) > 0);
    echo "not reached\n";
} catch (Exception $e) {
    echo "caught ", $e->getMessage(), "\n";
}
try {
    echo (new T)->m(), "\n";
    echo "not reached\n";
} catch (Exception $e) {
    echo "caught ", $e->getMessage(), "\n";
}
unset($e);
var_dump(spl_object_id(new stdClass));

echo "-- kept: a trace holds the argument\n";
try {
    str_repeat(new Tr, 2);
} catch (TypeError $e) {
    echo "caught TypeError\n";
}
var_dump(spl_object_id(new stdClass));

echo "-- kept: the method stores \$this\n";
echo (new K)->keep(), "\n";
var_dump(spl_object_id(new stdClass));

echo "-- kept: a shared factory result\n";
echo S::get()->p, "\n";
var_dump(spl_object_id(new stdClass));

echo "end\n";
