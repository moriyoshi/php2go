<?php
// `print_r` moved to the shared `runtime/php` on the same one hook `var_dump`
// uses -- *what should this object show?* -- so compiled code can reach it.
// The one fact that had to come down with it is the enum **backing type**:
// print_r writes `Suit Enum:string` where var_dump writes `enum(Suit::Hearts)`.
print_r([1, "a" => 2, "b" => [3, 4]]);
echo "\n";
echo print_r("scalar", true), "|", print_r(1.5, true), "|", print_r(null, true), "|", print_r(true, true), "|\n";

class P { public $pub = 1; protected $prot = 2; private $priv = 3; }
class Q extends P { private $own = 4; public $dyn; }
$q = new Q; $q->extra = "added";
print_r($q);
echo "\n";

enum Pure { case A; case B; }
enum Backed: string { case X = "x"; }
enum Counted: int { case One = 1; }
print_r(Pure::A);
print_r(Backed::X);
print_r(Counted::One);
echo "\n";
print_r([Pure::A, Backed::X]);

// A cycle prints the refusal rather than descending forever.
$a = [1];
$a[] = &$a;
print_r($a);
$o = new stdClass; $o->self = $o;
print_r($o);

class D { public $keep = 1; public function __debugInfo() { return ["shown" => "yes"]; } }
print_r(new D);
