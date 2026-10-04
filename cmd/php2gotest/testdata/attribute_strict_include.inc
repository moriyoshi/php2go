<?php
declare(strict_types=1);

#[A("1")]
class S {
    #[A("2")]
    public function m(#[A("3")] $x) {}
    #[A("4")]
    public $p;
    #[A("5")]
    const K = 1;
}

#[A("6")]
function sf() {}

trait T {
    #[A("7")]
    public function tm(#[A("8")] $y) {}
}

function strictClosure() {
    return #[A("9")] function () {};
}
