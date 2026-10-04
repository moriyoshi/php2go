<?php
// Where a class constant is *found*. The harvest flattens neither the class
// chain nor the interface list, so the lookup has to walk up and sideways at
// every step -- and a trait composes its constants into the using class the way
// it composes methods, without the conflict check methods get.

interface Spec
{
    const string FROM_IFACE = 'iface';
}

trait Marker
{
    public const string TAG = 'tag';
    protected const string HIDDEN = 'hidden';
}

trait AlsoTag
{
    // The same name and the same value in a second trait is legal: PHP only
    // refuses a *disagreement*. A conflict check like the one methods get
    // would refuse this program.
    public const string TAG = 'tag';
}

class Base implements Spec
{
    use Marker;
    use AlsoTag;

    const string OWN = 'own';

    public function reachHidden(): string
    {
        return self::HIDDEN . '/' . static::HIDDEN;
    }
}

class Middle extends Base {}
class Leaf extends Middle
{
    public function viaParent(): string
    {
        return parent::OWN . '/' . self::TAG;
    }
}

// Reached on the declaring class, on a child, and on a grandchild.
echo Base::OWN, ' ', Middle::OWN, ' ', Leaf::OWN, "\n";
echo Base::TAG, ' ', Middle::TAG, ' ', Leaf::TAG, "\n";
echo Base::FROM_IFACE, ' ', Leaf::FROM_IFACE, "\n";
echo (new Base)->reachHidden(), "\n";
echo (new Leaf)->viaParent(), "\n";
echo (new Leaf)::OWN, "\n";

// A trait is in the class table so composition can find it, which is exactly
// why reaching it directly has to be refused.
try {
    echo Marker::TAG;
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    new Marker();
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
$t = 'Marker';
try {
    new $t();
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
