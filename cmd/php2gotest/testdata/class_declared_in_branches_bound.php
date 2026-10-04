<?php
// A class bound at compile time takes the name before a conditional
// declaration of it runs.
echo "start\n";
class Early
{
}
$n = strlen("ab");
if ($n < 1) {
    echo "not taken\n";
} else {
    class Early
    {
        public $x;
    }
}
echo "not reached\n";
