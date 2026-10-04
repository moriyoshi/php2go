<?php
// A program that throws an exception something else still holds: the catch
// variable is then not the exception's only holder, so overwriting it frees
// nothing. (The release claim is program-wide: one such `throw` anywhere
// refuses it everywhere.)
$saved = new Exception("saved");
try { throw $saved; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
try { throw new Exception("fresh"); } catch (Exception $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass);
var_dump($saved->getMessage());

function thrower(Exception $x) { throw $x; }
$held = new LogicException("held");
try { thrower($held); } catch (Exception $f) { echo $f->getMessage(), "\n"; }
try { throw new Exception("again"); } catch (Exception $f) { echo $f->getMessage(), "\n"; }
var_dump(new stdClass);
