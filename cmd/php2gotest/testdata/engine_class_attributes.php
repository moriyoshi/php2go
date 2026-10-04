<?php
// Reflection on an engine class reports the attributes the engine gives it:
// the attribute classes carry #[Attribute(flags)], stdClass carries
// #[AllowDynamicProperties].
foreach (['Attribute', 'stdClass'] as $c) {
    foreach ((new ReflectionClass($c))->getAttributes() as $a) {
        echo $c, ": ", $a->getName(), " ", json_encode($a->getArguments()), "\n";
    }
}
$a = (new ReflectionClass('Attribute'))->getAttributes()[0]->newInstance();
var_dump(get_class($a), $a->flags === Attribute::TARGET_CLASS);
#[Attribute]
class Mine {}
var_dump(count((new ReflectionClass('Mine'))->getAttributes()));
