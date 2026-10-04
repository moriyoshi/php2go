<?php
// `%s` converts an object as PHP converts one to a string: its __toString,
// an exception's own rendering, and the Error for an object with neither.

class Named { function __toString(): string { return "named"; } }
printf("[%s] [%5s]\n", new Named, new Named);
echo strtok(sprintf("%s", new RuntimeException("boom")), "\n"), "\n";
var_dump(vsprintf("%s-%s", [new Named, 1]));
try {
    sprintf("%s", new stdClass);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
