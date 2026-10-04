<?php
// highlight_string and zend_version with literal arguments are answered at
// build time by the linked engine: the source is tokenized and coloured by
// the same tokenizer and the same highlight.* defaults the engine uses.
// The first two read locals written with a literal just before, with nothing
// in between that could run program code.
$src = "<?php class A { public \$p = 1; } // c\n";
$src2 = "<?php\nif (true) { echo \"s\"; }\n?>\ntail <b>html</b>";
$h1 = highlight_string($src, true);
$h2 = highlight_string($src2, true);
var_dump($h1, $h2);

var_dump(highlight_string("<?php echo 1 + 2; # hash\n/* block */ \$x = 'q';", true));
var_dump(highlight_string("", true));
var_dump(highlight_string("no php here at all", true));
var_dump(highlight_string("<?php \n 09 09 09;", true));
var_dump(highlight_string("<?php echo <<<EOT\n  heredoc \$v\n  EOT;\n", true));

// Printing mode: the output comes out where the call is, and the result is
// true.
$r = highlight_string("<?php function f(\$a) { return \$a; }");
echo "\n";
var_dump($r);
highlight_string("<?php 1;", false);
echo "\n";

// Into an output buffer, like any other output.
ob_start();
highlight_string("<?php 'buffered';");
$buf = ob_get_clean();
var_dump($buf);

var_dump(zend_version());
var_dump(is_string(zend_version()));

// A literal argument inside a function folds the same way.
function colour(): string {
    return highlight_string("<?php return [1, 'two'];", true);
}
var_dump(colour());
