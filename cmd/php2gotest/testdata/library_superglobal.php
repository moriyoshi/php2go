<?php
// The response-header functions are PHP in the bundled library, and share their
// state through a superglobal only php2go defines (`$_RESP_HEADERS`). Each of
// them names it as an ordinary variable, so a compiled body has to bind it to
// the global -- and a program on the engine never sees it in `$GLOBALS`.

header("X-First: 1");
header("X-Second: 2");
header("X-First: replaced");
var_dump(headers_list());
header_remove("X-Second");
var_dump(headers_list());
var_dump(in_array("_RESP_HEADERS", array_keys($GLOBALS), true));
