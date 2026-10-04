<?php
// An exception whose message is an object prints the object's string where
// its message goes -- the engine reads the property through zval_get_string
// -- both as `(string) $e` and when it is uncaught -- and getMessage(),
// declared `: string`, answers the string too (exception_delayed_message).

class Msg {
    function __construct(private string $text) {}
    function __toString(): string { return "msg:" . $this->text; }
}
class E extends Exception {
    public function __construct($m) {
        $this->message = $m;
    }
}
$e = new E(new Msg("one"));
echo strtok((string) $e, "\n"), "\n";
var_dump($e->getMessage());
throw new E(new Msg("two"));
