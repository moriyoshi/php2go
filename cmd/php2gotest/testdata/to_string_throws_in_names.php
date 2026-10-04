<?php
// A name or a value converted through a __toString that throws is that
// exception, and nothing is unset, tested or written.
class BadStr
{
    public function __toString()
    {
        throw new Exception("thrown");
    }
}
class GoodStr
{
    public function __toString()
    {
        return "zq";
    }
}
$badStr = new BadStr;
$str = "abc";

${""} = 42;
try {
    unset(${$badStr});
} catch (Exception $e) {
    echo "unset: ", $e->getMessage(), "\n";
}
var_dump(${""});
try {
    var_dump(isset(${$badStr}));
} catch (Exception $e) {
    echo "isset: ", $e->getMessage(), "\n";
}
try {
    var_dump(empty(${$badStr}));
} catch (Exception $e) {
    echo "empty: ", $e->getMessage(), "\n";
}
$zq = 1;
var_dump(isset(${new GoodStr}), empty(${new GoodStr}));
unset(${new GoodStr});
var_dump(isset($zq));

try {
    $str[0] = $badStr;
} catch (Exception $e) {
    echo "offset: ", $e->getMessage(), "\n";
}
var_dump($str);
$str[1] = new GoodStr;
var_dump($str);
