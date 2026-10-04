<?php
// A variable named by a non-string literal is still an ordinary local:
// `${true}` is `$1`, written and read back through the same slot, in the file
// body and in a function alike, and `@${false}` reads `$` silently.

${true} = 42;
var_dump(${true});
var_dump(${'1'});

function f() {
    ${1} = "one";
    ${2.5} = "float";
    var_dump(${'1'}, ${'2.5'}, ${1});
    var_dump(@${false});
}
f();
