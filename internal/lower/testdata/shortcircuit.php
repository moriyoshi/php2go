<?php
function pick($a, $b) {
    // An array literal whose accumulator has to survive a block boundary,
    // because each element short-circuits.
    return [
        $a && $b,
        $a || $b,
        $a ?? $b,
        $a ?: $b,
    ];
}
