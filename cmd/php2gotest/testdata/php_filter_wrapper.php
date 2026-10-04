<?php
// The php://filter wrapper (runtime/stdlib filterwrapper.go): the target
// opened first and quietly, then the named filters put on its chains.
class Up extends php_user_filter {
    function filter($in, $out, &$consumed, $closing): int {
        while ($b = stream_bucket_make_writeable($in)) {
            $b->data = strtoupper($b->data);
            $consumed += $b->datalen;
            stream_bucket_append($out, $b);
        }
        return PSFS_PASS_ON;
    }
    function onClose(): void { echo "[close {$this->filtername}]\n"; }
}
class No extends php_user_filter { function onCreate(): bool { return false; } }
class Dflt extends php_user_filter {}
stream_filter_register("up", "Up");
stream_filter_register("no", "No");
stream_filter_register("dflt", "Dflt");
stream_filter_register("ghost", "Ghost");

echo "-- file_get_contents\n";
var_dump(file_get_contents("php://filter/read=string.toupper/resource=data:,hello bug"));
var_dump(file_get_contents("php://filter/read=string.toupper|string.rot13/resource=data:,hello"));
var_dump(file_get_contents("php://filter/string.rot13/resource=data:,hello"));
var_dump(file_get_contents("PHP://FILTER/READ=string.toupper/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=convert.base64-encode/resource=data:,Hello"));
var_dump(file_get_contents("php://filter/read=convert.base64-decode/resource=data:,SGVsbG8"));
var_dump(file_get_contents("php://filter/read=string.to%75pper/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=string.toupper%7Cstring.rot13/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=string.toupper||string.rot13/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=string.toupper+/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=/resource=data:,abc"));
var_dump(file_get_contents("php://filter/resource=data:,abc"));
var_dump(file_get_contents("php://filter/write=string.toupper/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=string.toupper/write=string.rot13/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=string.toupper/resource=php://filter/read=string.rot13/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=nonexist/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=convert.foo/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=up/resource=data:,user"));
var_dump(file_get_contents("php://filter/read=no/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=ghost/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=dflt/resource=data:,abc"));
var_dump(file_get_contents("php://filter/read=string.toupper/resource=" . __DIR__ . "/no/such/file"));
var_dump(file_get_contents("php://filter/read=nope/resource=php://filter/read=nope2/resource=/no/such/file"));
foreach (["php://filter/read=string.toupper", "php://filter/read=string.toupper/resource="] as $url) {
    try { var_dump(file_get_contents($url)); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}

echo "-- fopen\n";
$fp = fopen("php://filter/read=string.toupper/resource=data:,stream me", "r");
var_dump(get_resource_type($fp), fread($fp, 100), feof($fp));
fclose($fp);
$fp = fopen("php://filter/write=string.toupper/resource=php://memory", "w+");
var_dump(fwrite($fp, "written"));
rewind($fp);
var_dump(stream_get_contents($fp));
$fp = fopen("php://filter/read=dflt/resource=data:,abc", "r");
var_dump(fread($fp, 10));
var_dump(fopen("php://filter/read=string.toupper/resource=/no/such/file", "r"));

echo "-- file_put_contents\n";
var_dump(file_put_contents("php://filter/write=string.toupper/resource=php://memory", "abc"));
var_dump(file_put_contents("php://filter/write=up/resource=php://memory", "abcd"));
var_dump(file_put_contents("php://filter/write=string.toupper/resource=/no/such/dir/f", "abc"));

echo "-- include\n";
var_dump(include "php://filter/read=dflt/resource=" . __FILE__);
var_dump(include "php://filter/read=string.toupper/resource=data:,abc");
var_dump(include "php://filter/read=string.toupper/resource=/no/such/file");
$url = "php://filter/read=dflt/resource=" . __FILE__;
var_dump(include_once $url);
try { require "php://filter/read=dflt/resource=" . __FILE__; } catch (Error $e) { echo get_class($e), ": ", substr($e->getMessage(), 0, 30), "\n"; }
try { include "php://filter/read=string.toupper"; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "end\n";
