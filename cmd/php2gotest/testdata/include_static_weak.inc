<?php
function takes_int(int $x) { return $x; }
function call_from_weak() { return takes_int("5"); }
