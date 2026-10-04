<?php
// The reflectors' own __toString is not modelled and is refused; everything
// around it must still convert as PHP does.
function f(int $a): ?string { return null; }

// A subclass's own __toString wins over the reflector's.
class R extends ReflectionClass {
    public function __toString(): string { return "mine: " . $this->getName(); }
}
echo new R('stdClass'), "\n";

// A reflected type converts to its spelling.
$fn = new ReflectionFunction('f');
echo $fn->getReturnType(), " ", $fn->getParameters()[0]->getType(), "\n";

// An object without __toString is still PHP's Error.
try {
    echo new stdClass;
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
