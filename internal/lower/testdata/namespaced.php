<?php
namespace App\Support;

function render(array $rows): string
{
    // Unqualified inside a namespace: each resolves to App\Support\x when that
    // exists and to the global x otherwise, so both names have to survive.
    $glue = implode(',', $rows);

    // Fully qualified, so there is nothing to fall back to.
    $pad = \str_repeat('-', 2);

    // A first-class callable resolves the same pair the call would have.
    $f = strtoupper(...);

    return $glue . $pad . $f('x');
}
