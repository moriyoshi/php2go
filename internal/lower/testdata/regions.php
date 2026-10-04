<?php
function nested(array $rows): string {
    $log = '';
    try {
        foreach ($rows as $r) {
            try {
                $log .= $r;
            } catch (TypeError $inner) {
                $log .= 'T';
            }
        }
    } catch (RuntimeException $outer) {
        return 'outer';
    }
    return $log;
}
