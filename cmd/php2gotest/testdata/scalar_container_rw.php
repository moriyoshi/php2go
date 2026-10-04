<?php
// A read-write fetch through a scalar container -- `.=` or `++` on an
// element of true, an int or a float -- is refused before any read; null
// and false autovivify and a string refuses in its own words.
$v = [true, 1, 1.5, null, false, "s"];
foreach ($v as $i => $x) { try { $x[0] .= "a"; echo "$i ok\n"; } catch (Error $e) { echo "$i ", $e->getMessage(), "\n"; } try { $x[0]++; echo "$i ok\n"; } catch (Error $e) { echo "$i ", $e->getMessage(), "\n"; } }
