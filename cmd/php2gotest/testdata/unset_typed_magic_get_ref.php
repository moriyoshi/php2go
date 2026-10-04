<?php
// After unset(), a declared property is reached through __get again -- one
// merely never initialized is not -- and a by-reference __get standing in
// for a typed property has its box held to the type once: coerced, or
// refused as incompatible with the unset property. No constraint lasts.
class T { public $p = "abc"; public int $v; public int $w;
  public function &__get($n) { echo "__get $n\n"; return $this->p; } }
$t = new T; unset($t->v);
try { $r = &$t->v; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $r = &$t->w; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$t->p = "7"; $r = &$t->v; var_dump($r, $t->p); $r = "zz"; var_dump($t->p);
