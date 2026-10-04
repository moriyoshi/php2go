<?php
// An exception whose file its constructor emptied: the uncaught heading
// keeps the empty file, `Uncaught a in :0`, and the `thrown in` line says
// `Unknown` (bug64821.3).
class a extends Exception {
    public function __construct() {
        $this->line = 0;
        $this->file = "";
    }
}
throw new a;
