<?php
// A WeakMap describes itself to `var_dump`. Its keys are objects, which an
// array cannot hold, so the engine shows a numerically indexed list of
// `["key" => ..., "value" => ...]` pairs rather than properties.
//
// `php.WeakMapDebugInfo` has always produced exactly that, and the interpreter
// has always called it -- compiled output never did, so a populated WeakMap
// dumped as `object(WeakMap)#1 (0) {}` while `count()` and `isset()` on the
// very same map answered correctly. A gap that only one of three surfaces
// shows is the easiest kind to keep.
class V { public function __construct(public readonly string $v) {} }

$map = new WeakMap();
$a = new V('a');
$b = new V('b');
$map[$a] = $a;
$map[$b] = 'plain value';

var_dump(count($map));
var_dump(isset($map[$a]), isset($map[$b]));
var_dump($map);

// Removing an entry is reflected in the dump too.
unset($map[$b]);
var_dump(count($map));
var_dump($map);

// A WeakReference describes itself the same way and for the same reason: what
// it holds is not a property either.
$ref = WeakReference::create($a);
var_dump($ref);
var_dump($ref->get() === $a);

// An empty one, and an ordinary object beside it for contrast.
$empty = new WeakMap();
var_dump($empty);
var_dump(new V('plain'));
