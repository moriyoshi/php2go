<?php
// The date constructors check their arguments as internal functions do
// (probed): DateTimeZone takes exactly one, DateTime and DateTimeImmutable at
// most two; an array or an object with no __toString is a TypeError for the
// string parameter, and anything but null or a DateTimeZone for the zone.
// `new DateTimeZone` with no argument is what instantiate_all_classes catches.
class MyDate extends DateTime {}
$cases = [
    'tz none' => fn() => new DateTimeZone(),
    'tz two' => fn() => new DateTimeZone('UTC', 1),
    'tz array' => fn() => new DateTimeZone([]),
    'tz object' => fn() => new DateTimeZone(new stdClass),
    'dt three' => fn() => new DateTime('now', null, 3),
    'dti three' => fn() => new DateTimeImmutable('now', null, 3),
    'dt array' => fn() => new DateTime([]),
    'dt object' => fn() => new DateTime(new stdClass),
    'dt zone string' => fn() => new DateTime('now', 'x'),
    'dti zone int' => fn() => new DateTimeImmutable('now', 5),
    'dt zone float' => fn() => new DateTime('now', 1.5),
    'dt zone true' => fn() => new DateTime('now', true),
    'dt zone object' => fn() => new DateTime('now', new stdClass),
    'sub three' => fn() => new MyDate('now', null, 3),
    'sub array' => fn() => new MyDate([]),
    'dt ok' => fn() => new DateTime('2020-01-02', new DateTimeZone('UTC')),
    'dt named zone' => fn() => new DateTime(timezone: null),
];
foreach ($cases as $label => $f) {
    try {
        $f();
        echo "$label: ok\n";
    } catch (Throwable $e) {
        echo "$label: ", get_class($e), ': ', $e->getMessage(), "\n";
    }
}
foreach (['DateTime', 'DateTimeImmutable', 'DateTimeZone'] as $class) {
    try {
        new $class;
        echo "$class: made\n";
    } catch (Throwable $e) {
        echo "$class: ", get_class($e), "\n";
    }
}
