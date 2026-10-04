<?php
// A property of an undefined variable: incrementing, decrementing and
// compound-assigning it warn `Undefined variable` before the refusal, while
// a write fetch -- an append, a reference, a nested write -- and an unset say
// nothing (bug78531).
foreach ([1,2,3,4,5,6,7] as $i) {
    try {
        switch ($i) {
            case 1: $u1->a[] = 1; break;
            case 2: $r = &$u2->b; break;
            case 3: $u3->c++; break;
            case 4: --$u4->d; break;
            case 5: $u5->e .= "x"; break;
            case 6: unset($u6->f); break;
            case 7: $u7->g->h = 1; break;
        }
    } catch (Error $e) { echo $e->getMessage(), "\n"; }
}
$n = null;
try { $n->z++; } catch (Error $e) { echo $e->getMessage(), "\n"; }
$o = new stdClass;
$o->y++;
print_r($o);
