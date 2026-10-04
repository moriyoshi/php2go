<?php
var_dump(new Exception("x") instanceof Throwable);
var_dump(new Error("y") instanceof Throwable);
var_dump(new stdClass instanceof Throwable);
class MyEx extends RuntimeException {}
var_dump(new MyEx("z") instanceof Throwable, new MyEx("z") instanceof Exception, new MyEx("z") instanceof Error);
try { throw new RuntimeException("r"); } catch (Throwable $t) { echo get_class($t), "\n"; }
