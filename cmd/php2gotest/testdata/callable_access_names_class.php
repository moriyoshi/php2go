<?php
// An inaccessible method callback is refused naming the class the callable
// named -- the object's class, or the class a `parent::` resolves to -- not
// the class that declared the method: the engine prints its calling scope
// (call_user_func_001). The `parent::` form is judged for visibility too,
// from the scope handing it over (bug48770_2).

abstract class Base {
    protected function prot() { return "Base::prot"; }
    private function priv() { return "Base::priv"; }
    public function viaParent($m) {
        return call_user_func([$this, "parent::$m"]);
    }
}
class Mid extends Base {
    private function own() { return "Mid::own"; }
    public function tryParent($m) {
        try {
            var_dump(call_user_func([$this, "parent::$m"]));
        } catch (TypeError $e) {
            echo $e->getMessage(), "\n";
        }
    }
}
class Leaf extends Mid {}

function attempt(callable|array|string $cb) {
    try {
        var_dump(call_user_func($cb));
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
    try {
        array_map($cb, [1]);
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}

attempt([new Leaf, "prot"]);
attempt([new Leaf, "priv"]);
attempt([new Mid, "own"]);
attempt([new Leaf, "own"]);
attempt("Leaf::prot");

(new Leaf)->tryParent("prot");
(new Leaf)->tryParent("priv");
(new Leaf)->tryParent("own");
var_dump(is_callable([new Leaf, "parent::own"]), is_callable([new Leaf, "prot"]));
// A class-qualified method named through a class string.
try {
    call_user_func(["Leaf", "Mid::prot"]);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
