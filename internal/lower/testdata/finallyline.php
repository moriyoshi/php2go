<?php
function f($early) {
    try {
        if ($early) { return $undef_in_return; }
    } finally {
        echo "finally ran\n";
    }
    return 0;
}
