<?php
// `@` belongs to the fiber it is written in. A fiber suspended inside `@`
// gives the caller back its own level, and gets its silenced level back when
// resumed; a fiber started or resumed under `@` in the caller runs at the
// level it had, unsilenced.

function noisy(string $tag): void {
    trigger_error("in fiber $tag", E_USER_WARNING);
    Fiber::suspend();
    trigger_error("in fiber $tag after resume", E_USER_WARNING);
}

$inside = new Fiber(function (): void { @noisy("inside"); });
$inside->start();
trigger_error("caller while inside is suspended", E_USER_WARNING);
$inside->resume();
trigger_error("caller after inside ended", E_USER_WARNING);

$outside = new Fiber(function (): void { noisy("outside"); });
@$outside->start();
trigger_error("caller between", E_USER_WARNING);
@$outside->resume();
trigger_error("caller at the end", E_USER_WARNING);
var_dump(error_reporting());
