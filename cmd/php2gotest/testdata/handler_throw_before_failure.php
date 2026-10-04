<?php
// An error handler that throws on a warning throws **at** the warning: when
// the operation that warned would go on to fail in its own right -- an
// undefined variable used as a class name, a callee, a receiver -- the
// catch sees the handler's exception, not the later failure.

set_error_handler(function (int $no, string $msg) { throw new Exception($msg); });

try { new $undef(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { $undef(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { $undef::m(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { $x = clone $undef; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "done\n";
