<?php
// Declaring a constant twice keeps the first value and warns, whether the
// second is `const` or `define`, namespaced or not.
namespace foo;

const foo = 1;
const foo = 2;
var_dump(foo);
\define('foo\bar', 1);
const bar = 3;
var_dump(bar, \define('foo\bar', 4));
