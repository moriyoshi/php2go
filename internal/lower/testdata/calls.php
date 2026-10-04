<?php
function helper(string $s, int $n): string {
    return str_repeat($s, $n);
}

class Greeter {
    private string $suffix = '!';

    public function make(string $who): string {
        return helper($who, 2) . $this->decorate($who);
    }

    private function decorate(string $w): string {
        return $w . $this->suffix;
    }
}
