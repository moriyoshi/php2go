<?php
// An unterminated INI section reports the innermost construct still open
// when the input ended -- a variable, its fallback, a quoted string --
// and only expects ']' once every token inside it closed. Its tokens stop
// at the first line end outside double quotes and at a `;` comment.
// A fallback may hold a variable, and operators in it are plain text.

$cases = [
    '[abc', '[${zz', '[${zz:-x', '[${zz}', '[${zz:-x}', '["abc', "[a\n",
    "[\${zz:-x\n\n", '[${', '[${zz:', '[${zz:-', '[${zz:-${q', '[${zz:-${q}',
    '[${zz:-${', '[a${zz', "[a\"b\"", "['q", '[${zz:-"q', "[\${zz\nq=1",
    "['q\na=1]", '[${  ', "\n[a", "[a\n\n\nb=1", "x=1\n[b\nc", "[a b\nc d\ne",
    "[a\"b\nc\"", "[a\n\"b\nc\"d", "[a\n\"b", "[\${zz:-a\n\"b", "[\"a\nb",
    "[\${zz:-a\nb", "[\${zz\nb", '[a=b', '[a|b', '[a;b', '[a;${zz', '[a|${zz',
    '[a=${zz', '[\'q${zz', "[\${zz:-\"q\nr",
    // Fallbacks in a terminated context.
    'a=${', 'a=${  ', 'a=${zz:-${q}}', 'a=${zz:-"q}"}', 'a=${zz:-x|y}',
    'a=${zz:-x=y}', 'a=${zz:-x]y}', 'a=${zz:-}', '[${zz:-${q}}]',
    'a=${zz:-a b}', 'a=${zz:- a }', 'a=${zz:-true}', "a=\${zz:-x\nb=1",
    '[${zz:-x|y}]',
];
foreach ($cases as $c) {
    echo json_encode($c), " => ";
    $r = @parse_ini_string($c, true);
    echo json_encode($r), " ", error_get_last()['message'] ?? 'none', "\n";
    error_clear_last();
}

// The warning itself, as the corpus case prints it.
var_dump(parse_ini_string("[\${zz:-x\n\n"));

// Words in a fallback are constants, not the INI's reserved tokens.
foreach (['a=${zz:-off}', 'a=${zz:-null}', 'a=${zz:-x true}', 'a=${zz:-truex}',
          'a=${zz:-yes}', 'a=${zz:-none}', 'a=${zz:-true"q"}', 'a=${zz:-TRUE}',
          'a=${zz:-false}'] as $c) {
    foreach ([INI_SCANNER_NORMAL, INI_SCANNER_TYPED] as $mode) {
        echo json_encode($c), " $mode => ";
        $r = @parse_ini_string($c, true, $mode);
        echo json_encode($r), " ", error_get_last()['message'] ?? 'none', "\n";
        error_clear_last();
    }
}

// A fallback's unquoted run is one piece; only a whole constant name resolves.
foreach (['a=${zz:- true}', 'a=${zz:-true }', 'a=${zz:-PHP_EOL x}', 'a=${zz:-1 2}',
          'a=${zz:-x"q"true}', 'a=${zz:-true${q}}', 'a=${zz:-${q}true}',
          'a=${zz:-"q"PHP_EOL"r"}', 'a=${zz:-E_ALL|1}', 'a=${zz:-1.5e3}',
          'a=${zz:-PHP_EOL=}'] as $c) {
    echo json_encode($c), " => ";
    $r = @parse_ini_string($c, true);
    echo json_encode($r), " ", error_get_last()['message'] ?? 'none', "\n";
    error_clear_last();
}
var_dump(defined('true'), defined('nUll'), constant('False'), defined('\true'));
