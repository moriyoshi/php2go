<?php
// `private(set) readonly` narrows who may initialize and refuses in the
// asymmetric words; plain and `protected(set)` readonly refuse outsiders as
// readonly; `public(set) readonly` may be initialized from anywhere, once.

class P
{
    public readonly int $plain;
    public private(set) readonly int $priv;
    public protected(set) readonly int $prot;
    public public(set) readonly int $pub;
}
class C extends P
{
    public function init()
    {
        foreach (['plain', 'priv', 'prot', 'pub'] as $n) {
            try {
                $this->$n = 1;
                echo "C set $n\n";
            } catch (Error $e) {
                echo $e->getMessage(), "\n";
            }
        }
    }
}
(new C)->init();
$p = new P;
foreach (['plain', 'priv', 'prot', 'pub', 'pub'] as $n) {
    try {
        $p->$n = 2;
        echo "outside set $n\n";
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
