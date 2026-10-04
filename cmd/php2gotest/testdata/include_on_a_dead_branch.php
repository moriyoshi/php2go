<?php
// `include` and `eval` cannot work in a standalone artifact and are refused --
// but at RUN TIME, so a body that carries one on a branch nothing takes still
// compiles and runs. A program that actually reaches one reports
// `php2go: unsupported: include or eval` and grades unsupported, which is the
// honest answer and cannot be pinned in a fixture that must pass.
if (false) { include "never-reached.php"; }
$mode = "safe";
if ($mode === "unsafe") { require "also-never.php"; }
echo "ran\n";
function helper() { if (false) { include "nor-this.php"; } return 42; }
var_dump(helper());
echo "done\n";
