#!/usr/bin/env php
<?php
// The CLI skips a shebang line, which still counts as a line and a byte run.
echo __LINE__, " ", __COMPILER_HALT_OFFSET__, "\n";
__halt_compiler();data
