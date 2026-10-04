<?php
// The class-table questions a compiled program answers itself -- the
// `*_exists` family, `method_exists`, the collector switch and a closure's
// rebound scope -- where each answer is a fact about the engine or about which
// declarations have run.

// Engine classes exist whether php2go models them or not: `stdClass` is built
// in and was answered false.
var_dump(class_exists("stdClass"), class_exists("stdclass", false));
var_dump(class_exists("Exception"), class_exists("Closure"), class_exists("Generator"));
var_dump(class_exists("Countable"), interface_exists("Countable"));
var_dump(class_exists("NoSuchClassAnywhere", false));

// A type declared inside an autoloader does not exist until the autoloader
// has run, and declaring an enum there does not ask the autoloader for
// `UnitEnum` -- the engine's interfaces are never autoloaded.
spl_autoload_register(function ($name) {
    echo "autoload $name\n";
    if ($name === 'LateEnum') {
        enum LateEnum {}
    }
    if ($name === 'LateTrait') {
        trait LateTrait {}
    }
    if ($name === 'LateInterface') {
        interface LateInterface {}
    }
    if ($name === 'LateBacked') {
        enum LateBacked: string implements JsonSerializable {
            case A = "a";
            public function jsonSerialize(): mixed { return 1; }
        }
    }
});
var_dump(enum_exists('LateEnum', false), enum_exists('LateEnum', true), enum_exists('LateEnum', false));
var_dump(trait_exists('LateTrait', false), trait_exists('LateTrait'), trait_exists('LateTrait', false));
var_dump(interface_exists('LateInterface', false), interface_exists('LateInterface'));
var_dump(enum_exists('LateBacked'), LateBacked::A instanceof BackedEnum);

// `method_exists` on an ancestor's private method: true for an object, false
// for a class name, static or not and from any scope.
class Base
{
    private function hidden() {}
    private static function hiddenStatic() {}
    protected static function shared() {}
}
class Child extends Base
{
    function fromInside()
    {
        return [method_exists('Child', 'hidden'), method_exists($this, 'hidden')];
    }
}
class GrandChild extends Child {}
var_dump(method_exists('Child', 'hidden'), method_exists('Child', 'hiddenStatic'), method_exists('Child', 'shared'));
var_dump(method_exists(new Child, 'hidden'), method_exists(new Child, 'hiddenStatic'));
var_dump(method_exists('GrandChild', 'hidden'), method_exists(new GrandChild, 'hiddenStatic'));
var_dump((new Child)->fromInside());
var_dump(method_exists('Base', 'hidden'), method_exists('Base', 'HIDDEN'));

// The collector switch is the `zend.enable_gc` setting, whichever spelling
// changes it.
ini_set('zend.enable_gc', '0');
var_dump(gc_enabled(), ini_get('zend.enable_gc'));
gc_enable();
var_dump(gc_enabled(), ini_get('zend.enable_gc'));
gc_disable();
var_dump(gc_enabled(), ini_get('zend.enable_gc'));
ini_set('zend.enable_gc', '1');
var_dump(gc_enabled());

// A null scope is no scope; "static" keeps the one the closure had.
class Holder
{
    private static $secret = 7;
    static function reader()
    {
        return static function () { return isset(Holder::$secret); };
    }
}
$reader = Holder::reader();
var_dump($reader());
var_dump(($reader->bindTo(null, null))());
var_dump(($reader->bindTo(null, 'static'))());
var_dump(($reader->bindTo(null))());
var_dump((Closure::bind($reader, null, null))());
var_dump((Closure::bind($reader, null, 'Holder'))());
