<?php
echo "warned body\n";
if (!class_exists('Warned', false)) {
    class Warned { private function __invoke() {} public function hi() { return 'hi'; } }
}
return 7;
