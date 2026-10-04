<?php
// Attacks on the parameter-escape summary. Each function below reaches its
// argument by a route the walk must refuse; if any is wrongly called
// non-retaining, the caller hands the handle back while the object is still
// live and the next `new` prints as the wrong object -- so every var_dump here
// is checking identity, not just a number.
class P { public $tag; function __construct($t) { $this->tag = $t; } }
class Holder { public $held; function keep($o) { $this->held = $o; } }

$box = new Holder();
$sink = [];

function viaStatic($o)  { static $s = null; $s = $o; return $s; }
function viaClosure($o) { return function () use ($o) { return $o->tag; }; }
function viaMethod($o)  { global $box; $box->keep($o); }
function viaReturn($o)  { return $o; }
function viaNested($o)  { return ["deep" => ["deeper" => $o]]; }
function viaProp($o)    { $h = new Holder(); $h->held = $o; return $h; }
function viaGlobalArr($o) { global $sink; $sink["k"] = $o; }
function viaThrow($o)    { throw new RuntimeException($o->tag); }

$a = viaStatic(new P("static"));
var_dump($a->tag);
$c = viaClosure(new P("closure"));
var_dump($c());
viaMethod(new P("method"));
var_dump($box->held->tag);
$r = viaReturn(new P("returned"));
var_dump($r->tag);
$n = viaNested(new P("nested"));
var_dump($n["deep"]["deeper"]->tag);
$p = viaProp(new P("prop"));
var_dump($p->held->tag);
viaGlobalArr(new P("global"));
var_dump($sink["k"]->tag);
try { viaThrow(new P("thrown")); } catch (RuntimeException $e) { var_dump($e->getMessage()); }

// Two locals naming one object: the second binding must stop the claim.
function twoLocals($o) { $q = $o; var_dump($q->tag); }
twoLocals(new P("two"));
var_dump(spl_object_id(new P("next")) > 0);

// A parameter reaching a method that does not store $this is still confined.
function reads($o) { var_dump($o->tag); }
reads(new P("read"));
var_dump($a->tag, $r->tag, $p->held->tag);
