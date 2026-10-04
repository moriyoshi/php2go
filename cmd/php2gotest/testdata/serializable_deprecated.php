<?php
// Implementing Serializable without both __serialize and __unserialize is
// deprecated where the class is declared: directly, through an interface or
// a parent. Abstract classes and interfaces are exempt, and a class with the
// magic pair -- declared or inherited -- is not warned about.

interface I extends Serializable {}
abstract class A implements Serializable {}

class Direct implements Serializable
{
    public function serialize() {}
    public function unserialize($data) {}
}
class ViaInterface extends A implements I
{
    public function serialize(): string { return ""; }
    public function unserialize(string $data) {}
}
class OnlyInterface implements I
{
    public function serialize(): string { return ""; }
    public function unserialize(string $data) {}
}
class WithPair extends A implements I
{
    public function serialize(): string { return ""; }
    public function unserialize(string $data) {}
    public function __serialize(): array { return []; }
    public function __unserialize(array $data) {}
}
class InheritsPair extends WithPair {}
echo "done\n";
