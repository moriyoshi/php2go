<?php
// A program that rethrows a caught exception (`throw $e`) can hand one
// exception to two catches, so no catch variable is taken as its only holder:
// here the first catch keeps it and rethrows, and the second must not free
// what the first kept. The next object's number shows it is still held.
class Thing {}
class Keep { public static $kept; }
function thrower() { throw new RuntimeException("x"); }
function keepsAndRethrows() {
    try { thrower(); } catch (Exception $e) { Keep::$kept = $e; throw $e; }
}
function outer() {
    try { keepsAndRethrows(); } catch (Exception $e) { echo "outer caught ", $e->getMessage(), "\n"; }
}
outer();
$t = new Thing;
echo "kept ", spl_object_id(Keep::$kept), ", next ", spl_object_id($t), "\n";
