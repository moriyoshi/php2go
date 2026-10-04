<?php
// A lazy proxy and the real instance it hands off to share ONE magic guard:
// `Object.Guard`, not `Object.ID`. Keying the in-flight set on the handle makes
// the accessor fire twice -- once for the shell, once for the instance -- which
// is what GH-18038 is about. The availability question ("would an accessor
// answer for this name right now?") has to consult the same guard, or a lazy
// object dispatches magic that is already running.
#[AllowDynamicProperties]
class RealInstance {
    public function __set($name, $value) {
        echo get_class($this), "::__set($name)\n";
        // Re-entering __set for the *same* name must fall through to a plain
        // dynamic property rather than recurse.
        $this->$name = $value * 2;
    }
    public function __get($name) {
        echo get_class($this), "::__get($name)\n";
        return $this->$name ?? "absent";
    }
}

#[AllowDynamicProperties]
class Proxy extends RealInstance {}

$rc = new ReflectionClass(Proxy::class);
$obj = $rc->newLazyProxy(function () { echo "init\n"; return new RealInstance; });

$real = $rc->initializeLazyObject($obj);
$real->prop = 1;
var_dump($obj->prop);

// Reaching the proxy first must give the same single dispatch.
$obj2 = $rc->newLazyProxy(function () { return new RealInstance; });
$obj2->other = 5;
var_dump($obj2->other);
var_dump($obj2->missing);

// A plain (non-lazy) object still guards on its own handle.
$plain = new RealInstance;
$plain->z = 3;
var_dump($plain->z);
var_dump($plain === $plain);
