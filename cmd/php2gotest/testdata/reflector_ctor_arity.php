<?php
// The reflector constructors count their arguments before anything else:
// the member reflectors take exactly two, ReflectionMethod at least one
// (instantiate_all_classes constructs every declared class with none).

foreach (["ReflectionParameter", "ReflectionClassConstant", "ReflectionMethod", "ReflectionProperty", "ReflectionClass", "ReflectionFunction"] as $c) {
    foreach ([[], ["stdClass"]] as $a) {
        try {
            new $c(...$a);
            echo "$c: built\n";
        } catch (Throwable $e) {
            echo get_class($e), ": ", $e->getMessage(), "\n";
        }
    }
}
