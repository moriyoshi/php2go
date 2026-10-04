<?php
// `#[Attr(...)]` through Reflection. An attribute is a **declaration, not a
// value**: the harvest carries its arguments unevaluated, and the constructor
// does not run until `newInstance()` asks for an instance. That is why the
// emitted table holds argument values rather than objects.
#[Attribute]
class Marker
{
    public function __construct(public string $tag = 'none', public int $n = 0)
    {
    }
}

#[Attribute]
class Other
{
}

#[Marker('cls', 3)]
#[Other]
class Subject
{
    #[Marker('prop')]
    public int $field = 1;

    #[Marker(tag: 'meth', n: 9)]
    public function go(): void
    {
    }
}

#[Marker(tag: 'fn')]
function target()
{
}

$rc = new ReflectionClass('Subject');
var_dump($rc->getName(), $rc->name, $rc->getShortName(), $rc->inNamespace());
foreach ($rc->getAttributes() as $a) {
    var_dump($a->getName(), $a->getArguments());
}

// The filter is by exact name; positional arguments keep their position and a
// named one is keyed by its name, in the order written.
foreach ($rc->getAttributes('Marker') as $a) {
    $i = $a->newInstance();
    var_dump(get_class($i), $i->tag, $i->n);
}
var_dump(count($rc->getAttributes('Other')), count($rc->getAttributes('Nope')));

// Members carry their own attributes, and the two reflectors name the member
// while `getDeclaringClass()` names the class.
$rm = $rc->getMethod('go');
var_dump($rm->getName(), $rm->class, $rm->getDeclaringClass()->getName());
foreach ($rm->getAttributes() as $a) {
    var_dump($a->getName(), $a->getArguments());
    $i = $a->newInstance();
    var_dump($i->tag, $i->n);
}

$rp = new ReflectionProperty('Subject', 'field');
var_dump($rp->getName());
foreach ($rp->getAttributes() as $a) {
    var_dump($a->getName(), $a->getArguments(), $a->newInstance()->tag);
}

$rf = new ReflectionFunction('target');
var_dump($rf->getName());
foreach ($rf->getAttributes() as $a) {
    var_dump($a->getName(), $a->getArguments(), $a->newInstance()->tag);
}

// ReflectionAttribute has no public constructor at all.
try {
    new ReflectionAttribute();
} catch (Throwable $e) {
    var_dump(get_class($e), $e->getMessage());
}

// `#[Attribute(...)]` on the attribute class restricts where it may be used,
// and the refusal is raised at **newInstance()** rather than at the
// declaration -- an attribute is only checked when something asks for an
// instance of it. A bare `#[Attribute]` allows everything.
#[Attribute(Attribute::TARGET_CLASS)]
class ClassOnly
{
}

#[ClassOnly]
class Targeted
{
}

#[ClassOnly]
function targetedWrongly()
{
}

var_dump(get_class((new ReflectionClass('Targeted'))->getAttributes()[0]->newInstance()));
try {
    (new ReflectionFunction('targetedWrongly'))->getAttributes()[0]->newInstance();
} catch (Throwable $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
