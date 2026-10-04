<?php
// php://memory and php://temp: a byte buffer with a position, presented as
// the engine's own stream.
$f = fopen("php://memory", "r+");
var_dump($f, get_resource_type($f));
var_dump(fwrite($f, "hello world"), ftell($f), feof($f));
rewind($f);
var_dump(fread($f, 5), ftell($f), fgets($f), feof($f));
var_dump(fseek($f, 20), ftell($f), fwrite($f, "!"), ftell($f));
rewind($f);
var_dump(bin2hex(stream_get_contents($f)));
var_dump(fseek($f, -1, SEEK_END), fgetc($f), fseek($f, -100, SEEK_SET));
var_dump(ftruncate($f, 5));
rewind($f);
var_dump(stream_get_contents($f));
$stat = fstat($f);
var_dump($stat["size"], $stat["mode"], $stat["dev"], $stat["nlink"]);
var_dump(stream_get_meta_data($f));
fclose($f);

$a = fopen("php://memory", "a+");
fwrite($a, "x");
rewind($a);
fwrite($a, "y");
rewind($a);
var_dump(stream_get_contents($a), stream_get_meta_data($a)["mode"]);

$r = fopen("php://memory", "r");
var_dump(fwrite($r, "q"), ftruncate($r, 0), fstat($r)["mode"], stream_get_meta_data($r)["mode"]);

$t = fopen("php://temp/maxmemory:64", "w+");
fwrite($t, str_repeat("ab", 100));
var_dump(ftell($t), stream_get_meta_data($t)["stream_type"], stream_get_meta_data($t)["uri"]);
rewind($t);
var_dump(strlen(fread($t, 1000)));

// Each opening is its own buffer.
$one = fopen("php://memory", "w+");
$two = fopen("php://memory", "w+");
fwrite($one, "one");
rewind($two);
var_dump(stream_get_contents($two));
var_dump(in_array("PHP2Go\\Extension\\Streams\\MemoryStreamWrapper", get_declared_classes()));

// EOF: set by a read that finds nothing, kept across a write, cleared by a
// seek; fread fills its buffer as from a plain file.
$e = fopen("php://memory", "w+");
fwrite($e, "abc");
var_dump(feof($e));
rewind($e);
var_dump(fread($e, 3), feof($e));
var_dump(fread($e, 3), feof($e));
fwrite($e, "de");
var_dump(feof($e), ftell($e));
fseek($e, 3);
var_dump(feof($e), fread($e, 10), feof($e));
fseek($e, 100);
var_dump(feof($e), fread($e, 1), feof($e));
rewind($e);
var_dump(fgets($e), feof($e), stream_get_meta_data($e)["eof"]);

// php://temp is two streams to the engine, so it takes two resource numbers.
var_dump(fopen("php://temp", "r"), fopen("php://memory", "r"), fopen("php://temp/maxmemory:10", "w+"), fopen("php://memory", "r"));

// A write that would reach maxmemory moves php://temp to a file, which is
// another stream; the limit counts from the position, not the size.
$s1 = fopen("php://temp/maxmemory:4", "w+");
fwrite($s1, "abc");
var_dump(fopen("php://memory", "r"));
rewind($s1);
fwrite($s1, "x");
ftruncate($s1, 50);
var_dump(fopen("php://memory", "r"));
fseek($s1, 3);
fwrite($s1, "d");
var_dump(fopen("php://memory", "r"));
$s2 = fopen("PHP://TEMP/MAXMEMORY:3abc", "w+");
fwrite($s2, "abc");
$s3 = fopen("php://temp/maxmemory:x", "w+");
fwrite($s3, "a");
$s4 = fopen("php://tempfoo", "w+");
fwrite($s4, "a");
var_dump(fopen("php://memory", "r"), stream_get_meta_data($s4));
try {
    fopen("php://temp/maxmemory:-1", "w+");
} catch (ValueError $e) {
    echo $e->getMessage(), "\n";
}
