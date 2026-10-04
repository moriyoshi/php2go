<?php
namespace App;

use Fiber;

// Suspension has no opcode of its own, so the lowering has to recognise the
// call. Both spellings resolve to the literal "Fiber": a class name inside a
// namespace does not fall back to the global one, so the compiler has already
// applied the `use` by the time this reaches an opline.
function inside($v): string
{
    $got = Fiber::suspend($v);
    $g2 = \Fiber::suspend($v);
    return $got . $g2;
}

// The driver side. These are method calls on a value whose class the lowering
// cannot know, so they stay calls until type inference supplies the receiver.
function driver($fn): array
{
    $f = new Fiber($fn);
    $a = $f->start(1);
    return [$a, $f->getReturn(), Fiber::getCurrent()];
}
