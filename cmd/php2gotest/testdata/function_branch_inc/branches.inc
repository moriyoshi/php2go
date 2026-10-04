<?php
// Declares g in one of two branches, chosen by the includer's $mode.
if ($mode > 1) {
    function g($x) { return "g-a:$x"; }
} else {
    function g(&$x) { $x .= "+"; return "g-b:$x"; }
}
