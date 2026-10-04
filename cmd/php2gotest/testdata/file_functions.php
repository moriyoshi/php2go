<?php
// A compiled program can open a stream now: the wrapper registry moved to the
// Ctx, so `fopen` can find a scheme without the interpreter's Unit. Everything
// here runs against a real file the emitted binary opens for itself.
$path = "/tmp/php2go-file-functions-test";
$fh = fopen($path, "w+");
var_dump(fwrite($fh, "alpha\nbeta\n"), fputs($fh, "gamma\n"));
var_dump(ftell($fh), rewind($fh), ftell($fh), feof($fh));
var_dump(fgets($fh), fgets($fh), ftell($fh));
var_dump(fread($fh, 3), fread($fh, 100), feof($fh), fgets($fh));
var_dump(fseek($fh, 6), ftell($fh), fread($fh, 4));
var_dump(fseek($fh, 1, SEEK_CUR), ftell($fh));
try { fread($fh, 0); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
var_dump(fclose($fh));
// Operating on an already-closed resource is a TypeError from the *argument*
// check, which compiled code does not make yet -- see TODO.md -- so it is not
// exercised here.
// unlink is still the interpreter's, so the file is left behind here.
// A `data:` URI needs no file at all, and an unknown scheme is a warning.
$data = fopen("data:text/plain,hello", "r");
var_dump(fgets($data), feof($data), rewind($data), fread($data, 5), fclose($data));
var_dump(fopen("nosuchwrapper://x", "r"));
var_dump(fopen("/tmp/php2go-definitely-absent-file", "r"));
var_dump(fopen($path, "!!"));
