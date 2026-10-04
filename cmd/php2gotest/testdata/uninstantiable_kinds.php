<?php
// A trait, an interface, an abstract class and an enum have no instances:
// `new $name` and every ReflectionClass constructor refuse them alike, naming
// the kind and the class as declared.
trait T {}
interface I {}
abstract class A {}
enum E {}
foreach (["T","I","A","E"] as $c) { foreach (["newInstance","newInstanceWithoutConstructor","newInstanceArgs"] as $m) { try { (new ReflectionClass($c))->$m(); echo "$c $m: made\n"; } catch (Throwable $e) { echo "$c $m: ", get_class($e), ": ", $e->getMessage(), "\n"; } } }
foreach (["T","I","A","E"] as $c) { try { new $c; } catch (Throwable $e) { echo "new $c: ", $e->getMessage(), "\n"; } }
try { new ReflectionClass("a") instanceof ReflectionClass && (new ReflectionClass("a"))->newInstance(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
