<?php
// The superglobals a request starts with, as the linked engine provides them:
// `$_GET`, `$_POST`, `$_COOKIE` and `$_FILES` always, empty; `$_SERVER`,
// `$_ENV` and `$_REQUEST` because this file names them, in the order the
// compiler met them -- `$_REQUEST` first, inside `req()` below. Values that
// differ between runs (the environment's contents, the request time) are
// checked for shape only.

var_dump(array_keys($GLOBALS));

function reader()
{
    return [$_GET, $_POST, $_COOKIE, $_FILES];
}
var_dump(reader());

function req()
{
    return $_REQUEST;
}
var_dump(req());

// A superglobal is one table, written from anywhere.
$_GET["a"] = 1;
function addGet()
{
    $_GET["b"] = 2;
}
addGet();
var_dump($_GET);

// Assigned out, it is a value: the copy grows, the superglobal does not.
$x = $_POST;
$x[] = 1;
var_dump(count($x), count($_POST));

var_dump($_SERVER["argv"], $_SERVER["argc"], isset($_SERVER["PHP_SELF"]));
var_dump(is_float($_SERVER["REQUEST_TIME_FLOAT"]), is_int($_SERVER["REQUEST_TIME"]));
var_dump(abs($_SERVER["REQUEST_TIME"] - (int) $_SERVER["REQUEST_TIME_FLOAT"]) <= 1);
var_dump($_ENV === getenv());
var_dump(array_slice(array_keys($_SERVER), -4));
var_dump(count($_SERVER) === count($_ENV) + 4);
var_dump(isset($argv), isset($argc));

unset($_GET);
var_dump(isset($_GET));
$_GET = "replaced";
var_dump($_GET);
echo "end\n";
