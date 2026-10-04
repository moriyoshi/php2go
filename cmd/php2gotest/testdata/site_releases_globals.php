<?php
// Any `$GLOBALS` in the program reaches every file-body local by name, so no
// interval is proved: the values here outlive their overwrite
// (site_releases holds the cases that do die).

class Q { static $quiet = false; }
class D {
    public $tag;
    function __construct($tag) { $this->tag = $tag; }
    function __destruct() { if (!Q::$quiet) echo "~", $this->tag, "\n"; }
}
function peek() { static $s; $s = $GLOBALS["gg"]; }
$gg = new D("gg");
peek();
$gg = 1;
echo "after gg (the static holds it)\n";
$ga = [new D("ga")];
$ga[0] = 2;
echo "end\n";
Q::$quiet = true;
