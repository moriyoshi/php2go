<?php
// Unsetting through a container that cannot hold offsets. Measured over every
// one, because only two of the six are silent:
//
//	null        nothing, and no diagnostic
//	false       the deprecation, then nothing
//	string      Cannot unset string offsets
//	int/float/  Cannot unset offset in a non-array variable
//	true
//
// Treating them all as a no-op swallowed three of the six, and a test whose
// whole output is the fatal block then printed nothing at all.
foreach (['str', 5, 1.5, true, false, null] as $i => $value) {
    $subject = $value;
    try {
        unset($subject['x']['y']);
        echo "$i: no error\n";
    } catch (Throwable $e) {
        echo "$i: ", get_class($e), ': ', $e->getMessage(), "\n";
    }
}
