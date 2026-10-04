<?php
// A generator made by a method names its class -- `S::g` in var_dump and
// `S->g()` on a trace taken inside it -- while a function's and a
// closure's keep their own names.

class S
{
    function g() { yield (new Exception)->getTrace()[0]['class'] ?? '-'; }
}
function f() { yield 3; }
$c = function () { yield 4; };
foreach ([(new S)->g(), f(), $c()] as $gen) {
    $info = print_r($gen, true);
    var_dump(trim(explode("\n", $info)[2]));
}
foreach ((new S)->g() as $v) {
    var_dump($v);
}
