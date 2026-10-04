<?php
interface Nameable { public function name(): string; }
trait Loggable { public function log(string $m): void {} }

abstract class Base implements Nameable {
    protected int $id = 0;
    public readonly string $slug;
    private static array $registry = [];
    abstract public function name(): string;
}

final class Model extends Base {
    use Loggable;
    public ?string $title = null;
    public function name(): string { return $this->title ?? ''; }
    public function __get(string $k) { return null; }
    public function __call(string $m, array $a) { return null; }
}

enum Status: string {
    case Active = 'active';
    case Off = 'off';
}
