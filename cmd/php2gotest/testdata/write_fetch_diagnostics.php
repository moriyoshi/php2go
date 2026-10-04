<?php
// A **write** fetch reads its container without the undefined-variable
// warning, and refuses at the fetch that found the non-object rather than at
// the one after it. Two shapes that look alike refuse in different places:
// `$ee->p->q = 1` names the *outer* property and says `modify`, while
// `$h["x"]->p = 1` names the inner one and says `assign`.
echo "a: "; $a['x'] = 1; var_dump($a);
echo "b: "; $b['x']['y'] = 1; var_dump($b);
echo "c: "; try { $c->p = 1; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
echo "d: "; try { $d->p['x'] = 1; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
echo "e: "; try { $ee->p->q = 1; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }
echo "f: "; $f[] = 1; var_dump($f);
echo "g: "; $g['x'][] = 1; var_dump($g);
echo "h: "; try { $h['x']->p = 1; } catch (Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }

// A **read-write** fetch is not a write fetch: it reads the container before it
// writes, and that read keeps its diagnostics. `$k->p = 1` warns about nothing
// while `$k->p .= "s"` warns `Undefined variable $k`.
echo "k: ";
$k->p .= "s";
