<?php
$o = new stdClass;
$o->array = [$o];
unset($o);
echo gc_collect_cycles(), "\n";
echo gc_collect_cycles(), "\n";

$o = new stdClass;
$o->self = $o;
$o->array = [new stdClass];
unset($o);
echo gc_collect_cycles(), "\n";

$live = new stdClass;
$live->array = [$live];
echo gc_collect_cycles(), "\n";
unset($live);
echo gc_collect_cycles(), "\n";

$shared = [5];
$o = new stdClass;
$o->self = $o;
$o->array = $shared;
unset($o);
echo gc_collect_cycles(), "\n";
echo $shared[0], "\n";
