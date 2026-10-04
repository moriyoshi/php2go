<?php
// User stream filters: a php_user_filter subclass registered with
// stream_filter_register and attached with stream_filter_append/prepend,
// driven through the bucket brigade functions (runtime/stdlib userfilter.go).
class Up extends php_user_filter {
    function onCreate(): bool { echo "create {$this->filtername} ", var_export($this->params, true), "\n"; return true; }
    function onClose(): void { echo "close {$this->filtername}\n"; }
    function filter($in, $out, &$consumed, $closing): int {
        echo "filter {$this->filtername} closing=", var_export($closing, true), " stream=", gettype($this->stream), "\n";
        while ($b = stream_bucket_make_writeable($in)) {
            echo "  bucket ", get_class($b), " ", strlen($b->data), " ", $b->datalen, " ", $b->dataLength, " ", gettype($b->bucket), "\n";
            $b->data = strtoupper($b->data);
            $consumed += $b->datalen;
            stream_bucket_append($out, $b);
        }
        return PSFS_PASS_ON;
    }
}
class Wrap extends php_user_filter {
    function filter($in, $out, &$consumed, $closing): int {
        while ($b = stream_bucket_make_writeable($in)) {
            $b->data = "<" . $b->data . ">";
            $consumed += $b->datalen;
            stream_bucket_prepend($out, $b);
        }
        return PSFS_PASS_ON;
    }
}
class Buf extends php_user_filter {
    private $buf = '';
    function filter($in, $out, &$consumed, $closing): int {
        while ($b = stream_bucket_make_writeable($in)) { $this->buf .= $b->data; $consumed += $b->datalen; }
        echo "buf closing=", var_export($closing, true), " have=", strlen($this->buf), "\n";
        if ($closing) {
            stream_bucket_append($out, stream_bucket_new($this->stream, "[" . $this->buf . "]"));
            return PSFS_PASS_ON;
        }
        return PSFS_FEED_ME;
    }
}
class No extends php_user_filter { function onCreate(): bool { return false; } }
class Dflt extends php_user_filter {}
class Fatal extends php_user_filter { function filter($in, $out, &$consumed, $closing): int { return PSFS_ERR_FATAL; } }
class Parent_ extends php_user_filter {
    function filter($in, $out, &$consumed, $closing): int {
        var_dump(parent::filter($in, $out, $consumed, $closing));
        while ($b = stream_bucket_make_writeable($in)) { $consumed += $b->datalen; stream_bucket_append($out, $b); }
        return PSFS_PASS_ON;
    }
}

var_dump(stream_filter_register("up", "Up"));
var_dump(stream_filter_register("up", "Up"));
var_dump(stream_filter_register("wrap", "Wrap"));
var_dump(stream_filter_register("buf.*", "Buf"));
var_dump(stream_filter_register("no", "No"));
var_dump(stream_filter_register("dflt", "Dflt"));
var_dump(stream_filter_register("ghost", "Ghost"));
var_dump(stream_filter_register("fatal", "Fatal"));
var_dump(stream_filter_register("parent", "Parent_"));
var_dump(in_array("up", stream_get_filters()), in_array("buf.*", stream_get_filters()));
var_dump(PSFS_PASS_ON, PSFS_FEED_ME, PSFS_ERR_FATAL, STREAM_FILTER_READ, STREAM_FILTER_WRITE, STREAM_FILTER_ALL);

echo "-- write chain\n";
$fp = fopen("php://memory", "w+");
$f = stream_filter_append($fp, "up", STREAM_FILTER_WRITE, ["k" => 1]);
var_dump(get_resource_type($f));
fwrite($fp, "hello ");
fwrite($fp, "world\n");
var_dump(stream_filter_remove($f));
fwrite($fp, "tail\n");
rewind($fp);
echo stream_get_contents($fp);

echo "-- order\n";
$fp = fopen("php://memory", "w+");
stream_filter_append($fp, "wrap", STREAM_FILTER_WRITE);
$u = stream_filter_prepend($fp, "up", STREAM_FILTER_WRITE);
fwrite($fp, "ab");
stream_filter_remove($u);
rewind($fp);
echo stream_get_contents($fp), "\n";

echo "-- feed me\n";
$fp = fopen("php://memory", "w+");
$g = stream_filter_append($fp, "buf.x", STREAM_FILTER_WRITE);
fwrite($fp, "a");
fwrite($fp, "b");
var_dump(stream_filter_remove($g));
rewind($fp);
echo stream_get_contents($fp), "\n";

echo "-- read chain\n";
$fp = fopen("php://memory", "w+");
fwrite($fp, "read me\n");
rewind($fp);
stream_filter_append($fp, "up", STREAM_FILTER_READ);
echo fread($fp, 100);

echo "-- refusals\n";
var_dump(stream_filter_append($fp, "no"));
var_dump(stream_filter_append($fp, "ghost"));
var_dump(stream_filter_append($fp, "nonexistent"));
$fp2 = fopen("php://memory", "w+");
stream_filter_append($fp2, "dflt", STREAM_FILTER_WRITE);
var_dump(fwrite($fp2, "x"));
$fp3 = fopen("php://memory", "w+");
stream_filter_append($fp3, "fatal", STREAM_FILTER_WRITE);
var_dump(fwrite($fp3, "x"));
$fp4 = fopen("php://memory", "w+");
stream_filter_append($fp4, "parent", STREAM_FILTER_WRITE);
var_dump(fwrite($fp4, "x"));
var_dump(method_exists("Dflt", "filter"), method_exists("Dflt", "onCreate"), method_exists("Dflt", "onclose"));
var_dump((new Dflt)->onCreate(), (new Dflt)->onClose());
var_dump(new Dflt instanceof php_user_filter, get_parent_class(new Dflt));
$d = new Dflt;
echo $d->filtername === '' ? "empty name\n" : "name\n";
var_dump($d->params, $d->stream);

print_r(new php_user_filter);
echo "\n";
print_r(new StreamBucket);
echo "\n";

echo "-- errors\n";
foreach ([
    fn() => stream_filter_append(false, "up"),
    fn() => stream_bucket_make_writeable($fp),
    fn() => stream_filter_remove($fp),
    fn() => stream_bucket_new(false, "x"),
    fn() => stream_filter_register("", "X"),
    fn() => stream_filter_register("x", ""),
    fn() => (new Dflt)->filter(1, 2, $c, false),
] as $call) {
    try { $call(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
echo "end\n";
