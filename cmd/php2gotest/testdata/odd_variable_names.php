<?php
// A PHP variable name is an **arbitrary byte string**. `${"b\nb\n d"}` is legal,
// and spelling one raw in the IR put a newline in the middle of an operand,
// where no scanner could find its end again. The IR quotes such a name --
// `${"..."}` -- and the scanner matches that form *before* it blanks string
// literals, which would otherwise erase the name it is looking for.

${"b\nb\n d"} = "newlines";
var_dump(${"b\nb\n d"});

${' leading space'} = "spaced";
var_dump(${' leading space'});

${'has"quote'} = "quoted";
var_dump(${'has"quote'});

${'has$dollar'} = "dollar";
var_dump(${'has$dollar'});

${""} = "empty name";
var_dump(${""});

${'0'} = "digit";
var_dump(${'0'});

// Reading one that was never set is an ordinary undefined variable, and the
// warning quotes the **real** name rather than the operand's spelling.
var_dump(@${"never set"});
var_dump(${"never set"});

// The name reaches an argument list as well as an operand, which is the other
// place an operand can live.
function takes($v) { return "got:" . $v; }
${'arg name'} = "value";
var_dump(takes(${'arg name'}));
var_dump(${'arg name'} . "|" . ${' leading space'});

// A computed name and a quoted one name the same slot.
$n = 'has$dollar';
var_dump($$n);
$$n = "written through the map";
var_dump(${'has$dollar'});
