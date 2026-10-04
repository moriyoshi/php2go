<?php
class AppErr extends RuntimeException {}
function t($k) {
    if ($k == 1) { throw new TypeError("a type error"); }
    if ($k == 2) { throw new AppErr("app"); }
    if ($k == 3) { throw new InvalidArgumentException("bad arg"); }
    if ($k == 4) { throw new ParseError("parse"); }
    return "none";
}
foreach ([1, 2, 3, 4, 5] as $k) {
    try {
        echo t($k), "\n";
    } catch (Exception $e) {
        echo "Exception branch: ", $e->getMessage(), "\n";
    } catch (CompileError $e) {
        echo "CompileError branch: ", $e->getMessage(), "\n";
    } catch (Error $e) {
        echo "Error branch: ", $e->getMessage(), "\n";
    }
}
