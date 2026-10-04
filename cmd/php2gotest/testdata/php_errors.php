<?php
class C {}
try { $s = 5; $s[0] = "x"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { $c = new C(); $c->nope(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { $n = 5; $n->m(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { nope_fn(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
echo "still running\n";
