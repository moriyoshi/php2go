<?php
// Floats as the engine writes them. var_dump, var_export, json_encode and
// serialize use serialize_precision (shortest round-trip digits, fixed
// notation unless the decimal exponent is past 17 or below -3); echo and
// string conversion use precision (14 digits, exponent past 14). Both write a
// lone mantissa digit as `1.0` and never pad the exponent.

$floats = [
    1e14, 1e15, 1.5e15, 9007199254740992.0, 1e16, 1e17, 1.25e17, 0.0001,
    0.00001, 1.5e-5, 123456789.125, -1e16, 0.0, -0.0, 1.0, 0.1, 1 / 3, 2.5,
    -1.5e-300, 1.7976931348623157e308, 1e-10, 123456789012345.678, 1e100,
    0.1 + 0.2,
];
foreach ($floats as $f) {
    var_dump($f);
    var_export($f);
    echo "\n", json_encode($f), " ", json_encode($f, JSON_PRESERVE_ZERO_FRACTION), " ",
        serialize($f), " ", $f, " ", print_r($f, true), "\n";
}
