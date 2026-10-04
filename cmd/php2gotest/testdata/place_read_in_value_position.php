<?php
// A write fetch reaching an operation that reads: the place is read, with the
// diagnostics an ordinary read would emit.
function &byRef(): string { $a = ["ref"]; return $a[0]; }
var_dump(byRef());
class C {
    public $d = ["x" => "s"];
    public function &get(): string { return $this->d["x"]; }
}
var_dump((new C)->get());
// A missing key read through a place warns exactly once.
function &missing() { $a = []; return $a["nope"]; }
var_dump(missing());
echo "done\n";
