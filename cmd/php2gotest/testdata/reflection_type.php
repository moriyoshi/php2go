<?php
class C {}
class D {
    public int $a = 1;
    public ?string $n = null;
    public $u;
    public C $c;
    public self $s;
    public mixed $m = 1;
    public iterable $it = [];
    public float $f = 0.5;
}

foreach (["a", "n", "u", "c", "s", "m", "it", "f"] as $x) {
    $r = new ReflectionProperty("D", $x);
    var_dump($r->hasType());
    $t = $r->getType();
    if ($t === null) {
        echo "no type\n";
        continue;
    }
    printf("%s %s builtin=%d null=%d str=%s\n",
        get_class($t), $t->getName(), $t->isBuiltin(), $t->allowsNull(), (string)$t);
    var_dump($t instanceof ReflectionType, $t instanceof ReflectionNamedType);
}
var_dump(get_parent_class("ReflectionNamedType"));
try {
    new ReflectionType();
} catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
