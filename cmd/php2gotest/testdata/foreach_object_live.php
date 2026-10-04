<?php
// A by-value foreach over a plain object walks its live property table: a
// value is read when the loop reaches it (bug71266), a property unset
// before it is reached is skipped, and one the body adds is reached after
// the rest. From inside the class the private and protected ones are in it.

$obj = (object) ['foo' => 1, 'bar' => 2, 'baz' => 3];
foreach ($obj as $k => $v) {
    echo "$k => $v\n";
    if ($k === 'foo') {
        $obj->bar = 42;
        unset($obj->baz);
        $obj->added = 'new';
    }
}

class C {
    public $pub = 'p';
    protected $prot = 'q';
    private $priv = 'r';
    function walk() {
        foreach ($this as $k => $v) {
            echo "$k => $v\n";
            if ($k === 'pub') {
                $this->priv = 'changed';
            }
        }
    }
}
(new C)->walk();
foreach (new C as $k => $v) {
    echo "outside $k => $v\n";
}

$arr = (object) ['list' => [1, 2]];
foreach ($arr as $k => $v) {
    $v[] = 3;
    var_dump($v, $arr->list);
}
