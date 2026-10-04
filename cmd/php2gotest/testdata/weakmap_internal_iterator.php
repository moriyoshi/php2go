<?php
// WeakMap::getIterator() hands out the engine's InternalIterator: the same
// positional walk foreach does, steppable by hand (gh16371).
$m = new WeakMap; $a = new stdClass; $b = new stdClass; $c = new stdClass;
$m[$a] = 1; $m[$b] = 2; $m[$c] = 3;
$it = $m->getIterator();
var_dump(get_class($it), $it instanceof Iterator, $it instanceof Traversable);
var_dump($it->current(), $it->key() === $a, $it->valid());
$it->next(); var_dump($it->current());
$it->rewind(); var_dump($it->current());
foreach ($it as $k => $v) echo $v, " ";
echo "\n";
var_dump($it->valid(), $it->current(), $it->key());
$it2 = $m->getIterator();
$it2->next();
unset($m[$b]);
var_dump($it2->current());
var_dump(iterator_to_array($m->getIterator(), false));
$it3 = $m->getIterator();
foreach ($it3 as $v) { echo "x$v "; break; }
foreach ($it3 as $v) { echo "y$v "; }
echo "\n";
try { $x = clone $it3; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { serialize($it3); } catch (Exception $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump(method_exists($it3, 'rewind'), method_exists($m, 'getIterator'));
foreach (['WeakMap' => ['count', 'offsetGet', 'getIterator', '__construct'], 'WeakReference' => ['get', 'create', '__construct'], 'InternalIterator' => ['current', 'rewind', '__construct']] as $cls => $names) {
    foreach ($names as $n) echo "$cls::$n ", var_export(method_exists($cls, $n), true), "\n";
}
var_dump(method_exists($m, 'count'));
