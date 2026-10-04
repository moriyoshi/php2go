<?php
// A constant missing from an engine class the program can use is the
// engine's `Undefined constant C::K` -- the class exists -- while a class
// nothing declares is `Class "C" not found`.

try { var_dump(Exception::NOPE); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { var_dump(RuntimeException::ALSO_NOPE); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { var_dump(ArrayIterator::NOT_A_FLAG); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { var_dump(Nope::X); } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump(ArrayIterator::ARRAY_AS_PROPS);
