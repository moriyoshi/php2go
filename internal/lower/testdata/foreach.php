<?php
function total(array $rows): int {
    $sum = 0;
    foreach ($rows as $k => $v) {
        $sum = $sum + $v;
    }
    return $sum;
}

// The key is **used** here, which the function above does not do. A foreach
// binds the key through a compiler temporary, and promoting that temporary left
// the binding naming a slot nothing wrote any more -- so every keyed foreach
// inside a function produced an empty key. `total` could not catch it: an
// unused key is legitimately dead.
function joined(array $rows): string {
    $out = "";
    foreach ($rows as $k => $v) {
        $out = $out . $k . "=" . $v . ";";
    }
    return $out;
}
