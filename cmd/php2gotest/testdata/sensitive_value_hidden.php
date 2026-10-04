<?php
// A SensitiveParameterValue shows nothing to a cast or json_encode, and
// refuses serialization outright.

$v = new SensitiveParameterValue('secret');
var_dump((array)$v, json_encode($v), get_object_vars($v));
try { serialize($v); } catch (Exception $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { unserialize('O:23:"SensitiveParameterValue":0:{}'); } catch (Exception $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
