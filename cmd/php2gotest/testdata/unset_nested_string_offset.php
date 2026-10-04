<?php
// A nested unset through a string: an integer offset names a character,
// which cannot be descended into; any other offset is the plain refusal to
// unset a string offset.

$x = "str";
try { unset($x[0][0]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { unset($x["1"]["a"]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { unset($x["a"]["b"]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { unset($x[0]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump($x);
