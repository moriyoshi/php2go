<?php
function t_int(int $x) { var_dump($x); }
function t_float(float $x) { var_dump($x); }
function t_string(string $x) { var_dump($x); }
function t_bool(bool $x) { var_dump($x); }
function t_nullable(?int $x) { var_dump($x); }

echo "t_int(int 5) = ";
try { t_int(5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(float 5.0) = ";
try { t_int(5.0); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(float 5.5) = ";
try { t_int(5.5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(str 5) = ";
try { t_int("5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(str 5.5) = ";
try { t_int("5.5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(str 5abc) = ";
try { t_int("5abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(str abc) = ";
try { t_int("abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(str empty) = ";
try { t_int(""); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(true) = ";
try { t_int(true); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(false) = ";
try { t_int(false); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_int(null) = ";
try { t_int(null); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "\n";
echo "t_float(int 5) = ";
try { t_float(5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(float 5.0) = ";
try { t_float(5.0); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(float 5.5) = ";
try { t_float(5.5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(str 5) = ";
try { t_float("5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(str 5.5) = ";
try { t_float("5.5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(str 5abc) = ";
try { t_float("5abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(str abc) = ";
try { t_float("abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(str empty) = ";
try { t_float(""); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(true) = ";
try { t_float(true); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(false) = ";
try { t_float(false); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_float(null) = ";
try { t_float(null); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "\n";
echo "t_string(int 5) = ";
try { t_string(5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(float 5.0) = ";
try { t_string(5.0); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(float 5.5) = ";
try { t_string(5.5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(str 5) = ";
try { t_string("5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(str 5.5) = ";
try { t_string("5.5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(str 5abc) = ";
try { t_string("5abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(str abc) = ";
try { t_string("abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(str empty) = ";
try { t_string(""); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(true) = ";
try { t_string(true); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(false) = ";
try { t_string(false); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_string(null) = ";
try { t_string(null); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "\n";
echo "t_bool(int 5) = ";
try { t_bool(5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(float 5.0) = ";
try { t_bool(5.0); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(float 5.5) = ";
try { t_bool(5.5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(str 5) = ";
try { t_bool("5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(str 5.5) = ";
try { t_bool("5.5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(str 5abc) = ";
try { t_bool("5abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(str abc) = ";
try { t_bool("abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(str empty) = ";
try { t_bool(""); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(true) = ";
try { t_bool(true); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(false) = ";
try { t_bool(false); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_bool(null) = ";
try { t_bool(null); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "\n";
echo "t_nullable(int 5) = ";
try { t_nullable(5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(float 5.0) = ";
try { t_nullable(5.0); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(float 5.5) = ";
try { t_nullable(5.5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(str 5) = ";
try { t_nullable("5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(str 5.5) = ";
try { t_nullable("5.5"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(str 5abc) = ";
try { t_nullable("5abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(str abc) = ";
try { t_nullable("abc"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(str empty) = ";
try { t_nullable(""); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(true) = ";
try { t_nullable(true); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(false) = ";
try { t_nullable(false); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "t_nullable(null) = ";
try { t_nullable(null); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
echo "\n";

// Return types coerce with the same rules and a different message shape.
function r_int(): int { return "abc"; }
function r_float(): float { return 5; }
function r_string(): string { return 42; }
function r_nullable(): ?int { return null; }
try { r_int(); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
var_dump(r_float());
var_dump(r_string());
var_dump(r_nullable());

// array and object verify without transforming: getting one wrong loses a
// TypeError but never corrupts a value.
function v_array(array $x) { var_dump(count($x)); }
function v_object(object $x) { var_dump(true); }
v_array([1, 2]);
try { v_array("s"); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
try { v_array(null); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
try { v_object(5); } catch (TypeError $e) { echo "TypeError: ", $e->getMessage(), "\n"; }
