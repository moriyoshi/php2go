<?php
// PHP 8's comparison and conversion rules, which are not guessable from PHP 7's:
// `0 == "a"` is false where it used to be true, a leading-space numeric string
// compares as a number and a trailing-space one does too, and `"1_2"` is not
// numeric at all despite the literal syntax.

echo "--- loose comparison (PHP 8 rules)\n";
$pairs = [[0,"a"],[0,""],[0,null],["1","01"],["10","1e1"],[100,"1e2"],["abc",0],
          [null,false],[null,0],[null,""],[[],false],["0",false],[" 1",1],["1 ",1]];
foreach ($pairs as [$l,$r]) { var_dump($l == $r); }
echo "--- ordering\n";
var_dump(1 <=> "1", "a" <=> "b", [1,2] <=> [1,3], null <=> 0, true <=> 1);
echo "--- numeric strings\n";
foreach (["12", " 12", "12 ", "1_2", "0x1A", "1e3", ".5", "5.", "+7", "-0"] as $s) {
    var_dump($s, $s + 0);
}
echo "--- concatenation and increment\n";
$a = "z"; $a++; var_dump($a);
$b = "Az"; $b++; var_dump($b);
$c = "a9"; $c++; var_dump($c);
$d = null; $d++; var_dump($d);
echo "--- casts\n";
var_dump((int)"12abc", (float)"1.5e2x", (bool)"0", (bool)"0.0", (bool)[], (bool)[0]);
var_dump((string)true, (string)false, (string)null, (string)1.0, (string)0.1);
var_dump((array)"x", (array)null, (array)1);
echo "--- integer overflow and division\n";
var_dump(PHP_INT_MAX + 1, 7 % 3, -7 % 3, 7 / 2);
var_dump(0.1 + 0.2 == 0.3, (0.1 + 0.2) - 0.3 < PHP_FLOAT_EPSILON);
echo "--- string offsets and functions\n";
$s = "hello";
var_dump($s[1], $s[-2], isset($s[9]), $s <=> "hellp");
