<?php
// `yield from` over an IteratorAggregate whose getIterator() is a generator
// delegates to that generator: its frame names the `yield from` line and the
// delegating generator's frame lies under it, with no Generator->next()
// between them.

class Agg implements IteratorAggregate {
    public function getIterator(): Generator {
        yield 1;
        throw new Exception("from the aggregate's generator");
    }
}

function outer() {
    yield from new Agg();
}

$g = outer();
var_dump($g->current());
try {
    $g->next();
} catch (Exception $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
}
foreach (outer() as $v) {
    var_dump($v);
    break;
}
