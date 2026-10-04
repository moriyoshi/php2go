<?php
$seen = isset($given) ? $given : "none";
$made = "made in " . basename(__FILE__);
return ["seen" => $seen, "n" => 7];
