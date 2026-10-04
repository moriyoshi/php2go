<?php
// preg_* over php2go's backtracking matcher: result shapes, flags, the
// empty-match loops, replacement references and the pattern warnings.

function show($label, $v) { echo $label, ': '; var_export($v); echo "\n"; }

// Result arrays: trailing unset groups dropped, middle ones "".
show('m1', [preg_match('/(a)(b)?(c)?/', 'a', $m), $m]);
show('m2', [preg_match('/(a)(b)?(c)/', 'ac', $m), $m]);
show('m3', [preg_match('/(?<x>a)(?P<y>b)?(?\'z\'c)?/', 'a', $m), $m]);
show('m4', [preg_match('/(a)(b)?(c)?/', 'a', $m, PREG_UNMATCHED_AS_NULL), $m]);
show('m5', [preg_match('/(b)(x)?(c)/', 'abc', $m, PREG_OFFSET_CAPTURE), $m]);
show('m6', [preg_match('/nope/', 'abc', $m), $m]);
show('offset', [preg_match('/b/', 'abcb', $m, PREG_OFFSET_CAPTURE, 2), $m]);
show('negoffset', [preg_match('/b/', 'abcb', $m, 0, -1), $m, preg_match('/b/', 'abcb', $m, 0, -10)]);
show('bigoffset', [preg_match('/b/', 'abcb', $m, 0, 10), preg_last_error(), preg_last_error_msg()]);

// Priority order: alternation, greedy, lazy, possessive, atomic.
show('alt', [preg_match('/(a|ab)(c|bcd)(d*)/', 'abcd', $m), $m]);
show('lazy', [preg_match('/<(.+?)>/', '<a><b>', $m), $m]);
show('greedy', [preg_match('/<(.+)>/', '<a><b>', $m), $m]);
show('ungreedy', [preg_match('/<(.+)>/U', '<a><b>', $m), $m]);
show('possessive', [preg_match('/a++b/', 'aaab'), preg_match('/a*+a/', 'aaa')]);
show('atomic', [preg_match('/(?>a+)b/', 'aaab'), preg_match('/(?>a|ab)c/', 'abc')]);
show('emptyloop', [preg_match('/(a|)*b/', 'aab', $m), $m]);
show('counted', [preg_match('/^a{2,3}$/', 'aaa'), preg_match('/^a{2}$/', 'aaa'), preg_match('/a{,2}/', 'a{,2}', $m), $m, preg_match('/x{a}/', 'x{a}')]);

// Anchors and the dollar rule.
show('dollar', [preg_match('/a$/', "a\n"), preg_match('/a$/D', "a\n"), preg_match('/a\z/', "a\n"), preg_match('/a\Z/', "a\n")]);
show('multiline', [preg_match_all('/^\w+$/m', "ab\ncd\n", $m), $m, preg_match('/a^/m', "a\n"), preg_match('/^$/m', "a\n"), preg_match_all('/^/m', "a\nb\n")]);
show('dot', [preg_match('/^.$/', "\n"), preg_match('/^.$/s', "\n"), preg_match('/^\N$/s', "\n")]);
show('anchored', [preg_match('/b/A', 'ab'), preg_match('/a/A', 'ab'), preg_match('/\Gb/', 'ab', $m, 0, 1)]);
show('boundary', [preg_match_all('/\bfo\w*/', 'foo food xfoo', $m), $m, preg_match('/\Bo\B/', 'foo')]);

// Lookaround and backreferences.
show('ahead', [preg_match('/\w+(?=!)/', 'hi there!', $m), $m, preg_match('/^(?!ab)\w+/', 'abc')]);
show('behind', [preg_match('/(?<=\$)\d+/', 'cost $42', $m), $m, preg_match('/(?<!a|bc)d/', 'bcd xd', $m, PREG_OFFSET_CAPTURE), $m]);
show('backref', [preg_match('/(\w)\1/', 'abccd', $m), $m, preg_match('/(?<q>["\'])(.*?)\k<q>/', 'say "hi" now', $m), $m]);
show('backref-i', [preg_match('/(a)\1/i', 'aA'), preg_match('/(a)\g{-1}/', 'aa'), preg_match('/(a)?b\1/', 'b')]);

// Case, classes, escapes.
show('caseless', [preg_match('/[a-z]+/i', 'ABC', $m), $m, preg_match('/\xc9/i', "\xe9"), preg_match('/(?i)a(?-i)b/', 'AB'), preg_match('/(?i:a)b/', 'Ab')]);
show('classes', [preg_match_all('/[^\d\s]+/', 'ab 12 c-d', $m), $m, preg_match('/[]a]/', ']'), preg_match('/[a\-z]/', '-')]);
show('posix', [preg_match_all('/[[:upper:][:digit:]]+/', 'abC1d', $m), $m, preg_match('/[[:^alpha:]]/', 'abc')]);
show('escapes', [preg_match('/\x41\101\t/', "AA\t"), preg_match('/\Qa.b\E/', 'a.b'), preg_match('/\Qa.b\E/', 'axb'), preg_match('/a\.b/', 'axb')]);
show('extended', [preg_match('/ a b # comment
    c /x', 'abc'), preg_match('/a[ ]b/x', 'a b')]);
show('newline', [preg_match_all('/\R/', "a\r\nb\nc\rd", $m), count($m[0])]);

// UTF-8.
show('utf', [preg_match('/^.$/u', 'é'), preg_match('/^.$/', 'é'), preg_match('/\w/u', 'é'), preg_match('/\w/', 'é'), preg_match('/É/iu', 'é')]);
show('utf-offsets', [preg_match('/b/u', 'éb', $m, PREG_OFFSET_CAPTURE), $m]);
show('badutf', [preg_match('/./u', "\xff"), preg_last_error(), preg_last_error_msg()]);
show('badoffset', [preg_match('/./u', 'éa', $m, 0, 1), preg_last_error()]);
show('ucp-word', [preg_match('/^\w$/u', "\u{0301}"), preg_match('/^\w$/u', "\u{203F}"), preg_match('/^\w$/u', "\u{0903}")]);
show('props', [preg_match_all('/\p{Lu}/u', 'aBcÉ', $m), $m, preg_match('/\p{Greek}/u', 'α')]);

// preg_match_all orders.
show('all-pattern', [preg_match_all('/(a)(b)?/', 'a ab', $m), $m]);
show('all-set', [preg_match_all('/(a)(b)?/', 'a ab', $m, PREG_SET_ORDER), $m]);
show('all-named', [preg_match_all('/(?<l>[a-z])(?<d>\d)?/', 'a1b', $m), $m]);
show('all-none', [preg_match_all('/(?<l>x)(y)/', 'abc', $m), $m]);
show('all-empty', [preg_match_all('/x*/', 'ab', $m, PREG_OFFSET_CAPTURE), $m]);
show('all-utf-empty', [preg_match_all('//u', 'éa', $m, PREG_OFFSET_CAPTURE), $m]);

// preg_replace.
show('refs', preg_replace('/(a)(b)?/', '[$2|\1|${1}x|$9|\\\\1|\$1|$]', 'a ab'));
show('limit', [preg_replace('/a/', 'b', 'aaa', 2, $c), $c]);
show('empty', [preg_replace('/x*/', '-', 'abc'), preg_replace('/x*/u', '-', 'aé'), preg_replace('/b*/', '-', 'abc')]);
show('arrays', preg_replace(['/a/', '/b/'], ['b', 'c'], ['k' => 'ab', 'z' => 'xa']));
show('arrays2', preg_replace(['/a/', '/b/'], 'X', 'abc'));
show('arrays3', preg_replace(['/a/', '/b/'], ['1'], 'abc'));
show('nomatch', preg_replace('/q/', 'z', 'abc'));
show('badutf-replace', [preg_replace('/a/u', 'b', "a\xff"), preg_last_error()]);

// preg_replace_callback.
show('cb', [preg_replace_callback('/\d+/', fn($m) => $m[0] * 2, 'a1b22', -1, $c), $c]);
show('cb-groups', preg_replace_callback('/(a)(b)?(c)?/', function ($m) { return count($m) . ':' . implode(',', $m); }, 'a ab'));
show('cb-flags', preg_replace_callback('/(a)(b)?/', fn($m) => json_encode($m), 'a', -1, $c, PREG_OFFSET_CAPTURE | PREG_UNMATCHED_AS_NULL));
show('cb-limit', preg_replace_callback('/./', fn($m) => strtoupper($m[0]), 'abc', 2));
$n = 0;
show('cb-closure', [preg_replace_callback('/x/', function () use (&$n) { return ++$n; }, 'xaxbx'), $n]);

// preg_split.
show('split', preg_split('/,/', 'a,b,,c'));
show('split-noempty', preg_split('/,/', 'a,b,,c,', -1, PREG_SPLIT_NO_EMPTY));
show('split-limit', [preg_split('/x/', 'axbxc', 2), preg_split('/x/', 'axbxc', 1), preg_split('/x/', 'axbxc', 0)]);
show('split-delim', preg_split('/(,)(x)?/', 'a,b,xc', -1, PREG_SPLIT_DELIM_CAPTURE));
show('split-offset', preg_split('/(,)/', 'a,b,,c', -1, PREG_SPLIT_NO_EMPTY | PREG_SPLIT_DELIM_CAPTURE | PREG_SPLIT_OFFSET_CAPTURE));
show('split-empty', [preg_split('//', 'ab'), preg_split('//', 'ab', -1, PREG_SPLIT_NO_EMPTY), preg_split('//u', 'éa', -1, PREG_SPLIT_NO_EMPTY)]);
show('split-lead', preg_split('/\d*/', 'a1b'));

// preg_quote.
show('quote', [preg_quote('a/b#c.(d)'), preg_quote('a/b', '/'), preg_quote("a\0b")]);

// Pattern warnings answer false and record an internal error.
show('w1', [preg_match('foo', 'bar'), preg_last_error_msg()]);
show('w2', preg_match('', 'bar'));
show('w3', preg_match('   ', 'bar'));
show('w4', preg_match('/abc', 'bar'));
show('w5', preg_match('(abc', 'bar'));
show('w6', preg_match('/abc/Q', 'bar'));
show('w7', preg_match('/abc/e', 'bar'));
show('w8', preg_replace('/abc/Q', 'x', 'abc'));
show('w9', preg_split('\\a\\', 'abc'));
show('ok-brackets', [preg_match('(a(b)c)', 'abc', $m), $m, preg_match('{a}i', 'A'), preg_match("  /a/ i\n", 'A'), preg_match('[a[b]]', 'a[b]')]);
show('reset', [preg_match('/a/', 'a'), preg_last_error()]);

try {
    preg_match('/(a)/', 'a', $m, 3);
} catch (ValueError $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    preg_replace('/a/', ['x'], 'a');
} catch (TypeError $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
