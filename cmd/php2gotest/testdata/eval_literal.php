<?php
// eval of code written in the program is compiled with it: it runs over the
// caller's variables, answers null unless it returns, declares what it
// holds when it runs, and is named after the line that ran it.
$x = 1;
var_dump(eval('$y = $x + 1;'), $y);
var_dump(eval('return $x * 10;'));
echo eval('return basename(__FILE__);'), "\n";
var_dump(function_exists('from_eval'));
eval('function from_eval() { return "declared by eval"; } class FromEval {}');
var_dump(from_eval(), new FromEval);

function scoped() {
    $local = 5;
    eval('$local *= 2; $created = "in scope";');
    return [$local, $created];
}
var_dump(scoped());

try {
    eval('function thrower() { throw new Exception("from eval"); } thrower();');
} catch (Exception $e) {
    echo basename($e->getFile()), ":", $e->getLine(), "\n", $e->getTraceAsString(), "\n";
}
try {
    eval('this is not php');
} catch (ParseError $e) {
    echo get_class($e), ": ", $e->getMessage(), " @ ", basename($e->getFile()), ":", $e->getLine(), "\n";
}
set_error_handler(function ($no, $msg, $file) {
    echo "handler: $msg @ ", basename($file), "\n";
    return true;
});
eval('class Magic { private function __invoke() {} }');
echo "end\n";
