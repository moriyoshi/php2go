<?php
function mandel2() {
  $b = " .:,;!/>)|&IH%*#";
  //float r, i, z, Z, t, c, C;
  for ($y=30; printf("\n"), $C = $y*0.1 - 1.5, $y--;){
    for ($x=0; $c = $x*0.04 - 2, $z=0, $Z=0, $x++ < 75;){
      for ($r=$c, $i=$C, $k=0; $t = $z*$z - $Z*$Z + $r, $Z = 2*$z*$Z + $i, $z=$t, $k<5000; $k++)
        if ($z*$z + $Z*$Z > 500000) break;
      echo $b[$k%16];
    }
  }
}
$t = hrtime(true); ob_start(); mandel2(); mandel2(); mandel2(); ob_end_clean(); echo "mandel2 ", round((hrtime(true)-$t)/1e9, 3), "\n";
