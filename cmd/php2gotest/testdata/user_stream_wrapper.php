<?php
// A stream wrapper the program registers is a class the streams layer drives.
class VarStream {
    public $context;
    private string $name;
    private int $position = 0;

    public function stream_open($path, $mode, $options, &$opened_path) {
        $this->name = parse_url($path, PHP_URL_HOST);
        if (!isset($GLOBALS[$this->name])) {
            $GLOBALS[$this->name] = "";
        }
        return true;
    }
    public function stream_read($count) {
        $chunk = substr($GLOBALS[$this->name], $this->position, $count);
        $this->position += strlen($chunk);
        return $chunk;
    }
    public function stream_write($data) {
        $v = $GLOBALS[$this->name];
        $GLOBALS[$this->name] = substr($v, 0, $this->position) . $data . substr($v, $this->position + strlen($data));
        $this->position += strlen($data);
        return strlen($data);
    }
    public function stream_tell() { return $this->position; }
    public function stream_eof() { return $this->position >= strlen($GLOBALS[$this->name]); }
    public function stream_seek($offset, $whence) {
        if ($whence !== SEEK_SET || $offset < 0) {
            return false;
        }
        $this->position = $offset;
        return true;
    }
    public function stream_stat() { return ["size" => strlen($GLOBALS[$this->name])]; }
}

var_dump(stream_wrapper_register("var", "VarStream"));
var_dump(stream_wrapper_register("var", "VarStream"));
var_dump(in_array("var", stream_get_wrappers()));
$myvar = "";
$fp = fopen("var://myvar", "r+");
fwrite($fp, "line1\nline2\n");
rewind($fp);
while (!feof($fp)) {
    echo "read: ", fgets($fp);
}
fclose($fp);
var_dump($myvar);
$meta = stream_get_meta_data(fopen("var://other", "w"));
var_dump($meta["wrapper_type"], $meta["stream_type"], get_class($meta["wrapper_data"]));
try {
    stream_wrapper_register("x", "NoSuchClass");
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump(stream_wrapper_register("bad scheme", "VarStream"));
var_dump(stream_wrapper_unregister("var"), in_array("var", stream_get_wrappers()));
var_dump(stream_wrapper_unregister("var"));
var_dump(stream_wrapper_restore("file"), stream_wrapper_restore("nope"));

// stream_get_meta_data asks the wrapper's stream_eof, every time.
class EofProbe {
    public $context;
    public $n = 0;
    function stream_open($p, $m, $o, &$op) { return true; }
    function stream_read($c) { $this->n++; return $this->n == 1 ? "abc" : ""; }
    function stream_eof() { echo "eof?\n"; return $this->n >= 2; }
}
stream_wrapper_register("eofprobe", "EofProbe");
$p = fopen("eofprobe://x", "r");
echo "read: ", fread($p, 3), "\n";
var_dump(stream_get_meta_data($p)["eof"]);
var_dump(feof($p));
var_dump(stream_get_meta_data($p)["eof"]);

// A write leaves EOF set; only a read or a seek changes it.
class Appending {
    public $context;
    public $d = "ab";
    public $p = 0;
    function stream_open($p, $m, $o, &$op) { return true; }
    function stream_read($c) { $r = substr($this->d, $this->p, $c); $this->p += strlen($r); return $r; }
    function stream_write($s) { $this->d .= $s; return strlen($s); }
    function stream_eof() { echo "eof? p={$this->p}\n"; return $this->p >= strlen($this->d); }
}
stream_wrapper_register("appending", "Appending");
$g = fopen("appending://x", "r+");
var_dump(fread($g, 10), feof($g));
var_dump(fwrite($g, "cd"), feof($g));
var_dump(fread($g, 10), feof($g));

// A registered wrapper holds a resource of its own: a duplicate or invalid
// scheme still takes a number, an unknown class does not, and unregistering
// releases it.
class Numbered {
    public $context;
    function stream_open($p, $m, $o, &$op) { return true; }
}
try {
    stream_wrapper_register("numbered", "NoSuchNumbered");
} catch (TypeError $e) {
}
var_dump(stream_wrapper_register("numbered", "Numbered"));
var_dump(@stream_wrapper_register("numbered", "Numbered"));
var_dump(@stream_wrapper_register("bad scheme", "Numbered"));
var_dump(fopen("numbered://x", "r"));
var_dump(stream_wrapper_unregister("numbered"));
var_dump(fopen("php://memory", "r"));

// A scheme is looked up as written, then lowercased.
class Cased {
    public $context;
    function stream_open($p, $m, $o, &$op) { echo "open $p\n"; return true; }
}
stream_wrapper_register("lower", "Cased");
stream_wrapper_register("Upper", "Cased");
var_dump(is_resource(fopen("LOWER://x", "r")), is_resource(fopen("Upper://x", "r")));
var_dump(@fopen("upper://x", "r"), @fopen("UPPER://x", "r"));

// A wrapper's diagnostics name the library function that reached it, at the
// caller's line; a throwing stream_eof is reported as missing, then thrown.
class Refusing {
    public $context;
    function stream_open($p, $m, $o, &$op) { return false; }
}
stream_wrapper_register("refusing", "Refusing");
var_dump(fopen("refusing://a", "r"));
function openRefused() {
    return fopen("refusing://b", "r");
}
var_dump(openRefused());
class NoEof {
    public $context;
    public $n = 0;
    function stream_open($p, $m, $o, &$op) { return true; }
    function stream_read($c) { $this->n++; return $this->n == 1 ? "abc" : ""; }
}
stream_wrapper_register("noeof", "NoEof");
$q = fopen("noeof://x", "r");
var_dump(fread($q, 1), feof($q));
class ThrowingEof {
    public $context;
    function stream_open($p, $m, $o, &$op) { return true; }
    function stream_eof() { throw new Exception("eof"); }
}
stream_wrapper_register("throwingeof", "ThrowingEof");
$z = fopen("throwingeof://x", "r");
try {
    feof($z);
} catch (Exception $e) {
    echo "caught ", $e->getMessage(), "\n";
}
