<?php
// The date classes as opaque objects: made from a string the engine surely
// parses, extended, compared by identity, and asked only what does not read
// their time or zone.

$a = new DateTime();
$b = new DateTime("now");
$c = new DateTime("@86400");
$d = new DateTime("2020-02-29 23:59:59", new DateTimeZone("UTC"));
$e = new DateTimeImmutable("2021-01-01");
$z = new DateTimeZone("UTC");
$n = new DateTime("", null);

var_dump($a instanceof DateTimeInterface, $e instanceof DateTimeInterface, $z instanceof DateTimeInterface);
var_dump($e instanceof DateTime, get_class($e), get_class($z));
var_dump($a === $a, $a === $b, $a == new stdClass);
var_dump($c > 0);
var_dump(DateTimeInterface::ATOM, DateTime::RFC3339, DateTimeZone::UTC, DateTimeZone::ALL);

// Only dynamic properties are the object's own.
$c->note = "x";
var_dump(get_object_vars($c), isset($c->date));
foreach ($c as $k => $v) {
    echo "$k => $v\n";
}
$copy = clone $c;
var_dump($copy === $c, $copy->note);

try {
    echo "$a";
} catch (Error $err) {
    echo get_class($err), ": ", $err->getMessage(), "\n";
}

var_dump(method_exists($a, 'format'), method_exists('DateTimeZone', 'getName'), method_exists($a, 'nope'));
var_dump(is_callable([$a, 'format']), is_callable('DateTime::createFromFormat'), is_callable('DateTime::format'));
try {
    $a->nope();
} catch (Error $err) {
    echo $err->getMessage(), "\n";
}
try {
    DateTime::nope();
} catch (Error $err) {
    echo $err->getMessage(), "\n";
}

class Stamp extends DateTime
{
    public $label = "stamp";

    public function __construct()
    {
        parent::__construct("2020-01-01 10:00:00");
    }

    public function describe()
    {
        return $this->label;
    }
}

class Plain extends DateTime {}

class Shown extends DateTimeImmutable
{
    public function __debugInfo()
    {
        return ['shown' => true];
    }
}

$s = new Stamp();
var_dump($s->describe(), get_parent_class($s), $s instanceof DateTimeInterface);
$p = new Plain("2000-12-31");
var_dump(get_class($p), get_object_vars($p));
print_r(new Shown("@0"));
echo "\n";
var_dump(is_callable([$s, 'describe']), method_exists($s, 'getTimestamp'));

$anon = new class("1999-01-01") extends DateTime {};
try {
    serialize($anon);
} catch (Exception $err) {
    echo get_class($err), ": ", $err->getMessage(), "\n";
}

// A class carrying engine state cannot be made lazy; stdClass can.
class Failure extends Exception {}
class Store extends ArrayObject {}
class Bag extends stdClass { public $p = 1; }
foreach (['Plain', 'Stamp', 'Failure', 'Store', 'Bag', 'DateTime', 'stdClass'] as $cls) {
    foreach (['newLazyGhost', 'newLazyProxy'] as $how) {
        try {
            $o = (new ReflectionClass($cls))->$how(function ($o) {});
            echo "$cls $how: made\n";
        } catch (Error $err) {
            echo "$cls $how: ", get_class($err), ": ", $err->getMessage(), "\n";
        }
    }
}

// A subclass that serializes itself is serialized by its own methods.
class Kept extends DateTime
{
    public $tag = "t";

    public function __serialize(): array
    {
        return ['tag' => $this->tag];
    }

    public function __unserialize(array $data): void
    {
        $this->tag = $data['tag'] . "!";
    }
}
$kept = serialize(new Kept("@0"));
echo $kept, "\n";
$back = unserialize($kept);
var_dump(get_class($back), $back->tag);
