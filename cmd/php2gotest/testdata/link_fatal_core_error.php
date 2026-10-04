<?php
// A class that extends one implementing Traversable directly must implement
// Iterator or IteratorAggregate. The engine raises that as an E_CORE_ERROR,
// which names no location at all -- `in Unknown on line 0` even though a
// declaration is executing, where other link errors name its line
// (bug62609).

echo "before\n";
abstract class AbstractTraversable implements Traversable {}
class NonAbstractTraversable extends AbstractTraversable {}
echo "never\n";
