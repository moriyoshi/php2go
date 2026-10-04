<?php
// Reflectors over the engine's own classes, methods, properties and
// functions answer from the engine's reflection (harvested), and a
// throwable's private trace written through ReflectionProperty::setValue is
// what getTraceAsString and the string form render, with the engine's
// warnings for frames it cannot read.

function show($label, $v) {
    echo $label, ': ';
    var_dump($v);
}

echo "-- classes\n";
$rc = new ReflectionClass('Exception');
show('name', $rc->getName());
show('internal', $rc->isInternal());
show('user', $rc->isUserDefined());
show('file', $rc->getFileName());
show('doc', $rc->getDocComment());
show('interface', $rc->isInterface());
show('final', $rc->isFinal());
show('abstract', $rc->isAbstract());
show('attrs', $rc->getAttributes());
show('short', $rc->getShortName());
show('ns', $rc->inNamespace());
show('hasMethod', $rc->hasMethod('GETMESSAGE'));
show('hasMethod nope', $rc->hasMethod('nope'));
show('parent', $rc->getParentClass());
show('parent of ErrorException', (new ReflectionClass('ErrorException'))->getParentClass()->name);

echo "-- methods\n";
$m = $rc->getMethod('getMessage');
show('name', $m->name);
show('class', $m->class);
show('final', $m->isFinal());
show('public', $m->isPublic());
show('static', $m->isStatic());
show('params', $m->getNumberOfParameters());
show('declaring', $m->getDeclaringClass()->name);
$ctor = new ReflectionMethod('Exception', '__construct');
show('ctor params', $ctor->getNumberOfParameters());
show('ctor required', $ctor->getNumberOfRequiredParameters());
foreach ($ctor->getParameters() as $p) {
    echo $p->getPosition(), ' ', $p->getName(), ' opt=', var_export($p->isOptional(), true),
        ' type=', var_export($p->hasType(), true), ' null=', var_export($p->allowsNull(), true),
        ' def=', var_export($p->isDefaultValueAvailable(), true), "\n";
}
try {
    new ReflectionMethod('Exception', 'nope');
} catch (ReflectionException $e) {
    show('missing method', $e->getMessage());
}
try {
    (new ReflectionClass('Exception'))->getMethod('nope');
} catch (ReflectionException $e) {
    show('missing getMethod', $e->getMessage());
}

echo "-- properties\n";
$t = new ReflectionProperty('Exception', 'trace');
show('private', $t->isPrivate());
show('readonly', $t->isReadOnly());
show('hasType', $t->hasType());
show('type', (string)$t->getType());
show('declaring', $t->getDeclaringClass()->name);
show('attrs', (new ReflectionProperty('Exception', 'message'))->getAttributes());
show('protected', (new ReflectionProperty('Exception', 'message'))->isProtected());
class MyException extends Exception {}
show('through subclass', (new ReflectionProperty('MyException', 'message'))->class);
foreach ([['MyException', 'trace'], ['Exception', 'nope']] as [$c, $n]) {
    try {
        new ReflectionProperty($c, $n);
    } catch (ReflectionException $e) {
        show('missing property', $e->getMessage());
    }
}

echo "-- functions\n";
$rf = new ReflectionFunction('array_map');
show('params', $rf->getNumberOfParameters());
show('required', $rf->getNumberOfRequiredParameters());
foreach ($rf->getParameters() as $p) {
    echo $p->getName(), ' variadic=', var_export($p->isVariadic(), true), "\n";
}
$f = (new ReflectionFunction('iterator_to_array'))->getClosure();
$invoke = new ReflectionMethod($f, '__invoke');
show('invoke class', $invoke->class);
show('invoke params', $invoke->getNumberOfParameters());
show('getClass', $invoke->getParameters()[0]->getClass()->name);
show('getClass bool', $invoke->getParameters()[1]->getClass());

echo "-- written trace\n";
$e = new Exception('boom');
$ref = new ReflectionProperty($e, 'trace');
$ref->setValue($e, [['file' => 'a.php', 'line' => 3, 'class' => 'C', 'type' => '->', 'function' => 'm', 'args' => [1, 'x', null]]]);
var_dump($e->getTraceAsString());
var_dump(count($ref->getValue($e)));
echo $e, "\n";
$ref->setValue($e, [null, ['function' => 'g', 'args' => 'bad'], ['file' => 5, 'class' => 1]]);
var_dump($e->getTraceAsString());
$ref->setValue($e, []);
var_dump($e->getTraceAsString());

echo "-- declaring class\n";
class PropBase {
    public $p = 1;
    protected $q;
}
class PropChild extends PropBase {}
show('p', (new ReflectionProperty('PropChild', 'p'))->class);
show('q', (new ReflectionProperty('PropChild', 'q'))->getDeclaringClass()->name);
class OwnMessage extends Exception {
    protected $message = 'own';
}
show('own message', (new ReflectionProperty('OwnMessage', 'message'))->class);
show('ATOM', (new ReflectionClassConstant('DateTime', 'ATOM'))->getValue());
show('ATOM class', (new ReflectionClassConstant('DateTime', 'ATOM'))->class);
