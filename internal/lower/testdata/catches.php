<?php
function multi($n) {
    try {
        if ($n < 0) { throw new TypeError("t"); }
        return $n;
    } catch (ValueError $a) {
        return "value";
    } catch (TypeError $b) {
        return "type";
    }
}
