<?php
// A function declared in either branch of an included file is the body of
// the branch that ran; including the file again redeclares it, naming the
// declaration that ran.
$mode = 1;
include __DIR__ . '/function_branch_inc/branches.inc';
$v = "v";
echo g($v), " ", $v, "\n";
$mode = 2;
include __DIR__ . '/function_branch_inc/branches.inc';
echo "not reached\n";
