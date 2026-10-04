<?php
// An uncaught throwable prints its **whole previous-chain**, and three things
// about the format are not guessable -- all three were taken from the engine:
//
//   * the root cause prints FIRST, as `Uncaught`, and each exception that
//     wrapped it follows as `Next`, outward;
//   * the entries are separated by a blank line;
//   * the closing `thrown in` names the OUTERMOST exception's file and line,
//     not the root's -- which is why this file puts them on different lines.
//
// We printed the outermost alone, so every chained fatal lost its cause.
$a = new Exception("root cause");

$b = new RuntimeException("middle", 0, $a);

throw new LogicException("outermost", 0, $b);
