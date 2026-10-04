<?php
function Ack($m, $n){
  if($m == 0) return $n+1;
  if($n == 0) return Ack($m-1, 1);
  return Ack($m - 1, Ack($m, ($n - 1)));
}
$t = hrtime(true); $r = Ack(3, 8); echo "ack ", $r, " ", round((hrtime(true)-$t)/1e9, 3), "\n";
