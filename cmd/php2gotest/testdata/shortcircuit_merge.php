<?php
// A short-circuit `&&` or `||` whose result is **assigned to a variable inside
// a function** was silently answering the LEFT operand.
//
// The lowering lands the result in a compiler temporary and writes it into the
// variable at the merge block: `write_var($flag, $__t20)`. `liveIn` treated a
// store as a definition of its place and skipped the rest of the instruction,
// so the *stored value* -- itself a tracked place -- never counted as a use.
// Pruned SSA then placed no block parameter at the merge, and the promotion
// read whichever version reached first. Only inside a function, because
// `{main}` is never promoted, and only with promotion on.
function andChain(string $sep, string $c): bool {
    $flag = $sep !== '' && strpos($sep, $c) !== false;
    return $flag;
}
var_dump(andChain(" \t", "h"), andChain(" \t", " "), andChain("", "h"));

function orChain(?int $a, ?int $b): bool {
    $flag = $a === null || $b === null;
    return $flag;
}
var_dump(orChain(1, 2), orChain(null, 2), orChain(1, null));

// Three-term chains, and a mix of the two operators.
function mixed(int $n, string $s): bool {
    $flag = $n > 0 && $s !== '' || $n < 0;
    return $flag;
}
var_dump(mixed(1, "x"), mixed(1, ""), mixed(-1, ""), mixed(0, "x"));

// The real shape this came from: ucwords' inner loop, where the flag decides
// the next iteration's behaviour.
function words(string $string, string $separators = " \t\r\n\f\v"): string {
    $length = strlen($string);
    $atWordStart = true;
    for ($index = 0; $index < $length; $index++) {
        $character = $string[$index];
        if ($atWordStart) {
            $byte = ord($character);
            if ($byte >= 97 && $byte <= 122) { $string[$index] = chr($byte - 32); }
        }
        $atWordStart = $separators !== '' && strpos($separators, $character) !== false;
    }
    return $string;
}
var_dump(words("hello world"), words("a-b c"), words("a|b c", "|"));
echo "done\n";
