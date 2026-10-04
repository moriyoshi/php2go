<?php
$seen = "include saw: " . (isset($local) ? $local : "nothing");
$made = "made by the include";
unset($doomed);
return "returned";
