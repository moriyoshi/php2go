<?php
class PlainCycle { public $next; public $tail; }

$a = new PlainCycle;
$b = new PlainCycle;
$a->next = $b;
$b->next = $a;
$b->tail = new PlainCycle;
unset($a, $b);
echo gc_collect_cycles(), "\n";
echo gc_collect_cycles(), "\n";

$live = new PlainCycle;
$live->next = $live;
echo gc_collect_cycles(), "\n";
unset($live);
echo gc_collect_cycles(), "\n";
