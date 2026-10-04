package bundlebuild_test

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moriyoshi/php2go/internal/bundlebuild"
	"github.com/moriyoshi/php2go/internal/zend"
	"github.com/moriyoshi/php2go/runtime/bundle"
	"github.com/moriyoshi/php2go/runtime/php"
)

func TestHybridBundleCompilesComputedEvalAtRuntime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	entry := filepath.Join(root, "main.php")
	mustWrite(t, entry, `<?php
function changeLocal() {
    $x = 4;
    $code = '$x += ' . getenv('P2G_HYBRID_ADD') . '; return $x;';
    echo eval($code), ':', $x, "\n";
}
changeLocal();
`)
	output := filepath.Join(t.TempDir(), "bundle")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "hybrid")
	build := exec.Command("go", "build", "-tags", "php2go_hybrid", "-o", binary, ".")
	build.Dir = output
	build.Env = append(os.Environ(), "GOWORK=off")
	if log, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building tagged bundle: %v\n%s", err, log)
	}
	for _, add := range []string{"3", "8"} {
		t.Setenv("P2G_HYBRID_ADD", add)
		run := exec.Command(binary)
		run.Env = append(os.Environ(), "P2G_HYBRID_ADD="+add)
		got, err := run.CombinedOutput()
		if err != nil {
			t.Fatalf("running tagged bundle with %s: %v\n%s", add, err, got)
		}
		engine, err := zend.Start()
		if err != nil {
			t.Fatal(err)
		}
		want, evalErr := engine.Eval(fmt.Sprintf("require %q;", entry))
		engine.Close()
		if evalErr != nil {
			t.Fatal(evalErr)
		}
		if string(got) != want {
			t.Fatalf("tagged bundle with %s = %q, Zend = %q", add, got, want)
		}
	}
}

func TestEmbeddedProjectMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	copyTarget := filepath.Join(t.TempDir(), "copied.txt")
	mustWrite(t, filepath.Join(root, "main.php"),
		fmt.Sprintf("<?php require_once __DIR__ . '/vendor/autoload.php'; "+
			"var_dump(is_countable(value: []), is_countable(42)); "+
			"$generator = (function () { yield 1; })(); "+
			"var_dump(is_iterable(value: []), is_iterable($generator), is_iterable(42)); "+
			"$values = [2 => 'x', 'k' => 'yy', 9 => 'zzz']; "+
			"$lengthTwo = fn($value, $key) => strlen($value) === 2; "+
			"var_dump(array_find(array: $values, callback: $lengthTwo), "+
			"array_find_key(array: $values, callback: $lengthTwo), "+
			"array_any([], fn() => true), array_all([], fn() => false)); "+
			"var_dump(doubleval(value: '1.25'), join(separator: ['a', 'b']), "+
			"join(separator: '-', array: ['a', 'b']), key_exists(key: 'x', array: ['x' => null]), "+
			"strchr(haystack: 'abcabc', needle: 'b', before_needle: true)); "+
			"$caseInput = ['A' => 1, 'a' => 2, 7 => 3]; "+
			"var_dump(array_change_key_case(array: $caseInput, case: 2)); "+
			"$base = [0 => 'zero', 'x' => ['a' => 1, 0 => 'old'], 9 => 'nine']; "+
			"$replacement = [0 => 'ZERO', 'x' => ['b' => 2, 0 => 'new']]; "+
			"var_dump(array_replace($base, $replacement), array_replace_recursive($base, $replacement)); "+
			"$natural = ['a' => 'img12', 'b' => 'img2', 'c' => 'IMG1']; $naturalCase = $natural; "+
			"var_dump(natsort(array: $natural), $natural, natcasesort(array: $naturalCase), $naturalCase); "+
			"var_dump(str_rot13(string: 'Abc-XYZ-123'), fpow(num: 2.0, exponent: 3.0)); "+
			"$binary = hex2bin('61626100ff'); "+
			"$used = count_chars(string: $binary, mode: 1); "+
			"var_dump($used, bin2hex(count_chars(string: $binary, mode: 3))); "+
			"try { count_chars('', 5); } catch (ValueError $e) { echo $e->getMessage(), \"\\n\"; } "+
			"var_dump(strspn(string: 'abc123abc', characters: 'abc', offset: -3), "+
			"strcspn(string: 'abc123abc', characters: 'abc', offset: 3, length: 3), "+
			"strspn(string: 'abc123abc', characters: 'abc', offset: -99)); "+
			"var_dump(decbin(num: -2), decoct(num: PHP_INT_MIN), dechex(num: -1)); "+
			"set_error_handler(function ($level, $message) { "+
			"echo $level, ':', $message, \"\\n\"; return true; }, E_DEPRECATED); "+
			"var_dump(bindec(binary_string: '10x1'), hexdec(hex_string: '8000000000000000'), "+
			"octdec(octal_string: '1000000000000000000000')); "+
			"var_dump(base_convert(num: 'ffffffffffffffff', from_base: 16, to_base: 10), "+
			"base_convert(num: 'zzzzzzzzzzzz', from_base: 36, to_base: 10)); "+
			"restore_error_handler(); "+
			"try { base_convert('10', 1, 2); } catch (ValueError $e) { "+
			"echo $e->getMessage(), \"\\n\"; } "+
			"for ($imageType = 0; $imageType <= 20; $imageType++) { "+
			"var_dump(image_type_to_extension(image_type: $imageType), "+
			"image_type_to_extension(image_type: $imageType, include_dot: false), "+
			"image_type_to_mime_type(image_type: $imageType)); } "+
			"foreach (['0.0.0.0', '127.0.0.1', '255.255.255.255', '1.2.3.4', "+
			"'1.2.3', '01.2.3.4', '1.2.3.256', '1.2.+3.4', ''] as $ip) { "+
			"var_dump(ip2long(ip: $ip)); } "+
			"foreach ([PHP_INT_MIN, -4294967297, -4294967296, -1, 0, 1, "+
			"4294967295, 4294967296, PHP_INT_MAX] as $ip) { var_dump(long2ip(ip: $ip)); } "+
			"foreach (['abc=20def', '=00=ff', \"abc= \\t\\r\\ndef\", 'abc= \\tX', "+
			"'=G1', '=1', '=', '==41'] as $encoded) { "+
			"var_dump(bin2hex(quoted_printable_decode(string: $encoded))); } "+
			"$allBytes = ''; for ($byte = 0; $byte < 256; $byte++) { $allBytes .= chr($byte); } "+
			"foreach ([$allBytes, str_repeat('A', 73) . chr(0), str_repeat('A', 75) . ' ', "+
			"\"a  \\r\\nb\", \"a\\t \\r\\nb\", \"a \\rb\"] as $input) { "+
			"$encoded = quoted_printable_encode(string: $input); "+
			"var_dump(bin2hex($encoded), bin2hex(quoted_printable_decode($encoded))); } "+
			"foreach (['610062', '3d20', '3d09', '3d2009', '3d0d58', '3d0a58', "+
			"'3d200d0a58', '3d58', '3d3d'] as $hex) { "+
			"var_dump(bin2hex(quoted_printable_decode(hex2bin($hex)))); } "+
			"foreach (['', '-1', 'Euler', 'Pfister', 'Ashcraft', 'Tymczak', 'Honeyman', "+
			"'BP', 'BHP', 'B-P', '1Robert', 'éclair'] as $value) { "+
			"var_dump(soundex(string: $value)); } "+
			"foreach (['Az', 'aZ', 'A9', 'a9', 'Zz', 'zZ', '9z', '9Z', '5e6', "+
			"'0', '9', 'ZZ', 'aA0', 'Aa0', '1000'] as $value) { "+
			"var_dump(str_increment(string: $value)); } "+
			"foreach (['Az', 'aZ', 'A9', 'a9', 'Za', 'zA', 'Z0', 'z0', 'Aa', 'aA', "+
			"'A0', 'a0', '10', '1A', '1a', '10a', '5e6', '1000'] as $value) { "+
			"var_dump(str_decrement(string: $value)); } "+
			"foreach (['', '-cc', 'é', 'foo1.txt'] as $value) { "+
			"foreach (['str_increment', 'str_decrement'] as $function) { try { "+
			"var_dump($function === 'str_increment' ? str_increment($value) : str_decrement($value)); "+
			"} catch (ValueError $e) { echo $e->getMessage(), \"\\n\"; } } } "+
			"foreach (['0', '00', '0A', 'a', 'A'] as $value) { try { str_decrement($value); "+
			"} catch (ValueError $e) { echo $e->getMessage(), \"\\n\"; } } "+
			"foreach (['', '5c', '5c78', '5c7847', '5c7831', '5c7831325a', '5c583431', "+
			"'5c38', '5c343030', '5c373737', '5c31323334', '5c3038', '5c63', '615c5c62'] "+
			"as $hex) { var_dump(bin2hex(stripcslashes(string: hex2bin($hex)))); } "+
			"foreach (['', 'bar', 'A..z', hex2bin('002e2e1f'), hex2bin('002e2eff'), 'ae..pz'] "+
			"as $characters) { var_dump(bin2hex(addcslashes(string: $allBytes, characters: $characters))); } "+
			"$warningHandler = function ($level, $message, $file, $line) { "+
			"echo 'W:', $level, ':', $message, ':', basename($file), ':', $line, \"\\n\"; return true; }; "+
			"set_error_handler($warningHandler, E_WARNING); "+
			"foreach (['..', '..a', 'a..', 'z..A', 'a..b..c', 'a...'] as $characters) { "+
			"var_dump(bin2hex(addcslashes(\"zoo['.']\" . hex2bin('00ff'), $characters))); } "+
			"foreach ([0, 1, 2, 3, 44, 45, 46, 89, 90, 91, 256] as $length) { "+
			"$input = substr($allBytes, 0, $length); $encoded = convert_uuencode(string: $input); "+
			"var_dump(bin2hex($encoded), bin2hex(convert_uudecode(string: $encoded))); } "+
			"$uu = convert_uuencode('not very sophisticated'); "+
			"var_dump(convert_uudecode(''), convert_uudecode(substr($uu, 0, -10)), "+
			"convert_uudecode(\"M86%%A86%%A86%%A86%%A86%%A86%%A86%%A86%%A86%%A86%%A86%%A86%%A86%%A86%%A86%%A\\na.\"), "+
			"bin2hex(convert_uudecode('!@#$%%^YUGFDFGHJKLUYTFBNMLOYT'))); "+
			"restore_error_handler(); "+
			"$versions = ['', '.', '..', '1', '2', '10', '1.0', '1.0.0', '1.2.', '1.2..', "+
			"'1-2', '1_2', '1+2', '1.0-dev', '1.0a1', '1.0b1', '1.0RC1', '1.0rc1', "+
			"'1.0pl1', 'foo', 'foo1', '1foo', '#N#', '1' . chr(0) . '.9', '9223372036854775808']; "+
			"foreach ($versions as $left) { foreach ($versions as $right) { "+
			"var_dump(version_compare(version1: $left, version2: $right)); } } "+
			"foreach (['<', 'lt', '<=', 'le', '>', 'gt', '>=', 'ge', '==', '=', 'eq', "+
			"'!=', '<>', 'ne'] as $operator) { var_dump(version_compare('1.0RC1', '1.0', $operator)); } "+
			"try { version_compare('1', '2', '??'); } catch (ValueError $e) { "+
			"echo $e->getMessage(), \"\\n\"; } "+
			"foreach (['', 'valid phrase', 'AE GN KN PN WH WR X', "+
			"'SCHW school Christ CIA SCI DGE DGI DGY GH GN GNED PH SH TH TCH X', "+
			"'They fell forward, grovelling heedlessly on the cold earth.', "+
			"'A' . chr(0) . 'X', hex2bin('ff') . 'Alpha'] as $value) { "+
			"foreach ([0, 1, 2, 3, 6] as $max) { "+
			"var_dump(metaphone(string: $value, max_phonemes: $max)); } } "+
			"var_dump(metaphone(-1)); "+
			"try { metaphone('valid', -1); } catch (ValueError $e) { "+
			"echo $e->getMessage(), \"\\n\"; } "+
			"$deprecationHandler = function ($level, $message, $file, $line) { "+
			"echo 'D:', $level, ':', $message, ':', basename($file), ':', $line, \"\\n\"; return true; }; "+
			"set_error_handler($deprecationHandler, E_DEPRECATED); "+
			"utf8_encode('A'); utf8_decode('A'); restore_error_handler(); "+
			"var_dump(bin2hex(@utf8_encode($allBytes))); "+
			"foreach (['', 'c3a6', '41c23e42', 'e38022', '4198ba42e29843e298bae298', "+
			"'616263e0', 'c0af', 'eda080', 'f48fbfbf', 'f4908080', "+
			"'e08041', 'f0808041'] as $hex) { "+
			"var_dump(bin2hex(@utf8_decode(hex2bin($hex)))); } "+
			"$hebrevText = \"The hebrev function converts logical Hebrew text to visual text.\\n\" . "+
			"\"The function tries to avoid breaking words.\\n\"; "+
			"foreach ([-1, 0, 1, 15] as $width) { "+
			"var_dump(bin2hex(hebrev(string: $hebrevText, max_chars_per_line: $width))); } "+
			"$hebrewBinary = 'abc ' . hex2bin('e0e1e2') . ' ([]) def' . chr(0) . \"\\r\\n\"; "+
			"foreach ([0, 3, 8] as $width) { var_dump(bin2hex(hebrev($hebrewBinary, $width))); } "+
			"$urls = ['', ':', 'http:', 'http://', 'file:///c:/path', 'file:////c:/path', "+
			"'mailto:user@example.com', 'www.php.net:80/', '//user:pass@host:0/path?query#fragment', "+
			"'//host:', '//host:+12/path', '//[::1]:80/path', '/relative/path?x#y', "+
			"'http://ho' . chr(0) . 'st/pa' . chr(1) . 'th?q' . chr(127) . '#f' . hex2bin('ff')]; "+
			"foreach ($urls as $url) { var_dump(parse_url(url: $url)); } "+
			"foreach ([-2, PHP_URL_SCHEME, PHP_URL_HOST, PHP_URL_PORT, PHP_URL_USER, "+
			"PHP_URL_PASS, PHP_URL_PATH, PHP_URL_QUERY, PHP_URL_FRAGMENT] as $component) { "+
			"var_dump(parse_url('http://u:p@h:0/a?b#c', $component)); } "+
			"try { parse_url('http://host', 99); } catch (ValueError $e) { "+
			"echo $e->getMessage(), \"\\n\"; } "+
			"$quantityHandler = function ($level, $message, $file, $line) { "+
			"echo 'Q:', $level, ':', $message, ':', basename($file), ':', $line, \"\\n\"; return true; }; "+
			"set_error_handler($quantityHandler, E_WARNING); "+
			"foreach (['', '1', '0123', '-0b11', '0o14K', '0x1FM', '1 K', '1KM', "+
			"'1.0K', '0X', '0Z', '0x0x12', '9223372036854775808', "+
			"'-9223372036854775808', '18446744073709551616', "+
			"' 123' . chr(0) . 'K', '0' . chr(0) . 'x'] as $quantity) { "+
			"var_dump(ini_parse_quantity(shorthand: $quantity)); } "+
			"restore_error_handler(); "+
			"$replaceCount = -1; "+
			"var_dump(str_ireplace(search: ['', 'ab', 'a'], replace: ['ignored', 'X'], "+
			"subject: ['k' => 'ABabA', 9 => 'cab'], count: $replaceCount), $replaceCount); "+
			"try { str_ireplace('a', [], 'a'); } catch (TypeError $e) { "+
			"echo $e->getMessage(), \"\\n\"; } "+
			"enum BundleQuery: string { case Value = 'v v'; } "+
			"$queryCycle = ['keep' => 1]; $queryCycle['self'] =& $queryCycle; $queryCycle['tail'] = 2; "+
			"var_dump(http_build_query(data: [0 => ['a b' => 'x y~'], 'cycle' => &$queryCycle, "+
			"'enum' => BundleQuery::Value], numeric_prefix: 'N', arg_separator: ';', "+
			"encoding_type: PHP_QUERY_RFC3986)); "+
			"class BundleQueryVisibility { private string $private = 'p'; "+
			"protected string $protected = 'r'; public string $public = 'u'; "+
			"public function build(): string { return http_build_query($this); } } "+
			"var_dump((new BundleQueryVisibility())->build()); "+
			"$queryObject = new stdClass; $queryObject->{'0'} = 'zero'; $queryObject->{'01'} = 'lead'; "+
			"var_dump(http_build_query($queryObject, 'N')); "+
			"$binary = hex2bin('00ff'); "+
			"var_dump(rawurlencode(string: $binary), urlencode(string: 'a b~')); "+
			"var_dump(bin2hex(rawurldecode(string: '%%00%%ff+')), "+
			"bin2hex(urldecode(string: '%%00%%ff+')), rawurldecode(string: '%%2g%%+%%')); "+
			"$gc = ini_get('zend.enable_gc'); "+
			"var_dump(ini_alter(option: 'zend.enable_gc', value: '0'), ini_get('zend.enable_gc')); "+
			"ini_set('zend.enable_gc', $gc); "+
			"$chars = fopen('data:text/plain,abc', 'r'); "+
			"var_dump(fgetc(stream: $chars), ftell($chars)); fseek($chars, 0, SEEK_END); "+
			"var_dump(fgetc(stream: $chars), ftell($chars)); "+
			"$passthrough = fopen('data:text/plain,xyz', 'r'); echo 'P:'; "+
			"$passed = fpassthru(stream: $passthrough); echo ':', $passed, \"\\n\"; "+
			"$stream = fopen('data:text/plain,abcdef', 'r'); "+
			"var_dump(stream_get_contents(stream: $stream, length: 3, offset: 2), ftell($stream)); "+
			"try { stream_get_contents($stream, -2); } catch (ValueError $e) { "+
			"echo $e->getMessage(), \"\\n\"; } "+
			"$line = fopen('data:text/plain,abcENDdef', 'r'); "+
			"var_dump(stream_get_line($line, 20, 'END'), stream_get_line($line, 20, 'END'), feof($line)); "+
			"$line = fopen('data:text/plain,ENDtail', 'r'); "+
			"var_dump(stream_get_line($line, 0, 'END'), stream_get_line($line, 0)); "+
			"$line = fopen('data:text/plain,abcENDdef', 'r'); "+
			"var_dump(stream_get_line($line, 4, 'END'), stream_get_line($line, 4, 'END')); "+
			"try { stream_get_line($line, -1); } catch (ValueError $e) { "+
			"echo $e->getMessage(), \"\\n\"; } "+
			"try { stream_get_line(42, 3); } catch (TypeError $e) { "+
			"echo get_class($e), ':', $e->getMessage(), \"\\n\"; } "+
			"$closedLine = fopen('data:text/plain,a', 'r'); fclose($closedLine); "+
			"try { stream_get_line($closedLine, 3); } catch (TypeError $e) { "+
			"echo get_class($e), ':', $e->getMessage(), \"\\n\"; } "+
			"$from = fopen('data:text/plain,abcdef', 'r'); fread($from, 1); "+
			"$to = fopen(%q, 'w+'); "+
			"var_dump(stream_copy_to_stream(from: $from, to: $to, length: 3, offset: 2), ftell($from)); "+
			"rewind($to); var_dump(stream_get_contents($to)); fclose($to); unlink(%q); "+
			"require_once __DIR__ . '/vendor/./autoload.php'; echo \\Acme\\Answer::value(), \"\\n\";", copyTarget, copyTarget))
	mustWrite(t, filepath.Join(root, "vendor", "autoload.php"),
		"<?php require __DIR__ . '/acme/pkg/src/Answer.php';")
	mustWrite(t, filepath.Join(root, "vendor", "acme", "pkg", "src", "Answer.php"),
		"<?php namespace Acme; class Answer { public static function value() { return 42; } }")
	mustWrite(t, filepath.Join(root, "templates", "message.txt"), "bundled asset\n")
	mustWrite(t, filepath.Join(root, "vendor", "composer", "installed.json"), "[]\n")
	mustWrite(t, filepath.Join(root, ".git", "config"), "must not ship\n")

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	report, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Files != 5 || report.PHPFiles != 3 || !report.HasVendor {
		t.Fatalf("report = %+v, want five files, three PHP files and vendor", report)
	}
	launcher, err := os.ReadFile(filepath.Join(output, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(launcher, []byte("//go:embed all:bundle")) {
		t.Fatal("generated launcher does not embed the bundle")
	}

	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	index := app.Index()
	aliases := map[string]bool{}
	responseInitializer := false
	for _, library := range index.Userland {
		for _, initializer := range library.Initializers {
			if initializer == "PHP2Go\\Extension\\Standard\\register_superglobals_impl" {
				responseInitializer = true
			}
		}
		for _, function := range library.Functions {
			aliases[function.Name] = true
		}
	}
	if len(index.Userland) != 2 || !responseInitializer ||
		!aliases["dns_check_record"] || !aliases["checkdnsrr"] ||
		!aliases["dns_get_record"] || !aliases["dns_get_mx"] || !aliases["getmxrr"] ||
		!aliases["gethostbyaddr"] || !aliases["gethostbyname"] ||
		!aliases["gethostbynamel"] || !aliases["getprotobyname"] ||
		!aliases["getprotobynumber"] || !aliases["getservbyname"] || !aliases["getservbyport"] ||
		!aliases["net_get_interfaces"] ||
		!aliases["header"] || !aliases["header_register_callback"] ||
		!aliases["header_remove"] || !aliases["headers_list"] ||
		!aliases["headers_sent"] || !aliases["http_response_code"] ||
		!aliases["setcookie"] || !aliases["setrawcookie"] ||
		!aliases["array_all"] || !aliases["array_any"] ||
		!aliases["array_change_key_case"] || !aliases["array_find"] || !aliases["array_find_key"] ||
		!aliases["addcslashes"] || !aliases["array_diff_uassoc"] || !aliases["array_diff_ukey"] ||
		!aliases["array_intersect_uassoc"] || !aliases["array_intersect_ukey"] ||
		!aliases["array_replace"] || !aliases["array_replace_recursive"] || !aliases["array_udiff"] ||
		!aliases["array_udiff_assoc"] || !aliases["array_udiff_uassoc"] ||
		!aliases["array_uintersect"] || !aliases["array_uintersect_assoc"] ||
		!aliases["array_uintersect_uassoc"] || !aliases["array_walk_recursive"] ||
		!aliases["count_chars"] || !aliases["crypt"] || !aliases["convert_uudecode"] || !aliases["convert_uuencode"] ||
		!aliases["decbin"] || !aliases["dechex"] || !aliases["decoct"] || !aliases["doubleval"] ||
		!aliases["fpow"] || !aliases["hebrev"] || !aliases["hexdec"] || !aliases["http_build_query"] || !aliases["image_type_to_extension"] ||
		!aliases["image_type_to_mime_type"] || !aliases["inet_ntop"] || !aliases["inet_pton"] || !aliases["ini_alter"] || !aliases["ini_parse_quantity"] || !aliases["ip2long"] ||
		!aliases["is_countable"] || !aliases["is_iterable"] || !aliases["join"] ||
		!aliases["key_exists"] || !aliases["long2ip"] || !aliases["metaphone"] || !aliases["natcasesort"] || !aliases["natsort"] || !aliases["octdec"] ||
		!aliases["output_add_rewrite_var"] || !aliases["output_reset_rewrite_vars"] ||
		!aliases["parse_ini_file"] || !aliases["parse_ini_string"] || !aliases["parse_str"] ||
		!aliases["request_parse_body"] ||
		!aliases["parse_url"] || !aliases["password_algos"] || !aliases["password_get_info"] || !aliases["password_hash"] || !aliases["password_needs_rehash"] || !aliases["password_verify"] || !aliases["quoted_printable_encode"] || !aliases["rawurldecode"] ||
		!aliases["rawurlencode"] || !aliases["set_include_path"] || !aliases["soundex"] || !aliases["str_decrement"] ||
		!aliases["str_increment"] || !aliases["str_ireplace"] || !aliases["str_rot13"] || !aliases["strcspn"] ||
		!aliases["strchr"] || !aliases["stripcslashes"] || !aliases["strspn"] || !aliases["urldecode"] ||
		!aliases["urlencode"] || !aliases["uniqid"] || !aliases["strptime"] || !aliases["phpcredits"] || !aliases["utf8_decode"] || !aliases["utf8_encode"] || !aliases["version_compare"] || !aliases["fgetc"] ||
		!aliases["fprintf"] || !aliases["fpassthru"] || !aliases["get_include_path"] || !aliases["readlink"] || !aliases["getopt"] ||
		!aliases["escapeshellarg"] || !aliases["escapeshellcmd"] ||
		!aliases["stream_get_contents"] || !aliases["stream_get_line"] || !aliases["stream_copy_to_stream"] ||
		!aliases["fgetcsv"] || !aliases["fputcsv"] || !aliases["str_getcsv"] ||
		!aliases["strtok"] || !aliases["vfprintf"] {
		t.Fatalf("userland extension index = %+v, want standard and streams mappings", index.Userland)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, err := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
	asset, err := app.Source("/app/templates/message.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(asset) != "bundled asset\n" {
		t.Fatalf("asset = %q", asset)
	}
	if _, err := app.Source("/app/.git/config"); !os.IsNotExist(err) {
		t.Fatalf(".git/config lookup error = %v, want not-exist", err)
	}

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Log("Go tool is not exposed to this test environment; generated-module build not run")
		return
	}
	binary := filepath.Join(t.TempDir(), "app")
	build := exec.Command(goTool, "build", "-o", binary, ".")
	build.Dir = output
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if log, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building generated module: %v\n%s", err, log)
	}
	run := exec.Command(binary)
	binaryOutput, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("running generated binary: %v\n%s", err, binaryOutput)
	}
	if string(binaryOutput) != want {
		t.Fatalf("generated binary output = %q, engine output = %q", binaryOutput, want)
	}
}

func TestFormattingAndTokenizerUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	formattedTarget := filepath.Join(t.TempDir(), "formatted.txt")
	mustWrite(t, filepath.Join(root, "main.php"), fmt.Sprintf(`<?php
$warning = function ($level, $message, $file, $line) {
    echo "W:", $level, ":", $message, ":", basename($file), ":", $line, "\n";
    return true;
};
set_error_handler($warning, E_WARNING);
var_dump(strtok(","));
var_dump(strtok("", ","), strtok(","));
var_dump(strtok("a,,b,", ","), strtok(","), strtok(","), strtok(","));
var_dump(
    bin2hex(strtok("a\0b,c", "\0,")),
    bin2hex(strtok("\0,")),
    bin2hex(strtok("\0,")),
    strtok("\0,")
);
var_dump(strtok(",,,", ","), strtok(","));
restore_error_handler();

$stream = fopen(%q, "w+");
var_dump(fprintf($stream, "%%s:%%04d", "x", 7));
var_dump(vfprintf($stream, '%%2$s/%%1$d', [3, "y"]));
try {
    fprintf($stream, "%%s %%s", "x");
} catch (ArgumentCountError $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
}
try {
    fprintf($stream, "%%q", 1);
} catch (ValueError $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
}
try {
    vfprintf($stream, "%%s %%s", ["x"]);
} catch (ValueError $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
}
rewind($stream);
var_dump(fread($stream, 1000));
fclose($stream);
try {
    fprintf($stream, "x");
} catch (TypeError $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
}
unlink(%q);
$includePath = get_include_path();
var_dump($includePath, ini_get("include_path"));
var_dump(ini_set("include_path", "one:two"), get_include_path());
ini_set("include_path", $includePath);
`, formattedTarget, formattedTarget))

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestParseStrUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function parse_query_case(string $query): void {
    $result = ['old' => 1];
    var_dump(parse_str($query, $result), $result);
}

$tab = chr(9);
foreach ([
    '',
    '&',
    '&&a=1&&',
    'a',
    '=x',
    ' =x',
    '+=x',
    '.=x',
    'a=1=2',
    'a+b=c+d',
    'a.b=1&a b=2&a+b=3',
    'a=1&a=2',
    'a=1&a[]=2',
    'a[]=1&a=2',
    'a[0]=x&a[]=y',
    'a[1]=x&a[]=y',
    'a[-1]=x&a[]=y',
    'a[08]=x&a[+1]=y&a[-01]=z',
    '0=x&08=y&-1=z&+1=q',
    'a[][]=x&a[][]=y',
    'a[b][c]=x&a[b]=y',
    'a[b]=y&a[b][c]=x',
    'arr[1=sid&arr[4][2=fred',
    'arr1]=sid&arr[4]2]=fred',
    'a[ ]=x&a[ x]=y&a[' . $tab . ']=z',
    'a[b]tail=x&a[b][c]tail=y',
    '%61%00tail=x',
    'a=x%00y',
    '%00a=x',
    'a%2eb=x&a%20b=y',
    'x__Host-=1&x.__Host-=2&__Host-a=3&a[__Host-x]=4',
    'x__Secure-=1&x.__Secure-=2&__Secure-a=3&a[__Secure-x]=4',
    'a%5Bb%5D=x',
    'a%255Bb%255D=x',
    'a=%a&b=%41&c=%GG&d=+',
    'a=1;b=2',
    'a=1%26b=2',
    'a=1' . chr(0) . '&b=2',
] as $query) {
    parse_query_case($query);
}

$result = new stdClass;
var_dump(parse_str('a=1', $result), $result);
$result = null;
var_dump(parse_str('a=1', $result), $result);
$query = 'a=1';
var_dump(parse_str($query, $query), $query);

$nested = 'a';
for ($index = 0; $index < 65; $index++) {
    $nested .= '[x]';
}
parse_query_case($nested . '=v');

$parts = [];
for ($index = 0; $index < 1001; $index++) {
    $parts[] = 'k' . $index . '=v';
}
set_error_handler(function ($level, $message) {
    echo 'W:', $level, ':', $message, "\n";
    return true;
}, E_WARNING);
$result = [];
parse_str(implode('&', $parts), $result);
var_dump(count($result), isset($result['k999']), isset($result['k1000']));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestParseIniUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function ini_probe(
    string $source,
    bool $sections = false,
    int $mode = INI_SCANNER_NORMAL
): void {
    var_dump(parse_ini_string($source, $sections, $mode));
}

define('INI_PROBE_TEXT', 'hello');
define('INI_PROBE_INT', 42);
putenv('INI_PROBE_ENV=world');

$basic = "\xEF\xBB\xBF; comment\r\n" .
    "integer = 1\r\n" .
    "decimal = 1.5\n" .
    "leading = 01\n" .
    "enabled = true\n" .
    "disabled = none\n" .
    "nothing = null\n" .
    "constant = INI_PROBE_TEXT\n" .
    "number_constant = INI_PROBE_INT\n" .
    "unknown = UNKNOWN_NAME\n" .
    "single = 'single value'\n" .
    "double = \"double\\\" \\\\ \\$ value\"\n" .
    "bare = bare text\n" .
    "commented = value ; ignored\n" .
    "empty =\n";
foreach ([INI_SCANNER_NORMAL, INI_SCANNER_RAW, INI_SCANNER_TYPED] as $mode) {
    ini_probe($basic, false, $mode);
}

$typed = "a=00\nb=1.\nc=.5\nd=-1.5\ne=1e3\n" .
    "f=9223372036854775807\ng=9223372036854775808\n";
ini_probe($typed, false, INI_SCANNER_TYPED);

$sections = "root=before\n[first]\na=1\na=2\n" .
    "list[]=x\nlist[key]=y\n[10]\nz=3\n[first]\nb=4\n";
foreach ([false, true] as $process) {
    ini_probe($sections, $process, INI_SCANNER_TYPED);
}

$offsets = "a[]=x\na[]=y\na[0]=z\na[08]=lead\n" .
    "a[-1]=negative\na[+1]=plus\n1[]=numeric\n01[]=string\n" .
    "scalar=x\nscalar[]=y\n";
ini_probe($offsets);

$dollar = chr(36);
$variables =
    "a=" . $dollar . "{INI_PROBE_ENV}\n" .
    "b=" . $dollar . "{MISSING}\n" .
    "c=" . $dollar . "{MISSING:-fallback}\n" .
    "d=pre" . $dollar . "{INI_PROBE_ENV}post\n" .
    "e=\"x" . $dollar . "{INI_PROBE_ENV}y\"\n";
ini_probe($variables);
ini_probe($variables, false, INI_SCANNER_RAW);

foreach ([
    "a=1|2\n",
    "a=7&3\n",
    "a=7^3\n",
    "a=1|2&4\n",
    "a=(1|2)&4\n",
    "a=~1\nb=!0\nc=!1\n",
    "a=UNKNOWN_NAME|2\n",
    "a=1|>tail\n",
] as $expression) {
    ini_probe($expression);
    ini_probe($expression, false, INI_SCANNER_TYPED);
}

foreach ([
    'ini="ini;raw"',
    'ini="ini;raw',
    'ini=ini;raw',
    'ini=ini"raw',
    'a=INSTALL_ROOT "quoted"',
] as $raw) {
    ini_probe($raw, false, INI_SCANNER_RAW);
}

ini_probe("a=1\0b=2\nc=3\n");
ini_probe("a=b ");
ini_probe("a= ");
ini_probe("a=\"line1\nline2\"\n");

set_error_handler(function ($level, $message) {
    echo 'W:', $level, ':', str_replace("\n", "\\n", $message), "\n";
    return true;
}, E_WARNING);
var_dump(parse_ini_string("a=1\n", false, -1));
var_dump(parse_ini_string("a=b=c\n"));
foreach ([
    'a="b',
    'a=' . $dollar . '{b',
    'true=1',
    '[abc',
] as $malformed) {
    var_dump(parse_ini_string($malformed));
}
var_dump(parse_ini_file('php2go-missing-ini-probe.ini'));
restore_error_handler();

var_dump(parse_ini_file(
    __DIR__ . '/settings.ini',
    true,
    INI_SCANNER_TYPED
));
var_dump(file_get_contents(__DIR__ . '/settings.ini'));
var_dump(@parse_ini_file(__DIR__ . '/missing.ini'));
try {
    parse_ini_file('');
} catch (ValueError $error) {
    echo get_class($error), ':', $error->getMessage(), "\n";
}
`)
	mustWrite(t, filepath.Join(root, "settings.ini"), `root=1
[section]
decimal=1.5
list[]=a
list[key]=b
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}

func TestPackUnpackUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
set_error_handler(function ($level, $message) {
    echo 'W:', $level, ':', $message, "\n";
    return true;
}, E_WARNING);

function pack_probe(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

pack_probe('strings', fn() => array_map('bin2hex', [
    pack('a5', 'xy'),
    pack('A5', 'xy'),
    pack('Z5', 'xy'),
    pack('H5', '12af9'),
    pack('h5', '12af9'),
]));
pack_probe(
    'cursors',
    fn() => bin2hex(pack('C2X1C1x2@8C', 1, 2, 3, 4))
);
pack_probe('integers', fn() => array_map('bin2hex', [
    pack('cC', -1, 255),
    pack('sSnv', -2, 65534, 0x1234, 0x1234),
    pack(
        'iIlLNV',
        -2,
        4294967295,
        -2,
        4294967295,
        0x12345678,
        0x12345678
    ),
    pack('qQJP', -2, -1, -2, -2),
]));
pack_probe('floats', fn() => array_map('bin2hex', [
    pack('f', -0.0),
    pack('gG', 1.5, 1.5),
    pack('eE', INF, NAN),
]));
pack_probe('star', fn() => bin2hex(pack('C*x', 1, 2, 3)));
pack_probe('zero', fn() => bin2hex(pack(
    'C0a0A0Z0H0h0x0X0@0',
    99,
    'x',
    'x',
    'x',
    'f',
    'f'
)));
pack_probe('extra', fn() => bin2hex(pack('C', 1, 2)));
pack_probe('few', fn() => pack('C2', 1));
pack_probe('unknown', fn() => pack('B', 1));
pack_probe('bad hex', fn() => bin2hex(pack('H4', '1z')));
pack_probe('coercion', fn() => array_map('bin2hex', [
    pack('C4', true, null, '12x', 3.9),
    pack('a2', 42),
]));

$all = pack(
    'cCsSniIlLNVvqQJP',
    -1,
    255,
    -2,
    65534,
    0x1234,
    -2,
    4294967295,
    -2,
    4294967295,
    0x12345678,
    0x12345678,
    0x1234,
    -2,
    -1,
    -2,
    -2
);
pack_probe('unpack integers', fn() => unpack(
    'csc/Cuc/sns/Sus/nbe/iin/Iui/lln/Lul/Nn/Vv/vle/qs/Qq/Jj/Pp',
    $all
));
$floats = pack('gGfeEd', -0.0, 1.5, -2.25, INF, NAN, -INF);
pack_probe('unpack floats', fn() => unpack(
    'glo/Gbe/fnative/einf/Enan/dninf',
    $floats
));
pack_probe(
    'names',
    fn() => unpack('C2a/Cb/C2/C0z', hex2bin('0102030405'))
);
pack_probe(
    'duplicates',
    fn() => unpack('Ca/Ca/C1a', hex2bin('010203'))
);
pack_probe('strings unpack', fn() => unpack(
    'a4raw/A4trim/Z4zero/H5hi/h5lo',
    hex2bin('61002009666f6f200062617212af9012af90')
));
pack_probe(
    'offset',
    fn() => unpack('Cfirst/@3/Cfourth', hex2bin('0001020304'), 1)
);
pack_probe('bad offset', fn() => unpack('C', 'x', -1));
pack_probe('short', fn() => unpack('C2a', hex2bin('01')));
pack_probe('star short', fn() => unpack('C*a', hex2bin('01')));
pack_probe('at outside', fn() => unpack('@9/C', hex2bin('01')));
pack_probe('at star', fn() => unpack('@*/C', hex2bin('01')));
pack_probe('skip short', fn() => unpack('x3/C', hex2bin('0102')));
pack_probe('unknown unpack', fn() => unpack('B', hex2bin('01')));
pack_probe('overflow', fn() => unpack('C2147483648x', hex2bin('01')));
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}

func TestScanfUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	scanTarget := filepath.Join(t.TempDir(), "scanf.txt")
	source := strings.Replace(`<?php
function scan_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

scan_case(
    'basic',
    fn() => sscanf(
        'Part: Widget Serial: 123 Stock: 25',
        'Part: %s Serial: %d Stock: %d'
    )
);
scan_case(
    'strings chars',
    fn() => [
        sscanf("  abc def\tXYZ", '%s %3s %c'),
        sscanf(' ab cd', '%c%2c %c'),
        sscanf('12% x', '%d%% %s'),
    ]
);
scan_case(
    'sets',
    fn() => [
        sscanf('abc123]-XYZ', '%[a-z]%[0-9]%[]-]%[^a-z]'),
        sscanf('fed-]x', '%[f-a]%[-]]%c'),
        sscanf('xyz', '%[^a-c]'),
    ]
);
scan_case(
    'integers',
    fn() => sscanf(
        '-12 +12 012 0x12 129 -0129 1G',
        '%d %u %i %i %o %o %x'
    )
);
scan_case(
    'integer boundaries',
    fn() => [
        sscanf(
            '9223372036854775808 18446744073709551615 -1',
            '%d %u %u'
        ),
        sscanf(
            '-18446744073709551616 18446744073709551616 ' .
            '-9223372036854775809',
            '%u %u %d'
        ),
        sscanf('-0x12 +0x12 0x12', '%i %i %i'),
    ]
);
scan_case(
    'floats',
    fn() => sscanf(
        '-1.25 .5 1. 1e3 1e+ 1e-2 x',
        '%f %e %g %f %f %f %f'
    )
);
scan_case(
    'float rollback',
    fn() => [
        sscanf('1e', '%f%n'),
        sscanf('1e+', '%f%n'),
        sscanf('1e+x', '%f%n%s'),
        sscanf('-12.34e5 1e+2', '%5f %3f'),
    ]
);
scan_case(
    'suppress position',
    fn() => [
        sscanf('123 abc', '%*d %n%s%n'),
        sscanf('12', '%*d'),
        sscanf('', '%n'),
    ]
);
scan_case(
    'partial',
    fn() => [
        sscanf('12:x', '%d:%d:%s'),
        sscanf('12-x', '%d:%s'),
        sscanf('x', '%d%s'),
        sscanf('', '%s%d'),
        sscanf('ab', 'abc'),
        sscanf('xbc', 'abc'),
    ]
);
scan_case(
    'positional',
    fn() => [
        sscanf('one two', '%2$s %1$s'),
        sscanf('one', '%3$s'),
        sscanf('10 20', '%2$d %1$d'),
    ]
);
scan_case(
    'nul',
    fn() => [
        sscanf('a' . chr(0) . 'b', '%s'),
        sscanf('12 34', '%d' . chr(0) . '%d'),
    ]
);

$a = 'A';
$b = 'B';
$c = 'C';
scan_case('refs success', function () use (&$a, &$b, &$c) {
    $return = sscanf('12 x', '%d %s%n', $a, $b, $c);
    return [$return, $a, $b, $c];
});
$a = 'A';
$b = 'B';
$c = 'C';
scan_case('refs partial', function () use (&$a, &$b, &$c) {
    $return = sscanf('12:x', '%d:%d:%s', $a, $b, $c);
    return [$return, $a, $b, $c];
});
$a = 'A';
$b = 'B';
scan_case('refs underflow', function () use (&$a, &$b) {
    $return = sscanf('', '%s%d', $a, $b);
    return [$return, $a, $b];
});
$a = 'A';
$b = 'B';
scan_case('refs positional', function () use (&$a, &$b) {
    $return = sscanf('one two', '%2$s %1$s', $a, $b);
    return [$return, $a, $b];
});
$a = 'A';
scan_case('refs suppressed', function () use (&$a) {
    $return = sscanf('12 x', '%*d %s', $a);
    return [$return, $a];
});

foreach ([
    fn() => sscanf('x y', '%1$s %s'),
    fn() => sscanf('x y', '%1$s %1$s'),
    fn() => sscanf('x', '%0$s'),
    fn() => sscanf('x', '%256$s'),
    fn() => sscanf('x', '%'),
    fn() => sscanf('x', '%.a'),
    fn() => sscanf('x', '%[abc'),
] as $invalid) {
    scan_case('invalid', $invalid);
}
$a = 'A';
$b = 'B';
$c = 'C';
scan_case(
    'too many refs',
    fn() => sscanf('x y', '%s %s', $a, $b, $c)
);
scan_case(
    'too few refs',
    fn() => sscanf('x y', '%s %s', $a)
);

$path = __SCAN_PATH__;
file_put_contents($path, "12 alpha\n\n34 beta tail\nlast");
$stream = fopen($path, 'r');
scan_case('fscanf line1', fn() => fscanf($stream, '%d %s'));
scan_case('fscanf blank', fn() => fscanf($stream, '%s'));
$a = 'A';
$b = 'B';
scan_case('fscanf refs', function () use ($stream, &$a, &$b) {
    $return = fscanf($stream, '%d %s', $a, $b);
    return [$return, $a, $b, ftell($stream)];
});
scan_case('fscanf last', fn() => fscanf($stream, '%2c%n'));
scan_case('fscanf eof', fn() => fscanf($stream, '%s'));
fclose($stream);
scan_case('fscanf closed', fn() => fscanf($stream, '%s'));
scan_case('fscanf type', fn() => fscanf(42, '%s'));

file_put_contents($path, "one\ntwo\n");
$stream = fopen($path, 'r');
$a = 'A';
scan_case('fscanf error consumes', function () use ($stream, &$a) {
    try {
        fscanf($stream, '%s %s', $a);
    } catch (Throwable $error) {
        return [$error->getMessage(), ftell($stream), fgets($stream)];
    }
});
fclose($stream);
`, "__SCAN_PATH__", fmt.Sprintf("%q", scanTarget), 1)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}

func TestResolveIncludePathUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "inc1", "file"), "one")
	mustWrite(t, filepath.Join(root, "inc2", "file"), "two")
	hostTarget := filepath.Join(t.TempDir(), "host-target")
	mustWrite(t, hostTarget, "host")
	hostLink := filepath.Join(t.TempDir(), "host-link")
	if err := os.Symlink(hostTarget, hostLink); err != nil {
		t.Fatal(err)
	}
	source := strings.NewReplacer(
		"__HOST_TARGET__", fmt.Sprintf("%q", hostTarget),
		"__HOST_LINK__", fmt.Sprintf("%q", hostLink),
	).Replace(`<?php
function resolve_case(string $label, mixed $filename, string $root, string $host): void {
    echo $label, ":";
    try {
        $value = stream_resolve_include_path($filename);
        if (is_string($value)) {
            $value = str_replace([$root, $host], ["ROOT", "HOST"], $value);
        }
        var_dump($value);
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}

$root = __DIR__;
$host = __HOST_TARGET__;
$host_link = __HOST_LINK__;
$old_path = get_include_path();

set_include_path($root . "/inc1" . PATH_SEPARATOR . $root . "/inc2");
resolve_case("missing", "missing", $root, $host);
resolve_case("first", "file", $root, $host);
resolve_case("empty", "", $root, $host);
resolve_case("absolute", $root . "/inc2/file", $root, $host);
resolve_case("dot-segments", $root . "/inc1/../inc2/file", $root, $host);
resolve_case("directory", $root . "/inc1", $root, $host);
resolve_case("file-uri", "file://" . $root . "/inc1/file", $root, $host);
resolve_case("localhost-uri", "FILE://LOCALHOST" . $root . "/inc1/file", $root, $host);
resolve_case("data-uri", "data:text/plain,x", $root, $host);
resolve_case("host", $host, $root, $host);
resolve_case("host-symlink", $host_link, $root, $host);
resolve_case("host-file-uri", "file://" . $host_link, $root, $host);

set_include_path($root . "/inc2" . PATH_SEPARATOR . $root . "/inc1");
resolve_case("second", "file", $root, $host);
set_include_path("file://" . $root . "/inc1");
resolve_case("file-include-path", "file", $root, $host);
set_include_path($root . "/missing-dir");
resolve_case("script-fallback", "main.php", $root, $host);
set_include_path(PATH_SEPARATOR . $root);
resolve_case("empty-entry", "", $root, $host);

set_include_path($root);
set_error_handler(function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});
foreach ([null, false, true, 0, 1, 1.5, "main.php", [], new stdClass] as $index => $filename) {
    resolve_case("arg-" . $index, $filename, $root, $host);
}
resolve_case("unknown-wrapper", "zz://x", $root, $host);
resolve_case("one-char-scheme", "x://x", $root, $host);
resolve_case("invalid-scheme", "a_b://x", $root, $host);
resolve_case("dotdot-wrapper", "..://x", $root, $host);
set_include_path("zz://root");
resolve_case("unknown-path-fallback", "main.php", $root, $host);
set_include_path($root);
resolve_case("nul", "bad\0path", $root, $host);
echo "named:";
$named = stream_resolve_include_path(filename: "main.php");
var_dump(str_replace($root, "ROOT", $named));
try {
    stream_resolve_include_path("main.php", true);
} catch (Throwable $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
}
restore_error_handler();
set_include_path($old_path);
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestPathPredicatesUserlandMatchEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "readonly.txt"), "embedded")
	mustWrite(t, filepath.Join(root, "inc", "item.txt"), "item")
	if err := os.Chmod(filepath.Join(root, "readonly.txt"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "inc", "item.txt"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "inc"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "inc"), 0o755) })

	hostRoot := t.TempDir()
	hostPlain := filepath.Join(hostRoot, "plain")
	hostExecutable := filepath.Join(hostRoot, "executable")
	hostNone := filepath.Join(hostRoot, "none")
	hostDirectory := filepath.Join(hostRoot, "directory")
	hostFileLink := filepath.Join(hostRoot, "file-link")
	hostDirectoryLink := filepath.Join(hostRoot, "directory-link")
	hostBrokenLink := filepath.Join(hostRoot, "broken-link")
	hostMissing := filepath.Join(hostRoot, "missing")
	mustWrite(t, hostPlain, "plain")
	mustWrite(t, hostExecutable, "executable")
	mustWrite(t, hostNone, "none")
	if err := os.Mkdir(hostDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{
		hostPlain: 0o644, hostExecutable: 0o755, hostNone: 0o000,
	} {
		if err := os.Chmod(name, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(hostPlain, hostFileLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hostDirectory, hostDirectoryLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hostMissing, hostBrokenLink); err != nil {
		t.Fatal(err)
	}

	source := strings.NewReplacer(
		"__HOST_PLAIN__", fmt.Sprintf("%q", hostPlain),
		"__HOST_EXECUTABLE__", fmt.Sprintf("%q", hostExecutable),
		"__HOST_NONE__", fmt.Sprintf("%q", hostNone),
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", hostDirectory),
		"__HOST_FILE_LINK__", fmt.Sprintf("%q", hostFileLink),
		"__HOST_DIRECTORY_LINK__", fmt.Sprintf("%q", hostDirectoryLink),
		"__HOST_BROKEN_LINK__", fmt.Sprintf("%q", hostBrokenLink),
		"__HOST_MISSING__", fmt.Sprintf("%q", hostMissing),
	).Replace(`<?php
function predicate_case(string $function, string $label, mixed $filename): void {
    echo $function, ':', $label, ':';
    try {
        var_dump($function($filename));
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

$functions = [
    'file_exists', 'is_dir', 'is_executable', 'is_file',
    'is_link', 'is_readable', 'is_writable', 'is_writeable',
];
$paths = [
    'embedded-file' => __DIR__ . '/readonly.txt',
    'embedded-directory' => __DIR__ . '/inc',
    'embedded-item' => __DIR__ . '/inc/item.txt',
    'embedded-file-uri' => 'file://' . __DIR__ . '/readonly.txt',
    'embedded-localhost-uri' => 'FILE://LOCALHOST' . __DIR__ . '/readonly.txt',
    'host-plain' => __HOST_PLAIN__,
    'host-executable' => __HOST_EXECUTABLE__,
    'host-none' => __HOST_NONE__,
    'host-directory' => __HOST_DIRECTORY__,
    'host-file-link' => __HOST_FILE_LINK__,
    'host-directory-link' => __HOST_DIRECTORY_LINK__,
    'host-broken-link' => __HOST_BROKEN_LINK__,
    'host-file-uri' => 'file://' . __HOST_FILE_LINK__,
    'host-missing' => __HOST_MISSING__,
    'empty' => '',
    'nul' => "bad\0path",
    'data' => 'data:text/plain,x',
];

foreach ($functions as $function) {
    foreach ($paths as $label => $filename) {
        predicate_case($function, $label, $filename);
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});
$arguments = [null, false, true, 0, 1, 1.5, [], new stdClass, 'zz://x'];
foreach ($functions as $function) {
    foreach ($arguments as $index => $filename) {
        predicate_case($function, 'argument-' . $index, $filename);
    }
    echo $function, ':named:';
    var_dump($function(filename: __DIR__ . '/readonly.txt'));
    echo $function, ':arity:';
    try {
        $function(__DIR__ . '/readonly.txt', true);
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestPosixHostFactsUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	hostFile := filepath.Join(t.TempDir(), "host-file")
	hostMissing := filepath.Join(t.TempDir(), "missing")
	mustWrite(t, hostFile, "host")

	source := strings.NewReplacer(
		"__HOST_FILE__", fmt.Sprintf("%q", hostFile),
		"__HOST_MISSING__", fmt.Sprintf("%q", hostMissing),
	).Replace(`<?php
function fact_case(string $label, callable $callback): void {
    echo $label, ':';
    try {
        var_dump($callback());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

function usage_summary(mixed $usage): array {
    $integers = is_array($usage);
    $nonnegative = is_array($usage);
    if (is_array($usage)) {
        foreach ($usage as $value) {
            $integers = $integers && is_int($value);
            $nonnegative = $nonnegative && $value >= 0;
        }
    }
    return [
        is_array($usage), is_array($usage) ? count($usage) : -1,
        is_array($usage) ? array_keys($usage) : [],
        $integers, $nonnegative,
        is_array($usage) && $usage['ru_utime.tv_usec'] >= 0
            && $usage['ru_utime.tv_usec'] < 1000000,
        is_array($usage) && $usage['ru_stime.tv_usec'] >= 0
            && $usage['ru_stime.tv_usec'] < 1000000,
    ];
}

function disk_summary(string $path): array {
    $free = disk_free_space($path);
    $total = disk_total_space($path);
    $alias = diskfreespace($path);
    return [
        is_float($free), is_float($total), is_float($alias),
        $free >= 0.0, $total > 0.0, $free <= $total,
        is_float($alias) && $alias >= 0.0 && $alias <= $total,
    ];
}

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

foreach ([0, 1, 2, -1] as $mode) {
    fact_case('getrusage-' . $mode, fn() => usage_summary(getrusage($mode)));
}
fact_case('getrusage-named', fn() => usage_summary(getrusage(mode: 0)));
fact_case('getrusage-null', fn() => usage_summary(getrusage(null)));
fact_case('getrusage-float', fn() => usage_summary(getrusage(1.5)));
fact_case('getrusage-arity', fn() => getrusage(0, 1));

$host_file = __HOST_FILE__;
foreach ([
    'tmp' => '/tmp',
    'file' => $host_file,
    'embedded' => __DIR__ . '/main.php',
] as $label => $path) {
    fact_case('disk-' . $label, fn() => disk_summary($path));
}
foreach (['disk_free_space', 'disk_total_space', 'diskfreespace'] as $function) {
    fact_case($function . '-empty', fn() => $function(''));
    fact_case($function . '-missing', fn() => $function(__HOST_MISSING__));
    fact_case($function . '-data', fn() => $function('data:,x'));
    fact_case($function . '-nul', fn() => $function("bad\0path"));
    fact_case($function . '-array', fn() => $function([]));
    fact_case($function . '-arity', fn() => $function('/tmp', true));
}
fact_case(
    'disk-named',
    fn() => is_float(disk_free_space(directory: '/tmp'))
);
fact_case('disk-null', fn() => disk_free_space(null));

foreach ([
    ['file-A', $host_file, 'A'],
    ['file-ff', $host_file, chr(254 + (int) is_array(getrusage()))],
    ['file-nul-project', $host_file, "\0"],
    ['tmp-A', '/tmp', 'A'],
] as [$label, $path, $project]) {
    fact_case('ftok-' . $label, fn() => ftok($path, $project));
}
fact_case('ftok-repeat', fn() => ftok($host_file, 'A') === ftok($host_file, 'A'));
fact_case('ftok-missing', fn() => ftok(__HOST_MISSING__, 'A'));
fact_case('ftok-empty-project', fn() => ftok($host_file, ''));
fact_case('ftok-long-project', fn() => ftok($host_file, 'AB'));
fact_case('ftok-int-project', fn() => ftok($host_file, 65));
fact_case('ftok-nul-filename', fn() => ftok("bad\0path", 'A'));
fact_case(
    'ftok-named',
    fn() => ftok(filename: $host_file, project_id: 'A')
);
fact_case('ftok-missing-argument', fn() => ftok($host_file));
fact_case('ftok-arity', fn() => ftok($host_file, 'A', true));
fact_case('sys-getloadavg-absent', fn() => function_exists('sys_getloadavg'));
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestPathStatUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "embedded.txt"), "embedded")
	mustWrite(t, filepath.Join(root, "nested", "item.txt"), "item")

	hostRoot := t.TempDir()
	hostFile := filepath.Join(hostRoot, "file")
	hostDirectory := filepath.Join(hostRoot, "directory")
	hostLink := filepath.Join(hostRoot, "link")
	hostBroken := filepath.Join(hostRoot, "broken")
	hostMissing := filepath.Join(hostRoot, "missing")
	mustWrite(t, hostFile, "abcdef")
	if err := os.Chmod(hostFile, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(hostDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hostFile, hostLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hostMissing, hostBroken); err != nil {
		t.Fatal(err)
	}

	source := strings.NewReplacer(
		"__HOST_FILE__", fmt.Sprintf("%q", hostFile),
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", hostDirectory),
		"__HOST_LINK__", fmt.Sprintf("%q", hostLink),
		"__HOST_BROKEN__", fmt.Sprintf("%q", hostBroken),
		"__HOST_MISSING__", fmt.Sprintf("%q", hostMissing),
	).Replace(`<?php
function stat_case(string $function, string $label, mixed $path): void {
    echo $function, ':', $label, ':';
    try {
        var_dump($function($path));
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

function embedded_stat_summary(string $label, string $path, bool $file): void {
    echo 'embedded:', $label, ':';
    $stat = stat($path);
    $lstat = lstat($path);
    $fields = [
        fileatime($path), filectime($path), filegroup($path),
        fileinode($path), filemtime($path), fileowner($path),
        fileperms($path), filesize($path), linkinfo($path),
    ];
    var_dump([
        count($stat), count($lstat),
        array_keys($stat), array_keys($lstat),
        $stat[0] === $stat['dev'], $stat[12] === $stat['blocks'],
        $lstat[0] === $lstat['dev'], $lstat[12] === $lstat['blocks'],
        $stat['mode'] & 0170000, $lstat['mode'] & 0170000,
        filetype($path),
        $file ? [$stat['size'], filesize($path)] : is_int(filesize($path)),
        array_map('is_int', $fields),
    ]);
}

$functions = [
    'stat', 'lstat', 'fileatime', 'filectime', 'filegroup',
    'fileinode', 'filemtime', 'fileowner', 'fileperms',
    'filesize', 'filetype', 'linkinfo',
];
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});
$host_file = __HOST_FILE__;
$paths = [
    'file' => $host_file,
    'directory' => __HOST_DIRECTORY__,
    'link' => __HOST_LINK__,
    'broken' => __HOST_BROKEN__,
];
foreach ($paths as $label => $path) {
    foreach ($functions as $function) {
        stat_case($function, $label, $path);
    }
}
foreach ($functions as $function) {
    stat_case($function, 'file-uri', 'file://' . $host_file);
}

embedded_stat_summary('file', __DIR__ . '/embedded.txt', true);
embedded_stat_summary('directory', __DIR__ . '/nested', false);

foreach ($functions as $function) {
    stat_case($function, 'missing', __HOST_MISSING__);
}
foreach (['stat', 'lstat', 'filetype', 'linkinfo'] as $function) {
    stat_case($function, 'unknown-wrapper', 'zz://x');
    stat_case($function, 'data-wrapper', 'data:text/plain,x');
    stat_case($function, 'nul', "bad\0path");
}
foreach ([null, false, true, 0, 1, 1.5, [], new stdClass] as $index => $path) {
    stat_case('stat', 'argument-' . $index, $path);
    stat_case('linkinfo', 'argument-' . $index, $path);
}
foreach ($functions as $function) {
    echo $function, ':named:';
    if ($function === 'linkinfo') {
        var_dump($function(path: $host_file));
    } else {
        var_dump($function(filename: $host_file));
    }
    echo $function, ':arity:';
    try {
        $function($host_file, true);
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestReadlinkUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "embedded.txt"), "embedded")
	if err := os.Mkdir(filepath.Join(root, "embedded-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "embedded-dir", "item"), "item")

	hostRoot := t.TempDir()
	hostTarget := filepath.Join(hostRoot, "target")
	hostRelative := filepath.Join(hostRoot, "relative-link")
	hostAbsolute := filepath.Join(hostRoot, "absolute-link")
	hostBroken := filepath.Join(hostRoot, "broken-link")
	hostMissing := filepath.Join(hostRoot, "missing")
	mustWrite(t, hostTarget, "target")
	if err := os.Symlink("target", hostRelative); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hostTarget, hostAbsolute); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", hostBroken); err != nil {
		t.Fatal(err)
	}

	source := strings.NewReplacer(
		"__HOST_TARGET__", fmt.Sprintf("%q", hostTarget),
		"__HOST_RELATIVE__", fmt.Sprintf("%q", hostRelative),
		"__HOST_ABSOLUTE__", fmt.Sprintf("%q", hostAbsolute),
		"__HOST_BROKEN__", fmt.Sprintf("%q", hostBroken),
		"__HOST_MISSING__", fmt.Sprintf("%q", hostMissing),
	).Replace(`<?php
function readlink_case(string $label, mixed $path): void {
    echo $label, ":";
    try {
        var_dump(readlink($path));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});
readlink_case("relative", __HOST_RELATIVE__);
readlink_case("absolute", __HOST_ABSOLUTE__);
readlink_case("broken", __HOST_BROKEN__);
readlink_case("regular", __HOST_TARGET__);
readlink_case("directory", dirname(__HOST_TARGET__));
readlink_case("missing", __HOST_MISSING__);
readlink_case("trailing-slash", __HOST_RELATIVE__ . "/");
readlink_case("embedded-file", __DIR__ . "/embedded.txt");
readlink_case("embedded-directory", __DIR__ . "/embedded-dir");
readlink_case("embedded-missing", __DIR__ . "/missing");
readlink_case("file-wrapper", "file://" . __HOST_RELATIVE__);
readlink_case("unknown-wrapper", "zz://x");
readlink_case("data-wrapper", "data:text/plain,x");
readlink_case("empty", "");
foreach ([null, false, true, 0, 1, 1.5, [], new stdClass] as $index => $path) {
    readlink_case("argument-" . $index, $path);
}
echo "named:";
var_dump(readlink(path: __HOST_RELATIVE__));
try { readlink(__HOST_RELATIVE__, true); } catch (Throwable $error) { echo get_class($error), ":", $error->getMessage(), "\n"; }
try { readlink("bad\0path"); } catch (Throwable $error) { echo get_class($error), ":", $error->getMessage(), "\n"; }
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestFilesystemMutationUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	hostRoot := t.TempDir()
	hostFile := filepath.Join(hostRoot, "file")
	hostLink := filepath.Join(hostRoot, "link")
	hostTouched := filepath.Join(hostRoot, "touched")
	hostMissing := filepath.Join(hostRoot, "missing")
	hostMissingParent := filepath.Join(hostRoot, "missing-parent", "leaf")
	mustWrite(t, hostFile, "x")
	if err := os.Chmod(hostFile, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", hostLink); err != nil {
		t.Fatal(err)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	group, err := user.LookupGroupId(current.Gid)
	if err != nil {
		t.Fatal(err)
	}

	source := strings.NewReplacer(
		"__HOST_FILE__", fmt.Sprintf("%q", hostFile),
		"__HOST_LINK__", fmt.Sprintf("%q", hostLink),
		"__HOST_TOUCHED__", fmt.Sprintf("%q", hostTouched),
		"__HOST_MISSING__", fmt.Sprintf("%q", hostMissing),
		"__HOST_MISSING_PARENT__", fmt.Sprintf("%q", hostMissingParent),
		"__USER_NAME__", fmt.Sprintf("%q", current.Username),
		"__GROUP_NAME__", fmt.Sprintf("%q", group.Name),
	).Replace(`<?php
function mutation_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

$file = __HOST_FILE__;
$link = __HOST_LINK__;
$uid = fileowner($file);
$gid = filegroup($file);
mutation_case('chmod', fn() => chmod($file, 0640));
var_dump(fileperms($file) & 0777);
mutation_case('chmod-file-uri', fn() => chmod('file://' . $file, 0640));
mutation_case('chmod-missing', fn() => chmod(__HOST_MISSING__, 0640));
mutation_case('chmod-data', fn() => chmod('data:text/plain,x', 0640));
mutation_case('chmod-unknown', fn() => chmod('zz://x', 0640));

mutation_case('chown-id', fn() => chown($file, $uid));
mutation_case('chown-name', fn() => chown($file, __USER_NAME__));
mutation_case(
    'chown-bad-name',
    fn() => chown($file, 'php2go-user-that-does-not-exist')
);
mutation_case('chgrp-id', fn() => chgrp($file, $gid));
mutation_case('chgrp-name', fn() => chgrp($file, __GROUP_NAME__));
mutation_case(
    'chgrp-bad-name',
    fn() => chgrp($file, 'php2go-group-that-does-not-exist')
);
mutation_case('lchown', fn() => lchown($link, $uid));
mutation_case('lchgrp', fn() => lchgrp($link, $gid));
mutation_case('lchown-data', fn() => lchown('data:text/plain,x', $uid));
mutation_case('lchgrp-data', fn() => lchgrp('data:text/plain,x', $gid));

$touched = __HOST_TOUCHED__;
mutation_case('touch-create', fn() => touch($touched, 123456789, 123456700));
$stat = stat($touched);
var_dump($stat['mtime'], $stat['atime']);
mutation_case('touch-mtime', fn() => touch($touched, 123456789));
$stat = stat($touched);
var_dump($stat['mtime'], $stat['atime']);
mutation_case('touch-now-data', fn() => touch('data:text/plain,x'));
mutation_case('touch-time-data', fn() => touch('data:text/plain,x', 1));
mutation_case('touch-missing-parent', fn() => touch(__HOST_MISSING_PARENT__));
mutation_case('touch-empty', fn() => touch(''));
mutation_case('touch-null-atime', fn() => touch($touched, null, 1));

mutation_case('chmod-nul', fn() => chmod("bad\0path", 0600));
mutation_case('touch-nul', fn() => touch("bad\0path"));
mutation_case('chmod-named', fn() => chmod(filename: $file, permissions: 0640));
mutation_case('touch-named', fn() => touch(filename: $touched, mtime: 123456789));
mutation_case('chmod-arity', fn() => chmod($file));
mutation_case('touch-arity', fn() => touch($touched, null, null, null));
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", first.String(), want)
	}
	if second.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", second.String(), want)
	}
}

func TestEmbeddedFilesystemMetadataIsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $message, "\n";
    return true;
});
$path = __DIR__ . '/embedded.txt';
var_dump(chmod($path, 0600), touch($path, 1), file_get_contents($path));
`)
	mustWrite(t, filepath.Join(root, "embedded.txt"), "embedded")

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	want := "diagnostic:chmod(): Read-only file system\n" +
		"diagnostic:touch(): Utime failed: Read-only file system\n" +
		"bool(false)\n" +
		"bool(false)\n" +
		"string(8) \"embedded\"\n"
	if got.String() != want {
		t.Fatalf("bundle output = %q, want %q", got.String(), want)
	}
}

func TestDirectoryUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "embedded", "alpha"), "a")
	mustWrite(t, filepath.Join(root, "embedded", "beta"), "b")
	mustWrite(t, filepath.Join(root, "embedded", "nested", "item"), "i")

	host := t.TempDir()
	mustWrite(t, filepath.Join(host, "alpha"), "a")
	mustWrite(t, filepath.Join(host, "beta"), "b")
	mustWrite(t, filepath.Join(host, "after-open"), "x")
	mustWrite(t, filepath.Join(host, "after-rewind"), "x")
	if err := os.Mkdir(filepath.Join(host, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	hostMissing := filepath.Join(host, "missing")
	hostFile := filepath.Join(host, "alpha")

	source := strings.NewReplacer(
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", host),
		"__HOST_MISSING__", fmt.Sprintf("%q", hostMissing),
		"__HOST_FILE__", fmt.Sprintf("%q", hostFile),
	).Replace(`<?php
function directory_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
function directory_entries($handle): array {
    $names = [];
    while (($name = readdir($handle)) !== false) {
        $names[] = $name;
    }
    sort($names);
    return $names;
}
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

$embedded = __DIR__ . '/embedded';
$host = __HOST_DIRECTORY__;
@unlink($host . '/after-open');
@unlink($host . '/after-rewind');
foreach (['embedded' => $embedded, 'host' => $host] as $label => $directory) {
    directory_case($label . '-scan-asc', fn() => scandir($directory));
    directory_case($label . '-scan-desc', fn() => scandir($directory, 1));
    directory_case($label . '-scan-other', fn() => scandir($directory, 99));
    $handle = opendir($directory);
    var_dump(is_resource($handle), get_resource_type($handle));
    directory_case($label . '-entries', fn() => directory_entries($handle));
    directory_case($label . '-rewind', fn() => rewinddir($handle));
    directory_case($label . '-first', fn() => readdir($handle));
    directory_case($label . '-close', fn() => closedir($handle));
    directory_case($label . '-read-closed', fn() => readdir($handle));
    directory_case($label . '-rewind-closed', fn() => rewinddir($handle));
    directory_case($label . '-close-closed', fn() => closedir($handle));
}

$live = opendir($host);
file_put_contents($host . '/after-open', 'x');
directory_case('live-after-open', fn() => directory_entries($live));
file_put_contents($host . '/after-rewind', 'x');
rewinddir($live);
directory_case('live-after-rewind', fn() => directory_entries($live));
closedir($live);

$first = opendir($host);
$second = opendir($host);
directory_case('close-nondefault', fn() => closedir($first));
directory_case('read-default', fn() => readdir());
directory_case('rewind-default', fn() => rewinddir());
directory_case('close-default', fn() => closedir());
directory_case('default-gone', fn() => readdir());

$stream = fopen('data:text/plain,x', 'r');
directory_case('read-wrong-resource', fn() => readdir($stream));
directory_case('rewind-wrong-resource', fn() => rewinddir($stream));
directory_case('close-wrong-resource', fn() => closedir($stream));
fclose($stream);
foreach ([1, 'x', [], new stdClass] as $index => $value) {
    directory_case('read-type-' . $index, fn() => readdir($value));
}

$context = stream_context_create();
directory_case('open-context', function () use ($host, $context): bool {
    $handle = opendir($host, $context);
    $result = is_resource($handle);
    closedir($handle);
    return $result;
});
directory_case('scan-context', fn() => scandir($host, 0, $context));
$wrong_context = fopen('data:text/plain,x', 'r');
directory_case('open-wrong-context', fn() => opendir($host, $wrong_context));
directory_case('scan-wrong-context', fn() => scandir($host, 0, $wrong_context));
fclose($wrong_context);
directory_case('open-context-type', fn() => opendir($host, 1));
directory_case('scan-context-type', fn() => scandir($host, 0, 1));

directory_case('open-empty', fn() => opendir(''));
directory_case('open-missing', fn() => opendir(__HOST_MISSING__));
directory_case('open-file', fn() => opendir(__HOST_FILE__));
directory_case('open-unknown', fn() => opendir('zz://x'));
directory_case('open-data', fn() => @opendir('data:text/plain,x'));
directory_case('scan-empty', fn() => scandir(''));
directory_case('scan-missing', fn() => scandir(__HOST_MISSING__));
directory_case('scan-file', fn() => scandir(__HOST_FILE__));
directory_case('scan-unknown', fn() => scandir('zz://x'));
directory_case('open-nul', fn() => opendir("bad\0path"));
directory_case('scan-nul', fn() => scandir("bad\0path"));
directory_case('open-named', function () use ($host): bool {
    $handle = opendir(directory: $host, context: null);
    closedir($handle);
    return true;
});
directory_case('scan-named', fn() => scandir(
    directory: $host,
    sorting_order: 0,
    context: null
));
directory_case('read-arity', fn() => readdir(null, null));
directory_case('scan-arity', fn() => scandir($host, 0, null, null));
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", firstRun.String(), want)
	}
	if secondRun.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", secondRun.String(), want)
	}
}

func TestDirectoryTopologyUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "topology")
	if err := os.Mkdir(host, 0o755); err != nil {
		t.Fatal(err)
	}

	source := strings.NewReplacer(
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", host),
	).Replace(`<?php
function topology_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), PHP_EOL;
    }
}

$base = __HOST_DIRECTORY__;
@unlink($base . '/nonempty/item');
foreach ([
    $base . '/recursive/a/b',
    $base . '/recursive/a',
    $base . '/recursive',
    $base . '/simple',
    $base . '/nonempty',
    $base . '/context',
    $base . '/named',
] as $path) {
    if (is_dir($path)) {
        rmdir($path);
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, PHP_EOL;
    return true;
});

$simple = $base . '/simple';
topology_case('mkdir-simple', fn() => mkdir($simple, 0751));
topology_case('mkdir-simple-mode', fn() => fileperms($simple) & 0777);
topology_case('mkdir-existing', fn() => mkdir($simple));
topology_case('mkdir-recursive', fn() =>
    mkdir($base . '/recursive/a/b', 0750, true)
);
topology_case('mkdir-recursive-present', fn() =>
    is_dir($base . '/recursive/a/b')
);
topology_case('mkdir-recursive-existing', fn() =>
    mkdir($base . '/recursive/a/b', 0750, true)
);
topology_case('mkdir-missing-parent', fn() =>
    mkdir($base . '/absent/child')
);

mkdir($base . '/nonempty');
file_put_contents($base . '/nonempty/item', 'x');
topology_case('rmdir-nonempty', fn() => rmdir($base . '/nonempty'));
unlink($base . '/nonempty/item');
topology_case('rmdir-nonempty-now-empty', fn() => rmdir($base . '/nonempty'));
topology_case('rmdir-simple', fn() => rmdir($simple));
topology_case('rmdir-missing', fn() => rmdir($simple));

topology_case('mkdir-empty', fn() => mkdir(''));
topology_case('rmdir-empty', fn() => rmdir(''));
topology_case('mkdir-data', fn() => mkdir('data:text/plain,x'));
topology_case('rmdir-data', fn() => rmdir('data:text/plain,x'));
topology_case('mkdir-unknown', fn() => mkdir('zz://x'));
topology_case('rmdir-unknown', fn() => rmdir('zz://x'));
topology_case('mkdir-nul', fn() => mkdir('bad' . chr(0) . 'path'));
topology_case('rmdir-nul', fn() => rmdir('bad' . chr(0) . 'path'));

$context = stream_context_create();
topology_case('mkdir-context', fn() =>
    mkdir($base . '/context', 0777, false, $context)
);
topology_case('rmdir-context', fn() =>
    rmdir($base . '/context', $context)
);
$wrong_context = fopen('data:text/plain,x', 'r');
topology_case('mkdir-wrong-context', fn() =>
    mkdir($base . '/context', 0777, false, $wrong_context)
);
topology_case('rmdir-wrong-context', fn() =>
    rmdir($base . '/context', $wrong_context)
);
fclose($wrong_context);

topology_case('mkdir-named', fn() => mkdir(
    directory: $base . '/named',
    permissions: 0777,
    recursive: false,
    context: null
));
topology_case('rmdir-named', fn() => rmdir(
    directory: $base . '/named',
    context: null
));
topology_case('mkdir-arity', fn() => mkdir($base, 0777, false, null, null));
topology_case('rmdir-arity', fn() => rmdir($base, null, null));
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", firstRun.String(), want)
	}
	if secondRun.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", secondRun.String(), want)
	}
}

func TestEmbeddedDirectoryTopologyIsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $message, PHP_EOL;
    return true;
});
$new = __DIR__ . '/new/nested';
$existing = __DIR__ . '/embedded';
var_dump(
    mkdir($new, 0777, true),
    rmdir($existing),
    is_dir($new),
    is_dir($existing)
);
`)
	mustWrite(t, filepath.Join(root, "embedded", "item"), "embedded")

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	want := `diagnostic:mkdir(): Read-only file system
diagnostic:rmdir(/app/embedded): Read-only file system
bool(false)
bool(false)
bool(false)
bool(true)
`
	if got.String() != want {
		t.Fatalf("bundle output = %q, want %q", got.String(), want)
	}
}

func TestLinkUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "links")
	if err := os.Mkdir(host, 0o755); err != nil {
		t.Fatal(err)
	}

	source := strings.NewReplacer(
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", host),
	).Replace(`<?php
function link_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), PHP_EOL;
    }
}

$base = __HOST_DIRECTORY__;
foreach ([
    'hard', 'soft', 'relative', 'missing-hard', 'missing-soft',
    'data-soft', 'unknown-soft', 'named-hard', 'named-soft',
] as $name) {
    $path = $base . '/' . $name;
    if (is_link($path) || file_exists($path)) {
        unlink($path);
    }
}
$target = $base . '/target';
file_put_contents($target, 'content');

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, PHP_EOL;
    return true;
});

$hard = $base . '/hard';
link_case('link-basic', fn() => link($target, $hard));
link_case('link-content', fn() => file_get_contents($hard));
link_case('link-inode', fn() => fileinode($target) === fileinode($hard));
link_case('link-existing', fn() => link($target, $hard));
link_case('link-missing-target', fn() =>
    link($base . '/missing', $base . '/missing-hard')
);
link_case('link-empty-target', fn() => link('', $base . '/empty-hard'));
link_case('link-empty-link', fn() => link($target, ''));
link_case('link-data-target', fn() =>
    link('data:text/plain,x', $base . '/data-hard')
);
link_case('link-unknown-target', fn() =>
    link('zz://x', $base . '/unknown-hard')
);

$soft = $base . '/soft';
link_case('symlink-basic', fn() => symlink($target, $soft));
link_case('symlink-read', fn() => readlink($soft));
link_case('symlink-relative', fn() =>
    symlink('target', $base . '/relative')
);
link_case('symlink-relative-read', fn() => readlink($base . '/relative'));
link_case('symlink-missing-target', fn() =>
    symlink('missing', $base . '/missing-soft')
);
link_case('symlink-missing-read', fn() => readlink($base . '/missing-soft'));
link_case('symlink-existing', fn() => symlink($target, $soft));
link_case('symlink-empty-target', fn() =>
    symlink('', $base . '/empty-soft')
);
link_case('symlink-empty-link', fn() => symlink($target, ''));
link_case('symlink-data-target', fn() =>
    symlink('data:text/plain,x', $base . '/data-soft')
);
link_case('symlink-data-read', fn() => readlink($base . '/data-soft'));
link_case('symlink-unknown-target', fn() =>
    symlink('zz://x', $base . '/unknown-soft')
);
link_case('symlink-unknown-read', fn() => readlink($base . '/unknown-soft'));

foreach (['link', 'symlink'] as $function) {
    link_case($function . '-nul-target', fn() =>
        $function('bad' . chr(0) . 'path', $base . '/nul')
    );
    link_case($function . '-nul-link', fn() =>
        $function($target, 'bad' . chr(0) . 'path')
    );
}
link_case('link-named', fn() => link(
    target: $target,
    link: $base . '/named-hard'
));
link_case('symlink-named', fn() => symlink(
    target: 'target',
    link: $base . '/named-soft'
));
link_case('link-arity', fn() => link($target, $hard, null));
link_case('symlink-arity', fn() => symlink($target, $soft, null));
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", firstRun.String(), want)
	}
	if secondRun.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", secondRun.String(), want)
	}
}

func TestEmbeddedLinksAreReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $message, PHP_EOL;
    return true;
});
$target = __DIR__ . '/embedded.txt';
$hard = __DIR__ . '/hard';
$soft = __DIR__ . '/soft';
var_dump(
    link($target, $hard),
    symlink('embedded.txt', $soft),
    file_get_contents($target),
    file_exists($hard),
    is_link($soft)
);
`)
	mustWrite(t, filepath.Join(root, "embedded.txt"), "embedded")

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	want := `diagnostic:link(): Read-only file system
diagnostic:symlink(): Read-only file system
bool(false)
bool(false)
string(8) "embedded"
bool(false)
bool(false)
`
	if got.String() != want {
		t.Fatalf("bundle output = %q, want %q", got.String(), want)
	}
}

func TestRenameUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "rename")
	if err := os.Mkdir(host, 0o755); err != nil {
		t.Fatal(err)
	}
	cross, err := os.CreateTemp("/dev/shm", "php2go-rename-differential-*")
	if err != nil {
		t.Skipf("cross-device destination unavailable: %v", err)
	}
	crossName := cross.Name()
	if err := cross.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(crossName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(crossName) })
	crossDirectory := crossName + "-directory"
	t.Cleanup(func() { _ = os.Remove(crossDirectory) })

	source := strings.NewReplacer(
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", host),
		"__CROSS_DESTINATION__", fmt.Sprintf("%q", crossName),
		"__CROSS_DIRECTORY__", fmt.Sprintf("%q", crossDirectory),
	).Replace(`<?php
function rename_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), PHP_EOL;
    }
}

$base = __HOST_DIRECTORY__;
$cross = __CROSS_DESTINATION__;
$cross_directory = __CROSS_DIRECTORY__;
foreach ([
    'source', 'dest', 'replace-source', 'replace-dest',
    'file-uri-source', 'file-uri-dest', 'context-source',
    'context-dest', 'named-source', 'named-dest',
] as $name) {
    $path = $base . '/' . $name;
    if (is_link($path) || file_exists($path)) {
        unlink($path);
    }
}
foreach (['dir-source', 'dir-dest', 'cross-directory'] as $name) {
    $path = $base . '/' . $name;
    if (is_dir($path)) {
        rmdir($path);
    }
}
if (file_exists($cross)) {
    unlink($cross);
}
if (is_dir($cross_directory)) {
    rmdir($cross_directory);
}

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, PHP_EOL;
    return true;
});

file_put_contents($base . '/source', 'source');
rename_case('file', fn() =>
    rename($base . '/source', $base . '/dest')
);
rename_case('file-state', fn() => [
    file_exists($base . '/source'),
    file_get_contents($base . '/dest'),
]);

file_put_contents($base . '/replace-source', 'new');
file_put_contents($base . '/replace-dest', 'old');
rename_case('replace', fn() =>
    rename($base . '/replace-source', $base . '/replace-dest')
);
rename_case('replace-content', fn() =>
    file_get_contents($base . '/replace-dest')
);

mkdir($base . '/dir-source');
rename_case('directory', fn() =>
    rename($base . '/dir-source', $base . '/dir-dest')
);
rename_case('directory-state', fn() => [
    is_dir($base . '/dir-source'), is_dir($base . '/dir-dest'),
]);
rename_case('same', fn() => rename($base . '/dest', $base . '/dest'));
rename_case('missing', fn() =>
    rename($base . '/missing', $base . '/new')
);
rename_case('empty-from', fn() => rename('', $base . '/new'));
rename_case('empty-to', fn() => rename($base . '/dest', ''));

rename_case('data-data', fn() =>
    rename('data:text/plain,x', 'data:text/plain,y')
);
rename_case('data-host', fn() =>
    rename('data:text/plain,x', $base . '/data')
);
rename_case('host-data', fn() =>
    rename($base . '/dest', 'data:text/plain,x')
);
rename_case('unknown-source', fn() =>
    rename('zz://x', $base . '/unknown')
);
rename_case('unknown-target', fn() =>
    rename($base . '/dest', 'zz://x')
);

file_put_contents($base . '/file-uri-source', 'uri');
rename_case('file-uri', fn() => rename(
    'file://' . $base . '/file-uri-source',
    'file://' . $base . '/file-uri-dest'
));
rename_case('file-uri-content', fn() =>
    file_get_contents($base . '/file-uri-dest')
);

$context = stream_context_create();
file_put_contents($base . '/context-source', 'context');
rename_case('context', fn() => rename(
    $base . '/context-source',
    $base . '/context-dest',
    $context
));
$wrong = fopen('data:text/plain,x', 'r');
rename_case('wrong-context', fn() => rename(
    $base . '/dest', $base . '/wrong-context', $wrong
));
fclose($wrong);
rename_case('context-type', fn() => rename(
    $base . '/dest', $base . '/context-type', 1
));
rename_case('nul-from', fn() =>
    rename('bad' . chr(0) . 'path', $base . '/nul')
);
rename_case('nul-to', fn() =>
    rename($base . '/dest', 'bad' . chr(0) . 'path')
);

file_put_contents($base . '/named-source', 'named');
rename_case('named', fn() => rename(
    from: $base . '/named-source',
    to: $base . '/named-dest',
    context: null
));
rename_case('arity', fn() => rename($base, $base, null, null));

file_put_contents($base . '/cross-source', 'cross');
chmod($base . '/cross-source', 0641);
rename_case('cross', fn() =>
    rename($base . '/cross-source', $cross)
);
rename_case('cross-state', fn() => [
    file_exists($base . '/cross-source'),
    file_get_contents($cross),
    fileperms($cross) & 0777,
    fileowner($cross) === getmyuid(),
    filegroup($cross) === getmygid(),
]);
mkdir($base . '/cross-directory');
rename_case('cross-directory', fn() =>
    rename($base . '/cross-directory', $cross_directory)
);
rename_case('cross-directory-state', fn() => [
    is_dir($base . '/cross-directory'),
    is_dir($cross_directory),
]);
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", firstRun.String(), want)
	}
	if secondRun.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", secondRun.String(), want)
	}
}

func TestEmbeddedRenameIsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "host-source")
	mustWrite(t, host, "host")
	source := strings.NewReplacer(
		"__HOST_SOURCE__", fmt.Sprintf("%q", host),
	).Replace(`<?php
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $message, PHP_EOL;
    return true;
});
$embedded = __DIR__ . '/embedded.txt';
$moved = __DIR__ . '/moved.txt';
$host = __HOST_SOURCE__;
var_dump(
    rename($embedded, $moved),
    rename($embedded, $host . '.moved'),
    rename($host, $moved),
    file_get_contents($embedded),
    file_exists($moved),
    file_get_contents($host)
);
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)
	mustWrite(t, filepath.Join(root, "embedded.txt"), "embedded")

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(
		"diagnostic:rename(/app/embedded.txt,/app/moved.txt): Read-only file system\n"+
			"diagnostic:rename(/app/embedded.txt,%s.moved): Read-only file system\n"+
			"diagnostic:rename(%s,/app/moved.txt): Read-only file system\n"+
			"bool(false)\nbool(false)\nbool(false)\n"+
			"string(8) \"embedded\"\nbool(false)\nstring(4) \"host\"\n",
		host, host,
	)
	if got.String() != want {
		t.Fatalf("bundle output = %q, want %q", got.String(), want)
	}
}

func TestUmaskUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "umask")
	if err := os.Mkdir(host, 0o755); err != nil {
		t.Fatal(err)
	}

	source := strings.NewReplacer(
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", host),
	).Replace(`<?php
function umask_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), PHP_EOL;
    }
}

$base = __HOST_DIRECTORY__;
foreach ([0, 1, 2, 3, 4, 5] as $index) {
    $path = $base . '/mask-' . $index;
    if (is_dir($path)) {
        rmdir($path);
    }
}
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, PHP_EOL;
    return true;
});

$initial = umask();
var_dump($initial, umask(), umask(null));
$masks = [0, 0022, 0777, -1, 01000, PHP_INT_MAX];
foreach ($masks as $index => $mask) {
    $old = umask($mask);
    $current = umask();
    $directory = $base . '/mask-' . $index;
    mkdir($directory, 0777);
    var_dump([
        $mask,
        $old,
        $current,
        fileperms($directory) & 0777,
    ]);
}

foreach ([false, true, 1.9, '18'] as $index => $mask) {
    umask_case('coerce-' . $index, fn() => [umask($mask), umask()]);
}
foreach ([[], new stdClass] as $index => $mask) {
    umask_case('type-' . $index, fn() => umask($mask));
}
umask_case('named', fn() => [umask(mask: 0027), umask()]);
umask_case('arity', fn() => umask(1, 2));

// Request shutdown, not user code, must restore the incoming process mask.
umask(0077);
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", firstRun.String(), want)
	}
	if secondRun.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", secondRun.String(), want)
	}
}

func TestCopyUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "copy")
	if err := os.Mkdir(host, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "embedded.txt"), "embedded")

	source := strings.NewReplacer(
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", host),
	).Replace(`<?php
function copy_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), PHP_EOL;
    }
}

$base = __HOST_DIRECTORY__;
foreach ([
    'source', 'dest', 'hard', 'data', 'file-uri', 'context', 'named',
    'embedded-copy', 'mode-copy', 'empty-file', 'empty-copy',
] as $name) {
    $path = $base . '/' . $name;
    if (is_link($path) || file_exists($path)) {
        unlink($path);
    }
}
foreach (['source-dir', 'dest-dir'] as $name) {
    $path = $base . '/' . $name;
    if (is_dir($path)) {
        rmdir($path);
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, PHP_EOL;
    return true;
});
file_put_contents($base . '/source', 'source');
copy_case('basic', fn() => copy($base . '/source', $base . '/dest'));
copy_case('basic-content', fn() => file_get_contents($base . '/dest'));
file_put_contents($base . '/dest', 'old');
copy_case('replace', fn() => copy($base . '/source', $base . '/dest'));
copy_case('replace-content', fn() => file_get_contents($base . '/dest'));
copy_case('same', fn() => copy($base . '/source', $base . '/source'));
link($base . '/source', $base . '/hard');
copy_case('same-inode', fn() => copy($base . '/source', $base . '/hard'));

mkdir($base . '/source-dir');
mkdir($base . '/dest-dir');
copy_case('source-dir', fn() =>
    copy($base . '/source-dir', $base . '/x')
);
copy_case('dest-dir', fn() =>
    copy($base . '/source', $base . '/dest-dir')
);
copy_case('missing', fn() =>
    copy($base . '/missing', $base . '/x')
);
copy_case('empty-from', fn() => copy('', $base . '/x'));
copy_case('empty-to', fn() => copy($base . '/source', ''));

copy_case('data-host', fn() =>
    copy('data:text/plain,data', $base . '/data')
);
copy_case('data-content', fn() => file_get_contents($base . '/data'));
copy_case('host-data', fn() =>
    copy($base . '/source', 'data:text/plain,x')
);
copy_case('data-data', fn() =>
    copy('data:text/plain,x', 'data:text/plain,y')
);
copy_case('unknown-source', fn() =>
    copy('zz://x', $base . '/unknown')
);
copy_case('unknown-target', fn() =>
    copy($base . '/source', 'zz://x')
);
copy_case('file-uri', fn() => copy(
    'file://' . $base . '/source',
    'file://' . $base . '/file-uri'
));
copy_case('file-uri-content', fn() =>
    file_get_contents($base . '/file-uri')
);

$embedded = __DIR__ . '/embedded.txt';
copy_case('embedded-host', fn() =>
    copy($embedded, $base . '/embedded-copy')
);
copy_case('embedded-content', fn() =>
    file_get_contents($base . '/embedded-copy')
);
copy_case('embedded-same', fn() => copy($embedded, $embedded));

file_put_contents($base . '/empty-file', '');
copy_case('empty-file', fn() =>
    copy($base . '/empty-file', $base . '/empty-copy')
);
copy_case('empty-size', fn() => filesize($base . '/empty-copy'));

$old_mask = umask(0027);
copy_case('mode-copy', fn() =>
    copy($base . '/source', $base . '/mode-copy')
);
copy_case('mode', fn() => fileperms($base . '/mode-copy') & 0777);
umask($old_mask);

$context = stream_context_create();
copy_case('context', fn() =>
    copy($base . '/source', $base . '/context', $context)
);
$wrong = fopen('data:text/plain,x', 'r');
copy_case('wrong-context', fn() =>
    copy($base . '/source', $base . '/wrong', $wrong)
);
copy_case('context-type', fn() =>
    copy($base . '/source', $base . '/wrong', 1)
);
fclose($wrong);
copy_case('nul-from', fn() =>
    copy('bad' . chr(0) . 'path', $base . '/nul')
);
copy_case('nul-to', fn() =>
    copy($base . '/source', 'bad' . chr(0) . 'path')
);
copy_case('named', fn() => copy(
    from: $base . '/source',
    to: $base . '/named',
    context: null
));
copy_case('arity', fn() => copy($base, $base, null, null));
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", firstRun.String(), want)
	}
	if secondRun.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", secondRun.String(), want)
	}
}

func TestEmbeddedCopyDestinationIsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "host-source")
	mustWrite(t, host, "host")
	source := strings.NewReplacer(
		"__HOST_SOURCE__", fmt.Sprintf("%q", host),
	).Replace(`<?php
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $message, PHP_EOL;
    return true;
});
$embedded = __DIR__ . '/embedded.txt';
$destination = __DIR__ . '/copied.txt';
$host = __HOST_SOURCE__;
var_dump(
    copy($host, $destination),
    copy($embedded, $destination),
    copy('data:text/plain,data', $destination),
    file_get_contents($embedded),
    file_exists($destination),
    file_get_contents($host)
);
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)
	mustWrite(t, filepath.Join(root, "embedded.txt"), "embedded")

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	want := `diagnostic:copy(/app/copied.txt): Failed to open stream: Read-only file system
diagnostic:copy(/app/copied.txt): Failed to open stream: Read-only file system
diagnostic:copy(/app/copied.txt): Failed to open stream: Read-only file system
bool(false)
bool(false)
bool(false)
string(8) "embedded"
bool(false)
string(4) "host"
`
	if got.String() != want {
		t.Fatalf("bundle output = %q, want %q", got.String(), want)
	}
}

func TestTemporaryFileUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "temporary-files")
	if err := os.Mkdir(host, 0o755); err != nil {
		t.Fatal(err)
	}

	source := strings.NewReplacer(
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", host),
	).Replace(`<?php
function temporary_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), PHP_EOL;
    }
}

$base = __HOST_DIRECTORY__;
foreach (scandir($base) as $name) {
    if ($name !== '.' && $name !== '..') {
        unlink($base . '/' . $name);
    }
}
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, PHP_EOL;
    return true;
});

$created = [];
$basic = $created[] = tempnam($base, 'pre');
temporary_case('basic', fn() => [
    dirname($basic) === realpath($base),
    str_starts_with(basename($basic), 'pre'),
    strlen(basename($basic)),
    fileperms($basic) & 0777,
    filesize($basic),
]);
$slash = $created[] = tempnam($base, '/alpha/beta');
temporary_case('slash-prefix', fn() => [
    str_starts_with(basename($slash), 'beta'),
    strlen(basename($slash)),
]);
$long = $created[] = tempnam($base, str_repeat('x', 70));
temporary_case('long-prefix', fn() => [
    str_starts_with(basename($long), str_repeat('x', 63)),
    strlen(basename($long)),
]);
$empty_prefix = $created[] = tempnam($base, '');
temporary_case('empty-prefix', fn() => strlen(basename($empty_prefix)));

$old_mask = umask(0777);
$masked = $created[] = tempnam($base, 'mask');
umask($old_mask);
temporary_case('masked-mode', fn() => fileperms($masked) & 0777);

$missing = $created[] = tempnam($base . '/missing', 'fallback');
temporary_case('missing-dir', fn() => [
    dirname($missing) === sys_get_temp_dir(),
    str_starts_with(basename($missing), 'fallback'),
    strlen(basename($missing)),
]);
$not_directory = $created[] = tempnam($basic, 'filedir');
temporary_case(
    'file-dir',
    fn() => dirname($not_directory) === sys_get_temp_dir()
);
$empty_directory = $created[] = tempnam('', 'emptydir');
temporary_case(
    'empty-dir',
    fn() => dirname($empty_directory) === sys_get_temp_dir()
);
$file_uri = $created[] = tempnam('file://' . $base, 'uri');
temporary_case(
    'file-uri-dir',
    fn() => dirname($file_uri) === sys_get_temp_dir()
);

temporary_case(
    'nul-dir',
    fn() => tempnam('bad' . chr(0) . 'dir', 'x')
);
temporary_case(
    'nul-prefix',
    fn() => tempnam($base, 'bad' . chr(0) . 'prefix')
);
temporary_case('named', function () use ($base, &$created): array {
    $path = $created[] = tempnam(directory: $base, prefix: 'named');
    return [
        str_starts_with(basename($path), 'named'),
        strlen(basename($path)),
    ];
});
temporary_case('arity', fn() => tempnam($base));

$first = tmpfile();
$first_meta = stream_get_meta_data($first);
temporary_case('tmpfile-shape', fn() => [
    is_resource($first),
    get_resource_type($first),
    $first_meta['wrapper_type'],
    $first_meta['stream_type'],
    $first_meta['mode'],
    $first_meta['seekable'],
    str_starts_with(basename($first_meta['uri']), 'php'),
    strlen(basename($first_meta['uri'])),
    file_exists($first_meta['uri']),
    fstat($first)['mode'] & 0777,
]);
temporary_case('tmpfile-write', function () use ($first): array {
    $written = fwrite($first, 'temporary');
    $position = ftell($first);
    rewind($first);
    return [$written, $position, fread($first, 9), fstat($first)['size']];
});
$first_path = $first_meta['uri'];
temporary_case('tmpfile-close', fn() => fclose($first));
temporary_case('tmpfile-gone', fn() => file_exists($first_path));
$second = tmpfile();
$second_path = stream_get_meta_data($second)['uri'];
temporary_case('tmpfile-unique', fn() => $second_path !== $first_path);
fclose($second);
temporary_case('tmpfile-arity', fn() => tmpfile(1));

foreach ($created as $path) {
    if (is_string($path) && file_exists($path)) {
        unlink($path);
    }
}
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", firstRun.String(), want)
	}
	if secondRun.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", secondRun.String(), want)
	}
}

func TestTmpfileIsRemovedAtRequestShutdown(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	marker := filepath.Join(t.TempDir(), "tmpfile-path")
	source := strings.NewReplacer(
		"__MARKER__", fmt.Sprintf("%q", marker),
	).Replace(`<?php
$stream = tmpfile();
$metadata = stream_get_meta_data($stream);
file_put_contents(__MARKER__, $metadata['uri']);
var_dump(
    is_resource($stream),
    file_exists($metadata['uri']),
    fwrite($stream, 'request')
);
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	firstPath, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(firstPath)); !os.IsNotExist(err) {
		t.Fatalf("first request tmpfile stat error = %v, want not exist", err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}
	secondPath, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(secondPath)); !os.IsNotExist(err) {
		t.Fatalf("second request tmpfile stat error = %v, want not exist", err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want || secondRun.String() != want {
		t.Fatalf("bundle outputs = %q, %q; engine output = %q",
			firstRun.String(), secondRun.String(), want)
	}
}

func TestEmbeddedTempnamFallsBackReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, PHP_EOL;
    return true;
});
$path = tempnam(__DIR__, 'embedded');
var_dump(
    dirname($path) === sys_get_temp_dir(),
    str_starts_with(basename($path), 'embedded'),
    strlen(basename($path)),
    file_exists($path)
);
unlink($path);
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	want := `diagnostic:8:tempnam(): file created in the system's temporary directory
bool(true)
bool(true)
int(27)
bool(true)
`
	if got.String() != want {
		t.Fatalf("bundle output = %q, want %q", got.String(), want)
	}
}

func TestDirObjectUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "directory-object")
	if err := os.Mkdir(host, 0o755); err != nil {
		t.Fatal(err)
	}

	source := strings.NewReplacer(
		"__HOST_DIRECTORY__", fmt.Sprintf("%q", host),
	).Replace(`<?php
function dir_object_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), PHP_EOL;
    }
}

$base = __HOST_DIRECTORY__;
@unlink($base . '/alpha');
file_put_contents($base . '/alpha', 'a');
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, PHP_EOL;
    return true;
});

$directory = dir($base);
dir_object_case('class', fn() => [
    get_class($directory),
    $directory->path === $base,
    is_resource($directory->handle),
    get_resource_type($directory->handle),
    array_keys(get_object_vars($directory)),
]);
dir_object_case('read-first', fn() => $directory->read());
dir_object_case('read-second', fn() => $directory->read());
dir_object_case('rewind', fn() => $directory->rewind());
dir_object_case('procedural-default', fn() => readdir());
dir_object_case('method-after-procedural', fn() => $directory->read());
dir_object_case('method-close', fn() => $directory->close());
dir_object_case('default-after-close', fn() => readdir());
dir_object_case('method-after-close', fn() => $directory->read());
dir_object_case('close-after-close', fn() => $directory->close());

$relative = dir($base . '/.');
dir_object_case('path-verbatim', fn() => $relative->path === $base . '/.');
$other = opendir($base);
dir_object_case('default-other', fn() => readdir() !== false);
dir_object_case('close-nondefault-object', fn() => $relative->close());
dir_object_case('default-survives', fn() => readdir() !== false);
closedir($other);

$manual = new Directory();
dir_object_case('manual-properties', fn() => get_object_vars($manual));
dir_object_case('manual-read', fn() => $manual->read());
dir_object_case('readonly-path', function () use ($directory): bool {
    $directory->path = 'changed';
    return true;
});

$context = stream_context_create();
dir_object_case('context', function () use ($base, $context): bool {
    $opened = dir($base, $context);
    $opened->close();
    return true;
});
$wrong = fopen('data:text/plain,x', 'r');
dir_object_case('wrong-context', fn() => dir($base, $wrong));
fclose($wrong);
dir_object_case('context-type', fn() => dir($base, 1));
dir_object_case('empty', fn() => dir(''));
dir_object_case('missing', fn() => dir($base . '/missing'));
dir_object_case('file', fn() => dir($base . '/alpha'));
dir_object_case('data', fn() => @dir('data:text/plain,x'));
dir_object_case('unknown', fn() => dir('zz://x'));
dir_object_case('nul', fn() => dir('bad' . chr(0) . 'path'));
dir_object_case('named', function () use ($base): string {
    $opened = dir(directory: $base, context: null);
    $class = get_class($opened);
    $opened->close();
    return $class;
});
dir_object_case('arity', fn() => dir($base, null, null));
dir_object_case('method-arity', fn() => $directory->read(1));
dir_object_case('method-named', fn() => $manual->read(value: 1));
unlink($base . '/alpha');
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", firstRun.String(), want)
	}
	if secondRun.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", secondRun.String(), want)
	}
}

func TestEmbeddedDirObjectMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "embedded", "item"), "embedded")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function embedded_dir_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), PHP_EOL;
    }
}
$path = __DIR__ . '/embedded';
$directory = dir($path);
embedded_dir_case('shape', fn() => [
    get_class($directory),
    $directory->path === $path,
    get_resource_type($directory->handle),
]);
$entries = [];
while (($entry = $directory->read()) !== false) {
    $entries[] = $entry;
}
sort($entries);
embedded_dir_case('entries', fn() => $entries);
embedded_dir_case('rewind', fn() => $directory->rewind());
embedded_dir_case('default-dot', fn() => readdir());
embedded_dir_case('close', fn() => $directory->close());
embedded_dir_case('default-closed', fn() => readdir());
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var firstRun, secondRun bytes.Buffer
	if _, err := app.Run(php.NewCtx(&firstRun)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&secondRun)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if firstRun.String() != want || secondRun.String() != want {
		t.Fatalf("bundle outputs = %q, %q; engine output = %q",
			firstRun.String(), secondRun.String(), want)
	}
}

func TestMemoryPeakResetUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
$values = range(0, 200000);
unset($values);
gc_collect_cycles();

$before_usage = memory_get_usage();
$before_peak = memory_get_peak_usage();
var_dump($before_peak >= $before_usage);
var_dump(memory_reset_peak_usage());
$after_usage = memory_get_usage();
$after_peak = memory_get_peak_usage();
var_dump(
    $after_peak >= $after_usage,
    $after_peak <= $before_peak
);
var_dump(
    memory_reset_peak_usage(),
    memory_get_peak_usage() === memory_get_usage()
);
try {
    memory_reset_peak_usage(true);
} catch (Throwable $error) {
    echo get_class($error), ':', $error->getMessage(), "\n";
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestImplicitFlushUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function implicit_flush_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

implicit_flush_case('default', fn() => ob_implicit_flush());
implicit_flush_case('true', fn() => ob_implicit_flush(true));
implicit_flush_case('false', fn() => ob_implicit_flush(false));
implicit_flush_case('named', fn() => ob_implicit_flush(enable: true));
implicit_flush_case('zero', fn() => ob_implicit_flush(0));
implicit_flush_case('one', fn() => ob_implicit_flush(1));
implicit_flush_case('null', fn() => ob_implicit_flush(null));
implicit_flush_case('array', fn() => ob_implicit_flush([]));
implicit_flush_case('object', fn() => ob_implicit_flush(new stdClass));
implicit_flush_case('arity', fn() => ob_implicit_flush(true, false));
restore_error_handler();

ob_start();
echo 'held';
ob_implicit_flush(true);
var_dump(ob_get_contents(), ob_get_length());
$contents = ob_get_clean();
var_dump($contents);
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestOutputRewriteResetUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function rewrite_reset_case(string $label, callable $probe): void {
    echo $label, ":";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}

$base_level = ob_get_level();
$base_handlers = ob_list_handlers();
rewrite_reset_case("initial", function () use ($base_level, $base_handlers): array {
    $result = output_reset_rewrite_vars();
    return [
        $result,
        ob_get_level() === $base_level,
        ob_list_handlers() === $base_handlers,
    ];
});

ob_start();
echo "before|";
$buffer_level = ob_get_level();
$buffer_handlers = ob_list_handlers();
$buffer_result = output_reset_rewrite_vars();
echo "after";
$contents = ob_get_clean();
var_dump(
    $buffer_result,
    $contents,
    ob_get_level() === $base_level,
    ob_list_handlers() === $base_handlers,
    $buffer_level === $base_level + 1,
    count($buffer_handlers) === count($base_handlers) + 1
);

rewrite_reset_case("repeat", fn() => [
    output_reset_rewrite_vars(),
    output_reset_rewrite_vars(),
]);
rewrite_reset_case("extra", fn() => output_reset_rewrite_vars(true));
rewrite_reset_case("unknown-named", fn() => output_reset_rewrite_vars(extra: true));
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", first.String(), want)
	}
	if second.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", second.String(), want)
	}
}

func TestOutputRewriteUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function rewrite_case(string $label, callable $probe): void {
    echo $label, ":";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}

$base_level = ob_get_level();
$base_handlers = ob_list_handlers();
ini_set("url_rewriter.tags", "a=href,area=href,frame=src,form=");
ini_set("url_rewriter.hosts", "example.com");
ini_set("arg_separator.output", ";");

$added = output_add_rewrite_var(name: "a b", value: "x&y");
$status = ob_get_status();
$handlers = ob_list_handlers();
var_dump($added, [
    "name" => $status["name"],
    "type" => $status["type"],
    "flags" => $status["flags"],
    "level" => $status["level"] - $base_level,
    "chunk_size" => $status["chunk_size"],
    "buffer_size" => $status["buffer_size"],
    "handler_delta" => count($handlers) - count($base_handlers),
    "last_handler" => $handlers[array_key_last($handlers)],
]);

echo '<a href="rel">relative</a>';
echo '<a href="/p?q=0#f">query</a>';
echo '<a href="http://example.com">host</a>';
echo '<a href="//example.com/p">network</a>';
echo '<a href="https://other.test/p">other</a>';
echo '<a href="#fragment">fragment</a>';
echo '<AREA HREF=map>';
echo '<frame src="/frame">';
echo '<form method=get action="/submit"></form>';
echo '<form action="http://example.com/post"></form>';
echo '<form action="mailto:user@example.com"></form>', "\n";
var_dump(ob_flush(), ob_get_contents());

var_dump(output_reset_rewrite_vars());
echo '<a href="/after-reset">reset-link</a>';
echo '<form action="/after-reset"></form>', "\n";
var_dump(ob_flush());

ini_set("url_rewriter.tags", " a=href,form=");
ini_set("url_rewriter.hosts", " example.com");
var_dump(output_add_rewrite_var("space", "token"));
echo '<a href="/space">space-link</a>';
echo '<form action="/space"></form>';
echo '<form action="http://example.com/space"></form>', "\n";
var_dump(ob_flush(), output_reset_rewrite_vars());

ini_set("url_rewriter.tags", "a=href,area=href,frame=src,form=");
ini_set("url_rewriter.hosts", "example.com");
var_dump(
    output_add_rewrite_var("quote\"'", "<value&>"),
    output_add_rewrite_var("duplicate", "one"),
    output_add_rewrite_var("duplicate", "two")
);
echo '<a href="/second?old=1#tail">second</a>';
echo '<form action="/second"></form>', "\n";
var_dump(ob_end_flush());
var_dump(
    ob_get_level() === $base_level,
    ob_list_handlers() === $base_handlers
);

var_dump(output_add_rewrite_var("late", "value"));
echo '<a href="/not-restarted">late</a>', "\n";
var_dump(
    ob_get_level() === $base_level,
    ob_list_handlers() === $base_handlers
);

rewrite_case("array-name", fn() => output_add_rewrite_var([], "x"));
rewrite_case("missing", fn() => output_add_rewrite_var("x"));
rewrite_case("unknown-named", fn() => output_add_rewrite_var(name: "x", value: "y", extra: "z"));
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", first.String(), want)
	}
	if second.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", second.String(), want)
	}
}

func TestOutputBufferIntrospectionUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
$base = ob_get_level();

function normalize_status(array $status, int $base): array {
    if ($status !== []) {
        $status['level'] -= $base;
    }
    return $status;
}

function normalize_statuses(array $statuses, int $base): array {
    foreach ($statuses as &$status) {
        $status = normalize_status($status, $base);
    }
    unset($status);
    return $statuses;
}

function status_snapshot(int $base): array {
    $all = array_slice(ob_get_status(true), $base);
    return [
        ob_get_level() === $base
            ? []
            : normalize_status(ob_get_status(), $base),
        normalize_statuses($all, $base),
        array_slice(ob_list_handlers(), $base),
    ];
}

function status_report(string $label, mixed $value): void {
    echo $label, ':', json_encode($value), "\n";
}

function status_capture(callable $probe): array {
    try {
        return ['value' => $probe()];
    } catch (Throwable $error) {
        return ['error' => get_class($error) . ':' . $error->getMessage()];
    }
}

status_report('empty', status_snapshot($base));

ob_start();
echo 'abc';
$default = status_snapshot($base);
$default_raw = ob_get_clean();
status_report('default', [$default, $default_raw]);

function status_named(string $text, int $phase): string {
    return $text;
}
class StatusHandler {
    public static function staticCB(string $text, int $phase): string {
        return $text;
    }
    public function methodCB(string $text, int $phase): string {
        return $text;
    }
    public function __invoke(string $text, int $phase): string {
        return $text;
    }
}
$handler = new StatusHandler();
$callbacks = [
    'STATUS_NAMED',
    [StatusHandler::class, 'STATICCB'],
    [$handler, 'METHODCB'],
    $handler,
    static fn(string $text, int $phase): string => $text,
];
$names = [];
foreach ($callbacks as $callback) {
    ob_start($callback);
    $names[] = [
        ob_get_status()['name'],
        ob_list_handlers()[ob_get_level() - 1],
    ];
    ob_end_clean();
}
status_report('names', $names);

$capacities = [];
foreach ([
    [0, 64, 16384],
    [4096, 112, 30],
    [-5, 112, 1],
] as [$chunk, $flags, $length]) {
    ob_start(null, $chunk, $flags);
    echo str_repeat('x', $length);
    $capacities[] = normalize_status(ob_get_status(), $base);
    ob_end_clean();
}
status_report('capacities', $capacities);

$events = [];
$upper = static function (string $text, int $phase) use (&$events): string {
    $events[] = [$text, $phase];
    return strtoupper($text);
};
ob_start();
ob_start($upper, 2);
echo 'ab';
$chunk_first = normalize_status(ob_get_status(), $base);
echo 'c';
$chunk_second = normalize_status(ob_get_status(), $base);
$chunk_raw = ob_get_clean();
$chunk_outer = ob_get_clean();
status_report('chunk', [
    $chunk_first, $chunk_second, $chunk_raw, $chunk_outer, $events,
]);

$disabled_events = [];
$disable = static function (string $text, int $phase) use (&$disabled_events): bool {
    $disabled_events[] = [$text, $phase];
    return false;
};
ob_start();
ob_start($disable, 2);
echo 'ab';
$disabled_first = normalize_status(ob_get_status(), $base);
echo 'cd';
$disabled_second = normalize_status(ob_get_status(), $base);
$disabled_raw = ob_get_clean();
$disabled_outer = ob_get_clean();
status_report('disabled', [
    $disabled_first,
    $disabled_second,
    $disabled_raw,
    $disabled_outer,
    $disabled_events,
]);

$control_events = [];
$control = static function (string $text, int $phase) use (&$control_events): string {
    $control_events[] = [$text, $phase];
    return strtoupper($text);
};
ob_start();
ob_start($control);
echo 'a';
$control_clean = ob_clean();
$after_clean = normalize_status(ob_get_status(), $base);
echo 'b';
$control_flush = ob_flush();
$after_flush = normalize_status(ob_get_status(), $base);
echo 'c';
$control_raw = ob_get_clean();
$control_outer = ob_get_clean();
status_report('controls', [
    $control_clean,
    $after_clean,
    $control_flush,
    $after_flush,
    $control_raw,
    $control_outer,
    $control_events,
]);

$clean_disabled_events = [];
$clean_disable = static function (string $text, int $phase) use (&$clean_disabled_events): bool {
    $clean_disabled_events[] = [$text, $phase];
    return false;
};
ob_start();
ob_start($clean_disable);
echo 'x';
$clean_disabled = ob_clean();
$after_clean_disabled = normalize_status(ob_get_status(), $base);
echo 'y';
$clean_disabled_raw = ob_get_clean();
$clean_disabled_outer = ob_get_clean();
status_report('clean-disabled', [
    $clean_disabled,
    $after_clean_disabled,
    $clean_disabled_raw,
    $clean_disabled_outer,
    $clean_disabled_events,
]);

$diagnostics = [];
set_error_handler(
    static function (int $level, string $message) use (&$diagnostics): bool {
        $diagnostics[] = [$level, $message];
        return true;
    }
);
ob_start();
$validation = [
    status_capture(
        fn() => normalize_status(ob_get_status(null), $base)
    ),
    status_capture(
        fn() => normalize_status(
            ob_get_status(full_status: false),
            $base
        )
    ),
    status_capture(
        fn() => normalize_statuses(
            array_slice(ob_get_status(true), $base),
            $base
        )
    ),
    status_capture(fn() => ob_get_status([])),
    status_capture(fn() => ob_get_status(new stdClass())),
    status_capture(fn() => ob_get_status(false, true)),
    status_capture(fn() => ob_list_handlers(true)),
];
ob_end_clean();
restore_error_handler();
status_report('validation', [$validation, $diagnostics]);
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestHostBuildFacadesUserlandMatchEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
$original_tmpdir = getenv('TMPDIR');

var_dump(php_uname('n'), gethostname());
var_dump(
    is_string(gethostname()),
    gethostname() === php_uname('n'),
    strlen(gethostname()) > 0
);

putenv('TMPDIR=/tmp/php2go-temp////');
var_dump(sys_get_temp_dir());
putenv('TMPDIR=/tmp/php2go-changed');
var_dump(sys_get_temp_dir());

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});
foreach ([
    'x.so', '', "bad\0name", null, false, true, 0, 1, 1.5,
    [], new stdClass,
] as $index => $extension) {
    echo 'dl:', $index, ':';
    try {
        var_dump(dl($extension));
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
echo 'dl:named:';
var_dump(dl(extension_filename: 'named.so'));
foreach (['gethostname', 'sys_get_temp_dir'] as $function) {
    echo $function, ':arity:';
    try {
        $function(true);
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
try {
    dl();
} catch (Throwable $error) {
    echo get_class($error), ':', $error->getMessage(), "\n";
}
try {
    dl('x.so', true);
} catch (Throwable $error) {
    echo get_class($error), ':', $error->getMessage(), "\n";
}
restore_error_handler();

if ($original_tmpdir === false) {
    putenv('TMPDIR');
} else {
    putenv('TMPDIR=' . $original_tmpdir);
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestStreamRegistryMetadataUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
$functions = ['stream_get_wrappers', 'stream_get_transports', 'stream_get_filters'];
foreach ($functions as $function) {
    echo '==', $function, "==\n";
    var_dump($function());
    try {
        $function(true);
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
$copy = stream_get_wrappers();
$copy[] = 'private-test-wrapper';
var_dump([stream_get_wrappers(), $copy]);
`)
	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestUserStreamWrapperLifecycleAndIOMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
class BundleStreamWrapper {
    public $context;
    public static array $events = [];
    private string $data = 'abcdef';
    private int $position = 0;

    public function __construct() {
        self::$events[] = 'construct';
    }
    public function stream_open($path, $mode, $options, &$opened_path): bool {
        self::$events[] = 'open:' . $path . ':' . $mode . ':' . $options . ':' . get_resource_type($this->context);
        return true;
    }
    public function stream_read($count): string {
        self::$events[] = 'read:' . $count . ':' . $this->position;
        $result = substr($this->data, $this->position, $count);
        $this->position += strlen($result);
        return $result;
    }
    public function stream_write($data): int {
        self::$events[] = 'write:' . bin2hex($data) . ':' . $this->position;
        $this->data = substr($this->data, 0, $this->position) . $data . substr($this->data, $this->position + strlen($data));
        $this->position += strlen($data);
        return strlen($data);
    }
    public function stream_eof(): bool {
        self::$events[] = 'eof:' . $this->position;
        return $this->position >= strlen($this->data);
    }
    public function stream_tell(): int {
        self::$events[] = 'tell:' . $this->position;
        return $this->position;
    }
    public function stream_seek($offset, $whence): bool {
        self::$events[] = 'seek:' . $offset . ':' . $whence . ':' . $this->position;
        if ($whence === SEEK_SET) {
            $this->position = $offset;
        } elseif ($whence === SEEK_CUR) {
            $this->position += $offset;
        } else {
            $this->position = strlen($this->data) + $offset;
        }
        return true;
    }
    public function stream_flush(): bool {
        self::$events[] = 'flush';
        return true;
    }
    public function stream_stat(): array {
        self::$events[] = 'stat';
        return ['mode' => 0100666, 'size' => strlen($this->data)];
    }
    public function stream_close(): void {
        self::$events[] = 'close:' . $this->data . ':' . $this->position;
    }
}

class BundleDirectoryWrapper {
    public $context;
    public static array $events = [];
    private array $entries = ['.', '..', 'beta', 'alpha'];
    private int $position = 0;

    public function __construct() {
        self::$events[] = 'construct';
    }
    public function dir_opendir($path, $options): bool {
        self::$events[] = 'open:' . $path . ':' . $options . ':' . get_resource_type($this->context);
        return true;
    }
    public function dir_readdir(): string|false {
        self::$events[] = 'read:' . $this->position;
        return $this->entries[$this->position++] ?? false;
    }
    public function dir_rewinddir(): bool {
        self::$events[] = 'rewind:' . $this->position;
        $this->position = 0;
        return true;
    }
    public function dir_closedir(): void {
        self::$events[] = 'close:' . $this->position;
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

var_dump(stream_wrapper_register('bundleio', BundleStreamWrapper::class));
var_dump(in_array('bundleio', stream_get_wrappers(), true));
var_dump(stream_register_wrapper('bundleio', BundleStreamWrapper::class));

$context = stream_context_create();
$stream = fopen('bundleio://resource', 'r+', false, $context);
var_dump(is_resource($stream));
$metadata = stream_get_meta_data($stream);
var_dump([
    'timed_out' => $metadata['timed_out'],
    'blocked' => $metadata['blocked'],
    'eof' => $metadata['eof'],
    'wrapper_data' => get_class($metadata['wrapper_data']),
    'wrapper_context' => get_resource_type($metadata['wrapper_data']->context),
    'wrapper_type' => $metadata['wrapper_type'],
    'stream_type' => $metadata['stream_type'],
    'mode' => $metadata['mode'],
    'unread_bytes' => $metadata['unread_bytes'],
    'seekable' => $metadata['seekable'],
    'uri' => $metadata['uri'],
]);
var_dump(fread($stream, 2), feof($stream), ftell($stream));
var_dump(fseek($stream, 1, SEEK_CUR), ftell($stream));
var_dump(fwrite($stream, 'XY'), ftell($stream));
var_dump(rewind($stream), stream_get_contents($stream), feof($stream));
var_dump(fflush($stream));
$stat = fstat($stream);
var_dump($stat['mode'], $stat['size'], $stat['rdev'], $stat['blksize'], $stat['blocks']);
var_dump(fclose($stream));
var_dump(BundleStreamWrapper::$events);

var_dump(stream_wrapper_unregister('bundleio'));
var_dump(in_array('bundleio', stream_get_wrappers(), true));
var_dump(stream_wrapper_restore('bundleio'));
var_dump(stream_wrapper_unregister('file'));
var_dump(in_array('file', stream_get_wrappers(), true));
var_dump(stream_wrapper_register('file', BundleStreamWrapper::class));
var_dump(array_key_last(stream_get_wrappers()), stream_get_wrappers()[array_key_last(stream_get_wrappers())]);
var_dump(stream_wrapper_restore('file'));
var_dump(array_key_last(stream_get_wrappers()), stream_get_wrappers()[array_key_last(stream_get_wrappers())]);

var_dump(stream_wrapper_register('bundledir', BundleDirectoryWrapper::class));
$directory = opendir('bundledir://root', $context);
var_dump(is_resource($directory), readdir($directory), readdir($directory));
rewinddir($directory);
var_dump(readdir($directory));
closedir($directory);
var_dump(scandir('bundledir://root', SCANDIR_SORT_ASCENDING, $context));
var_dump(BundleDirectoryWrapper::$events);
var_dump(stream_wrapper_unregister('bundledir'));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestStreamContextUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function context_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

set_error_handler(function (int $level, string $message): true {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

$input = [
    'http' => ['method' => 'GET', 0 => 'ignored'],
    'empty' => [],
];
$context = stream_context_create($input, [
    'notification' => null,
    'ignored' => 'value',
    'options' => [
        'http' => ['timeout' => 3],
        'ftp' => ['overwrite' => true],
    ],
]);
var_dump(get_resource_type($context));
var_dump(stream_context_get_options($context));
var_dump(stream_context_get_params($context));

$input['http']['method'] = 'POST';
$copy = stream_context_get_options($context);
$copy['http']['method'] = 'COPY';
var_dump(stream_context_get_options($context));

var_dump(stream_context_set_option($context, 'http', 'method', 'HEAD'));
var_dump(stream_context_set_option($context, [
    'http' => ['header' => 'X-Test: yes'],
    'file' => ['mode' => 0600],
]));
var_dump(stream_context_set_options($context, [
    'http' => ['timeout' => 7],
]));
var_dump(stream_context_set_params($context, [
    'notification' => ['Notifier', 'handle'],
    'options' => ['ftp' => ['resume_pos' => 4]],
]));
var_dump(stream_context_get_options($context));
var_dump(stream_context_get_params($context));

$default_one = stream_context_get_default(['http' => ['protocol_version' => 1.1]]);
$default_two = stream_context_set_default(['ftp' => ['overwrite' => false]]);
$default_three = stream_context_get_default();
var_dump($default_one === $default_two, $default_two === $default_three);
var_dump(stream_context_get_options($default_three));

$explicit = stream_context_create(['http' => ['explicit' => true]]);
$stream = fopen('data:text/plain,abc', 'r', false, $explicit);
var_dump(stream_context_get_options($stream));
var_dump(stream_context_get_params($stream));
var_dump(stream_context_set_option($stream, 'data', 'tag', 9));
var_dump(stream_context_get_options($stream));
var_dump(stream_context_get_options($explicit));
fclose($stream);
context_case('closed stream', fn() => stream_context_get_options($stream));

context_case('numeric wrapper', fn() => stream_context_create([0 => []]));
context_case('scalar wrapper options', fn() => stream_context_create(['http' => 1]));
context_case('invalid params options', fn() => stream_context_create(null, ['options' => 1]));
context_case('array with option name', fn() => stream_context_set_option($context, [], 'name'));
context_case('array with value', fn() => stream_context_set_option($context, [], null, null));
context_case('string without option name', fn() => stream_context_set_option($context, 'http'));
context_case('string without value', fn() => stream_context_set_option($context, 'http', 'method'));
context_case('null resource', fn() => stream_context_get_options(null));
context_case('integer resource', fn() => stream_context_get_params(1));
context_case('array resource', fn() => stream_context_set_options([], []));
context_case('object resource', fn() => stream_context_set_params(new stdClass(), []));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestStreamClassificationUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	streamTarget := filepath.Join(t.TempDir(), "stream-classification.txt")
	if err := os.WriteFile(streamTarget, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(`<?php
function stream_classification_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
set_error_handler(function (int $level, string $message): true {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

$paths = [
    'empty' => '',
    'relative' => 'relative.txt',
    'absolute' => '/tmp/example',
    'file' => 'file:///tmp/example',
    'php' => 'PHP://memory',
    'glob' => 'glob://*',
    'data-colon' => 'data:text/plain,x',
    'data-upper-colon' => 'DATA:text/plain,x',
    'data-slashes' => 'DaTa://x',
    'http' => 'HTTP://example.com/x',
    'http-one-slash' => 'http:/example.com/x',
    'ftp' => 'ftp://example.com/x',
    'one-letter' => 'x://example',
    'unknown' => 'a+b.c-d://example',
    'invalid-scheme' => 'ab_cd://example',
];
foreach ($paths as $label => $path) {
    stream_classification_case('local path ' . $label, fn() => stream_is_local($path));
}
foreach ([null, 12, 1.5, true, []] as $value) {
    stream_classification_case(
        'local value ' . get_debug_type($value),
        fn() => stream_is_local($value)
    );
}
stream_classification_case(
    'local object',
    fn() => stream_is_local(new stdClass())
);

$file = fopen(__STREAM_PATH__, 'r');
$data = fopen('data:text/plain,x', 'r');
$context = stream_context_create();
stream_classification_case('local file', fn() => stream_is_local($file));
stream_classification_case('local data', fn() => stream_is_local($data));
stream_classification_case('local context', fn() => stream_is_local($context));
stream_classification_case('isatty file', fn() => stream_isatty($file));
stream_classification_case('isatty data', fn() => stream_isatty($data));
stream_classification_case('isatty context', fn() => stream_isatty($context));
foreach ([null, 12, [], new stdClass()] as $value) {
    stream_classification_case(
        'isatty value ' . get_debug_type($value),
        fn() => stream_isatty($value)
    );
}
$ptmx = @fopen('/dev/ptmx', 'r+');
if ($ptmx === false) {
    echo "==isatty ptmx unavailable==\n";
} else {
    stream_classification_case('isatty ptmx', fn() => stream_isatty($ptmx));
    fclose($ptmx);
}
fclose($file);
stream_classification_case('local closed', fn() => stream_is_local($file));
stream_classification_case('isatty closed', fn() => stream_isatty($file));
stream_classification_case('local missing argument', fn() => stream_is_local());
stream_classification_case('isatty excess argument', fn() => stream_isatty($data, true));
restore_error_handler();
`, "__STREAM_PATH__", fmt.Sprintf("%q", streamTarget), 1)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestStreamControlsUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	streamTarget := filepath.Join(t.TempDir(), "stream-options.txt")
	if err := os.WriteFile(streamTarget, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(`<?php
function stream_control_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

$path = __STREAM_PATH__;
$file = fopen($path, 'r');
stream_control_case('file controls', fn() => [
    stream_set_blocking($file, false),
    socket_set_blocking($file, true),
    stream_set_timeout($file, 2, 345),
    socket_set_timeout($file, 3),
    stream_set_read_buffer($file, 16),
    stream_set_write_buffer($file, 16),
    set_file_buffer($file, 0),
    stream_set_chunk_size($file, 4096),
    stream_set_chunk_size($file, 1234),
]);
stream_control_case('file metadata', fn() => stream_get_meta_data($file));
stream_control_case('file metadata alias', fn() => socket_get_status($file));
fclose($file);

$data = fopen('data:text/plain;base64,YWJj', 'r');
stream_control_case('data controls', fn() => [
    stream_set_blocking($data, false),
    stream_set_timeout($data, -1, 1000000),
    stream_set_read_buffer($data, -1),
    stream_set_write_buffer($data, -1),
    stream_set_chunk_size($data, 2048),
]);
stream_control_case('data metadata', fn() => stream_get_meta_data($data));
stream_control_case('chunk zero', fn() => stream_set_chunk_size($data, 0));
stream_control_case(
    'chunk large',
    fn() => stream_set_chunk_size($data, 2147483648)
);
fclose($data);
`, "__STREAM_PATH__", fmt.Sprintf("%q", streamTarget), 1)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}

func TestStreamOperationsUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	streamTarget := filepath.Join(t.TempDir(), "stream-operations.txt")
	source := strings.Replace(`<?php
function stream_operation_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

$path = __STREAM_PATH__;
file_put_contents($path, 'abcdef');
$file = fopen($path, 'r+');

stream_operation_case('file operations', fn() => [
    fflush($file),
    fsync($file),
    fdatasync($file),
    stream_supports_lock($file),
]);

$stat = fstat($file);
stream_operation_case('file stat', fn() => [
    count($stat),
    array_keys($stat),
    $stat[0] === $stat['dev'],
    $stat[1] === $stat['ino'],
    $stat[2] === $stat['mode'],
    $stat[7] === $stat['size'],
    $stat['mode'],
    $stat['size'],
    $stat['rdev'],
    $stat['blksize'],
    $stat['blocks'],
]);

stream_operation_case('file locks', function () use ($file) {
    $wouldBlock = 99;
    $locked = flock($file, LOCK_EX | LOCK_NB, $wouldBlock);
    $unlocked = flock($file, LOCK_UN);
    return [$locked, $wouldBlock, $unlocked];
});
stream_operation_case('flock invalid', fn() => flock($file, LOCK_NB));

fseek($file, 2);
stream_operation_case('file truncate', function () use ($file) {
    $truncated = ftruncate($file, 3);
    $position = ftell($file);
    $size = fstat($file)['size'];
    rewind($file);
    return [$truncated, $position, $size, stream_get_contents($file)];
});
stream_operation_case('truncate negative', fn() => ftruncate($file, -1));
fclose($file);

$data = fopen('data:text/plain,abc', 'r');
set_error_handler(static function (int $level, string $message): bool {
    echo 'warning:', $message, "\n";
    return true;
});
stream_operation_case('data operations', fn() => [
    fflush($data),
    fsync($data),
    fdatasync($data),
    stream_supports_lock($data),
]);
restore_error_handler();
$stat = fstat($data);
stream_operation_case('data stat', fn() => [
    count($stat),
    $stat['dev'],
    $stat['ino'],
    $stat['mode'],
    $stat['size'],
    $stat['atime'],
    $stat['blksize'],
    $stat[7] === $stat['size'],
]);
stream_operation_case('data lock', function () use ($data) {
    $wouldBlock = 99;
    return [flock($data, LOCK_EX | LOCK_NB, $wouldBlock), $wouldBlock];
});
fseek($data, 2);
stream_operation_case('data truncate', function () use ($data) {
    $truncated = ftruncate($data, 6);
    $position = ftell($data);
    $size = fstat($data)['size'];
    rewind($data);
    return [$truncated, $position, $size, bin2hex(stream_get_contents($data))];
});
fclose($data);
stream_operation_case('closed flush', fn() => fflush($data));
`, "__STREAM_PATH__", fmt.Sprintf("%q", streamTarget), 1)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}

func TestStreamSelectUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	streamTarget := filepath.Join(t.TempDir(), "stream-select.txt")
	mustWrite(t, streamTarget, "abcdef")
	source := strings.Replace(`<?php
function stream_select_case(
    string $label,
    mixed &$read,
    mixed &$write,
    mixed &$except,
    mixed $seconds,
    mixed $microseconds = null
): void {
    echo '==', $label, "==\n";
    try {
        $result = stream_select(
            $read,
            $write,
            $except,
            $seconds,
            $microseconds
        );
        var_dump(
            $result,
            $read === null ? null : array_keys($read),
            $write === null ? null : array_keys($write),
            $except === null ? null : array_keys($except)
        );
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
function stream_select_error_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

$file = fopen(__STREAM_PATH__, 'r+');
$read = ['one' => $file, 'two' => $file];
$write = ['write' => $file];
$except = ['except' => $file];
stream_select_case('file duplicate sets', $read, $write, $except, 0);

$data = fopen('data:text/plain,abc', 'r');
$read = [7 => $data];
$write = ['write' => $data];
$except = ['except' => $data];
stream_select_case('data sets', $read, $write, $except, 0);
fread($data, 3);
$read = ['eof-position' => $data];
$write = $except = null;
stream_select_case('data at end', $read, $write, $except, null, 0);


$held = $data;
$read = [&$held];
$write = $except = null;
stream_select_case('referenced element', $read, $write, $except, 0);
$read[0] = null;
var_dump(is_resource($held));
$alias = ['same' => $file];
$write = null;
stream_select_case('read except alias', $alias, $write, $alias, 0);

$read = $write = null;
$except = ['wait' => $file];
stream_select_case('exception timeout', $read, $write, $except, 0, 1000);

$read = [$data];
$write = $except = null;
stream_select_case('large microseconds ready', $read, $write, $except, 0, 1000001);
stream_select_case('coerced seconds', $read, $write, $except, '0');

$read = $write = $except = null;
stream_select_case('all null', $read, $write, $except, 0);
$read = [];
stream_select_case('empty', $read, $write, $except, 0);
$read = [1];
stream_select_case('invalid only', $read, $write, $except, 0);
$read = [$data, 1];
stream_select_case('invalid argument mixed', $read, $write, $except, 0);
$read = [$data, stream_context_create()];
stream_select_case('invalid resource mixed', $read, $write, $except, 0);
$closed = fopen('data:text/plain,x', 'r');
fclose($closed);
$read = [$data, $closed];
stream_select_case('closed mixed', $read, $write, $except, 0);

$read = [$data];
stream_select_case('negative seconds', $read, $write, $except, -1);
stream_select_case('negative microseconds', $read, $write, $except, 0, -1);
stream_select_case('microseconds with null seconds', $read, $write, $except, null, 1);
stream_select_case('negative microseconds with null seconds', $read, $write, $except, null, -1);

$bad = 1;
stream_select_case('nonarray read', $bad, $write, $except, 0);
stream_select_error_case(
    'missing',
    function () use (&$read, &$write, &$except) {
        return stream_select($read, $write, $except);
    }
);
stream_select_error_case(
    'excess',
    function () use (&$read, &$write, &$except) {
        return stream_select($read, $write, $except, 0, null, true);
    }
);
stream_select_error_case(
    'named',
    function () use (&$read, &$write, &$except) {
        return stream_select(
            read: $read,
            write: $write,
            except: $except,
            seconds: 0,
            microseconds: null
        );
    }
);
`, "__STREAM_PATH__", fmt.Sprintf("%q", streamTarget), 1)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}

func TestStreamSocketPairUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function socket_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

foreach ([
    'stream' => STREAM_SOCK_STREAM,
    'dgram' => STREAM_SOCK_DGRAM,
    'seqpacket' => STREAM_SOCK_SEQPACKET,
    'raw' => STREAM_SOCK_RAW,
] as $label => $type) {
    echo '==pair ', $label, "==\n";
    $pair = stream_socket_pair(STREAM_PF_UNIX, $type, 0);
    var_dump(is_array($pair), count($pair));
    var_dump(stream_get_meta_data($pair[0]));
    fclose($pair[0]);
    fclose($pair[1]);
}
socket_case(
    'pair inet',
    fn() => stream_socket_pair(STREAM_PF_INET, STREAM_SOCK_STREAM, 0)
);
socket_case(
    'pair bad type',
    fn() => stream_socket_pair(STREAM_PF_UNIX, 0, 0)
);
socket_case(
    'pair bad protocol',
    fn() => stream_socket_pair(STREAM_PF_UNIX, STREAM_SOCK_STREAM, 6)
);

[$left, $right] = stream_socket_pair(
    STREAM_PF_UNIX,
    STREAM_SOCK_STREAM,
    0
);
var_dump(
    stream_socket_get_name($left, false),
    stream_socket_get_name($left, true),
    stream_set_blocking($right, false),
    stream_set_timeout($right, 0, 20000),
    stream_set_read_buffer($right, 0),
    stream_set_write_buffer($right, 0),
    stream_set_chunk_size($right, 2048),
    stream_supports_lock($right),
    stream_is_local($right),
    stream_isatty($right),
    count(fstat($right)),
    stream_context_get_options($right)
);
var_dump(fread($right, 1), feof($right));
var_dump(fgets($right), feof($right));
var_dump(stream_get_meta_data($right));

var_dump(fwrite($left, 'abc'));
$read = ['ready' => $right];
$write = $except = null;
var_dump(stream_select($read, $write, $except, 0), array_keys($read));
var_dump(fread($right, 3), feof($right));

var_dump(stream_socket_sendto($left, 'XYZ'));
$from = 'sentinel';
var_dump(
    stream_socket_recvfrom($right, 2, STREAM_PEEK, $from),
    $from,
    stream_socket_recvfrom($right, 3, 0, $from),
    $from
);
socket_case(
    'send address',
    fn() => stream_socket_sendto($left, 'x', 0, 'not-an-address')
);
stream_set_blocking($right, false);
$from = 'sentinel';
var_dump(stream_socket_recvfrom($right, 1, 0, $from), $from);

stream_set_blocking($right, true);
stream_set_timeout($right, 0, 10000);
var_dump(
    fread($right, 1),
    feof($right),
    stream_get_meta_data($right)['timed_out']
);
fwrite($left, 'z');
var_dump(fread($right, 1), stream_get_meta_data($right)['timed_out']);

socket_case(
    'crypto method required',
    fn() => stream_socket_enable_crypto($left, true)
);
socket_case(
    'crypto disable',
    fn() => stream_socket_enable_crypto($left, false)
);
socket_case(
    'crypto enable',
    fn() => stream_socket_enable_crypto(
        $left,
        true,
        STREAM_CRYPTO_METHOD_TLS_CLIENT
    )
);
socket_case(
    'crypto scalar session',
    fn() => stream_socket_enable_crypto(
        $left,
        true,
        STREAM_CRYPTO_METHOD_TLS_CLIENT,
        1
    )
);
socket_case(
    'crypto context session',
    fn() => stream_socket_enable_crypto(
        $left,
        true,
        STREAM_CRYPTO_METHOD_TLS_CLIENT,
        stream_context_create()
    )
);

$file = fopen('data:text/plain,x', 'r');
$context = stream_context_create();
socket_case('name file', fn() => stream_socket_get_name($file, false));
socket_case('name context', fn() => stream_socket_get_name($context, false));
socket_case('name scalar', fn() => stream_socket_get_name(1, false));
$from = 'sentinel';
var_dump(stream_socket_recvfrom($file, 1, 0, $from), $from);
socket_case('recv zero', fn() => stream_socket_recvfrom($left, 0));
socket_case('send file', fn() => stream_socket_sendto($file, 'x'));
socket_case('shutdown file', fn() => stream_socket_shutdown($file, STREAM_SHUT_RD));
socket_case('shutdown mode', fn() => stream_socket_shutdown($left, 3));
socket_case(
    'crypto file',
    fn() => stream_socket_enable_crypto(
        $file,
        true,
        STREAM_CRYPTO_METHOD_TLS_CLIENT
    )
);

[$writer, $reader] = stream_socket_pair(
    STREAM_PF_UNIX,
    STREAM_SOCK_STREAM,
    0
);
fwrite($writer, 'x');
var_dump(
    stream_socket_shutdown($writer, STREAM_SHUT_WR),
    stream_socket_shutdown($writer, STREAM_SHUT_WR),
    fread($reader, 2),
    feof($reader),
    fwrite($writer, 'y')
);

$named = stream_socket_pair(
    protocol: 0,
    type: STREAM_SOCK_STREAM,
    domain: STREAM_PF_UNIX
);
var_dump(is_array($named));
fclose($named[0]);
fclose($named[1]);
socket_case('pair missing', fn() => stream_socket_pair(STREAM_PF_UNIX, STREAM_SOCK_STREAM));
socket_case('name excess', fn() => stream_socket_get_name($left, false, true));

fclose($left);
socket_case('name closed', fn() => stream_socket_get_name($left, false));
fclose($right);
fclose($writer);
fclose($reader);
fclose($file);
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}

func TestAddressedStreamSocketsUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function addressed_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

$context = stream_context_create(['socket' => ['backlog' => 3]]);
$errno = 99;
$error = 'old';
$server = stream_socket_server(
    'tcp://127.0.0.1:0',
    $errno,
    $error,
    STREAM_SERVER_BIND | STREAM_SERVER_LISTEN,
    $context
);
$name = stream_socket_get_name($server, false);
var_dump(
    is_resource($server),
    $errno,
    $error,
    str_starts_with($name, '127.0.0.1:'),
    stream_get_meta_data($server)['stream_type'],
    stream_context_get_options($server)
);
stream_context_set_option($context, 'socket', 'later', 9);
var_dump(stream_context_get_options($server)['socket']['later']);

$errno = 98;
$error = 'before';
$client = stream_socket_client('tcp://' . $name, $errno, $error, 0.5);
$peer = 'sentinel';
$accepted = stream_socket_accept($server, 0.5, $peer);
var_dump(
    is_resource($client),
    is_resource($accepted),
    $errno,
    $error,
    str_starts_with($peer, '127.0.0.1:'),
    stream_socket_get_name($client, false) === $peer,
    stream_socket_get_name($client, true) === $name,
    stream_get_meta_data($client)['stream_type'],
    stream_get_meta_data($accepted)['stream_type'],
    stream_context_get_options($accepted)['socket']['later']
);
var_dump(fwrite($client, 'abc'), fread($accepted, 3));
var_dump(fwrite($accepted, 'xy'), fread($client, 2));
fclose($accepted);
fclose($client);

$peer = 'unchanged';
addressed_case(
    'accept timeout',
    fn() => stream_socket_accept($server, 0.001, $peer)
);
var_dump($peer);

foreach (['fsockopen', 'pfsockopen'] as $legacy) {
    $parts = explode(':', $name);
    $legacyErrno = 7;
    $legacyError = 'old';
    $legacyClient = $legacy(
        '127.0.0.1',
        (int) $parts[1],
        $legacyErrno,
        $legacyError,
        0.5
    );
    if ($legacy === 'pfsockopen') {
        $samePersistent = pfsockopen(
            '127.0.0.1',
            (int) $parts[1],
            $sameErrno,
            $sameError,
            0.5
        );
        var_dump(
            $samePersistent === $legacyClient,
            (int) $samePersistent === (int) $legacyClient,
            $sameErrno,
            $sameError
        );
    }
    $legacyAccepted = stream_socket_accept($server, 0.5);
    var_dump(
        $legacy,
        is_resource($legacyClient),
        is_resource($legacyAccepted),
        $legacyErrno,
        $legacyError,
        fwrite($legacyClient, 'L'),
        fread($legacyAccepted, 1)
    );
    if ($legacy === 'pfsockopen') {
        $firstLocal = stream_socket_get_name($legacyClient, false);
        fclose($legacyAccepted);
        fclose($legacyClient);
        $afterClose = pfsockopen(
            '127.0.0.1',
            (int) $parts[1],
            $afterErrno,
            $afterError,
            0.5
        );
        $afterAccepted = stream_socket_accept($server, 0.5);
        var_dump(
            is_resource($afterClose),
            is_resource($afterAccepted),
            stream_socket_get_name($afterClose, false) !== $firstLocal,
            $afterErrno,
            $afterError
        );
        fclose($afterAccepted);
        fclose($afterClose);
    } else {
        fclose($legacyAccepted);
        fclose($legacyClient);
    }
}

$async = stream_socket_client(
    'tcp://' . $name,
    $asyncErrno,
    $asyncError,
    0.5,
    STREAM_CLIENT_ASYNC_CONNECT
);
$asyncRead = null;
$asyncWrite = [$async];
$asyncExcept = null;
$asyncReady = stream_select(
    $asyncRead,
    $asyncWrite,
    $asyncExcept,
    1
);
$asyncPeer = 'sentinel';
$asyncAccepted = stream_socket_accept($server, -1, $asyncPeer);
var_dump(
    is_resource($async),
    $asyncErrno,
    $asyncError,
    $asyncReady,
    count($asyncWrite),
    is_resource($asyncAccepted),
    str_starts_with($asyncPeer, '127.0.0.1:'),
    stream_get_meta_data($async)['blocked']
);
fclose($asyncAccepted);
fclose($async);

$unbound = stream_socket_server(
    'tcp://127.0.0.1:0',
    $unboundErrno,
    $unboundError,
    0
);
var_dump(
    is_resource($unbound),
    $unboundErrno,
    $unboundError,
    stream_socket_get_name($unbound, false),
    stream_get_meta_data($unbound)['stream_type']
);
fclose($unbound);
$unconnected = stream_socket_client(
    'tcp://127.0.0.1:1',
    $unconnectedErrno,
    $unconnectedError,
    0.01,
    0
);
var_dump(
    is_resource($unconnected),
    $unconnectedErrno,
    $unconnectedError,
    stream_socket_get_name($unconnected, false),
    stream_socket_get_name($unconnected, true)
);
fclose($unconnected);

$udp = stream_socket_server(
    'udp://127.0.0.1:0',
    $udpErrno,
    $udpError,
    STREAM_SERVER_BIND
);
$udpName = stream_socket_get_name($udp, false);
$udpClient = stream_socket_client('udp://' . $udpName, $udpClientErrno, $udpClientError, 0.5);
var_dump(
    is_resource($udp),
    is_resource($udpClient),
    $udpErrno,
    $udpError,
    $udpClientErrno,
    $udpClientError,
    stream_get_meta_data($udp)['stream_type'],
    fwrite($udpClient, 'dat')
);
$from = 'sentinel';
var_dump(
    stream_socket_recvfrom($udp, 3, 0, $from),
    str_starts_with($from, '127.0.0.1:'),
    stream_socket_sendto($udp, 'z', 0, $from),
    fread($udpClient, 1)
);
fclose($udpClient);
fclose($udp);

addressed_case('udp default flags', function () {
    $errno = 7;
    $message = 'old';
    $result = stream_socket_server('udp://127.0.0.1:0', $errno, $message);
    var_dump($errno, $message);
    return $result;
});

$udgPath = sys_get_temp_dir() . '/php2go-addressed-' . getmypid() . '.udg';
@unlink($udgPath);
$udgServer = stream_socket_server(
    'udg://' . $udgPath,
    $udgErrno,
    $udgError,
    STREAM_SERVER_BIND
);
$udgClient = stream_socket_client(
    'udg://' . $udgPath,
    $udgClientErrno,
    $udgClientError,
    0.5
);
$udgFrom = 'sentinel';
var_dump(
    is_resource($udgServer),
    is_resource($udgClient),
    $udgErrno,
    $udgError,
    $udgClientErrno,
    $udgClientError,
    stream_get_meta_data($udgServer)['stream_type'],
    fwrite($udgClient, 'g'),
    stream_socket_recvfrom($udgServer, 1, 0, $udgFrom)
);
fclose($udgClient);
fclose($udgServer);
@unlink($udgPath);

$path = sys_get_temp_dir() . '/php2go-addressed-' . getmypid() . '.sock';
@unlink($path);
$unixServer = stream_socket_server('unix://' . $path, $unixErrno, $unixError);
$unixClient = stream_socket_client('unix://' . $path, $unixClientErrno, $unixClientError, 0.5);
$unixAccepted = stream_socket_accept($unixServer, 0.5, $unixPeer);
var_dump(
    is_resource($unixServer),
    is_resource($unixClient),
    is_resource($unixAccepted),
    $unixErrno,
    $unixError,
    $unixClientErrno,
    $unixClientError,
    stream_socket_get_name($unixServer, false) === $path,
    stream_get_meta_data($unixServer)['stream_type'],
    fwrite($unixClient, 'u'),
    fread($unixAccepted, 1)
);
fclose($unixAccepted);
fclose($unixClient);
fclose($unixServer);
@unlink($path);

addressed_case('server parse', function () {
    $errno = 7;
    $message = 'old';
    $result = stream_socket_server('tcp://127.0.0.1', $errno, $message);
    var_dump($errno, $message);
    return $result;
});
addressed_case('server transport', fn() => stream_socket_server('bogus://x'));
addressed_case('client refused', function () {
    $errno = 7;
    $message = 'old';
    $result = stream_socket_client('tcp://127.0.0.1:1', $errno, $message, 0.01);
    var_dump($errno, $message);
    return $result;
});
addressed_case('legacy missing port', fn() => fsockopen('127.0.0.1'));
$e = null;
$m = null;
addressed_case('client finite', fn() => stream_socket_client('tcp://127.0.0.1:1', $e, $m, NAN));
addressed_case(
    'client context scalar',
    fn() => stream_socket_client('tcp://127.0.0.1:1', $e, $m, 0.01, STREAM_CLIENT_CONNECT, 1)
);
$data = fopen('data:,x', 'r');
addressed_case(
    'client context stream',
    fn() => stream_socket_client('tcp://127.0.0.1:1', $e, $m, 0.01, STREAM_CLIENT_CONNECT, $data)
);
addressed_case('accept data', fn() => stream_socket_accept($data, 0));
addressed_case('accept scalar', fn() => stream_socket_accept(1));
fclose($data);

$named = stream_socket_server(
    flags: STREAM_SERVER_BIND | STREAM_SERVER_LISTEN,
    address: 'tcp://127.0.0.1:0',
    error_message: $namedMessage,
    error_code: $namedErrno
);
var_dump(is_resource($named), $namedErrno, $namedMessage);
fclose($named);
addressed_case('server missing', fn() => stream_socket_server());
addressed_case('client excess', fn() => stream_socket_client('tcp://127.0.0.1:1', $e, $m, 0.1, 0, null, true));

fclose($server);
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}
func TestPFSockopenPersistsAcrossBundleRequestsLikeEngine(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	connections := make(chan net.Conn, 4)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			connections <- connection
		}
	}()
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), fmt.Sprintf(`<?php
$socket = pfsockopen('127.0.0.1', %d, $errno, $message, 0.5);
echo stream_socket_get_name($socket, false), "\n";
`, port))

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	require := fmt.Sprintf("require %q;", filepath.Join(root, "main.php"))
	engineFirst, err := engine.Eval(require)
	if err != nil {
		engine.Close()
		t.Fatal(err)
	}
	if err := engine.Reset(); err != nil {
		engine.Close()
		t.Fatal(err)
	}
	engineSecond, err := engine.Eval(require)
	engine.Close()
	if err != nil {
		t.Fatal(err)
	}

	var bundleFirst bytes.Buffer
	if _, err := app.Run(php.NewCtx(&bundleFirst)); err != nil {
		t.Fatal(err)
	}
	var bundleSecond bytes.Buffer
	if _, err := app.Run(php.NewCtx(&bundleSecond)); err != nil {
		t.Fatal(err)
	}

	engineReused := engineFirst != "" && engineFirst == engineSecond
	bundleReused := bundleFirst.Len() != 0 &&
		bundleFirst.String() == bundleSecond.String()
	if bundleReused != engineReused || !engineReused {
		t.Fatalf(
			"persistent reuse: bundle=(%q,%q,%v), engine=(%q,%q,%v)",
			bundleFirst.String(), bundleSecond.String(), bundleReused,
			engineFirst, engineSecond, engineReused,
		)
	}

	accepted := make([]net.Conn, 0, 2)
	for len(accepted) < 2 {
		select {
		case connection := <-connections:
			accepted = append(accepted, connection)
		case <-time.After(2 * time.Second):
			t.Fatalf("accepted %d connections, want 2", len(accepted))
		}
	}
	defer func() {
		for _, connection := range accepted {
			_ = connection.Close()
		}
	}()
	select {
	case extra := <-connections:
		_ = extra.Close()
		t.Fatal("pfsockopen created a third connection instead of reusing")
	case <-time.After(100 * time.Millisecond):
	}
}
func TestShellEscapingUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
$quote = chr(34);
$apostrophe = chr(39);
$slash = chr(92);
$nl = chr(10);
$high = 255;
function shell_bytes(string $hex): string {
    return hex2bin($hex);
}
$special = '#&;' . chr(96) . '|*?~<>^()[]{}$' . $slash . $nl . chr($high) .
    $quote . $apostrophe;

foreach ([
    '',
    'abc XYZ-_=+.,:/',
    $apostrophe,
    $quote,
    'a' . $apostrophe . 'b',
    'a' . $quote . 'b',
    $apostrophe . 'a',
    'a' . $apostrophe,
    $quote . 'a',
    'a' . $quote,
    'a' . $apostrophe . $apostrophe . 'b',
    'a' . $quote . $quote . 'b',
    'a' . $apostrophe . ' b ' . $apostrophe . 'c',
    'a' . $quote . ' b ' . $quote . 'c',
    $special,
    shell_bytes('c3a9'),
    shell_bytes('e282ac'),
    shell_bytes('f09f9880'),
    shell_bytes('c0af'),
    shell_bytes('e228a1'),
    shell_bytes('eda080'),
    shell_bytes('f4908080'),
    shell_bytes('f7bfbfbf'),
    shell_bytes('f887bfbfbf'),
    shell_bytes('f888808080'),
    shell_bytes('fbbfbfbfbf'),
    shell_bytes('fc8480808080'),
    shell_bytes('fdbfbfbfbfbf'),
    shell_bytes('ff41'),
] as $value) {
    var_dump(bin2hex(escapeshellarg($value)), bin2hex(escapeshellcmd($value)));
}
for ($byte = 1; $byte < 256; $byte++) {
    $value = chr($byte);
    var_dump(bin2hex(escapeshellarg($value)), bin2hex(escapeshellcmd($value)));
}

foreach ([
    fn() => escapeshellarg('a' . chr(0) . 'b'),
    fn() => escapeshellcmd('a' . chr(0) . 'b'),
    fn() => escapeshellarg(str_repeat('a', 2097150)),
    fn() => escapeshellcmd(str_repeat('a', 2097150)),
] as $invalid) {
    try {
        $invalid();
    } catch (ValueError $error) {
        echo get_class($error), ':', $error->getMessage(), $nl;
    }
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestCSVUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	csvTarget := filepath.Join(t.TempDir(), "records.csv")
	mustWrite(t, filepath.Join(root, "main.php"), fmt.Sprintf(`<?php
$quote = chr(34);
$slash = chr(92);
$nl = chr(10);
$cr = chr(13);
$tab = chr(9);
set_error_handler(function ($level, $message) use ($nl) {
    echo 'E:', $level, ':', $message, $nl;
    return true;
}, E_WARNING | E_DEPRECATED);

var_dump(str_getcsv(''));
foreach ([
    'a,b,',
    '  ' . $quote . 'a,b' . $quote . '  , x ',
    $quote . 'a' . $quote . $quote . 'b' . $quote . ',z',
    $quote . 'a' . $slash . $quote . 'b' . $quote . ',z',
    $quote . 'a' . $slash . 'qb' . $quote . ',z',
    $quote . 'a' . $quote . 'junk,z',
    $quote . 'a' . $nl . 'b' . $quote . ',z',
    $quote . 'unterminated',
    $cr . $nl,
] as $record) {
    var_dump(str_getcsv($record, ',', $quote, ''));
}
var_dump(str_getcsv($quote . 'a' . $quote . $quote . $quote, ',', $quote, $quote));
var_dump(str_getcsv($slash . 'a' . $slash . $slash . $slash, ',', $slash, $slash));
foreach ([
    fn() => str_getcsv('x', '', $quote, ''),
    fn() => str_getcsv('x', ',', '', ''),
    fn() => str_getcsv('x', ',', $quote, 'xx'),
] as $invalid) {
    try {
        $invalid();
    } catch (ValueError $error) {
        echo get_class($error), ':', $error->getMessage(), $nl;
    }
}

$path = %q;
file_put_contents(
    $path,
    'a,' . $quote . 'b' . $nl . 'c' . $quote . ',d' . $nl . $nl . 'x,y' . $nl
);
$stream = fopen($path, 'r');
while (($record = fgetcsv($stream, null, ',', $quote, '')) !== false) {
    var_dump($record);
}
var_dump($record);
fclose($stream);

file_put_contents($path, 'abcdef,gh' . $nl . 'X,Y' . $nl);
$stream = fopen($path, 'r');
while (($record = fgetcsv($stream, 4, ',', $quote, '')) !== false) {
    var_dump($record);
}
fclose($stream);

file_put_contents(
    $path,
    'a,' . $quote . 'bc' . $nl . 'def' . $quote . ',z' . $nl
);
$stream = fopen($path, 'r');
var_dump(
    fgetcsv($stream, 4, ',', $quote, ''),
    fgetcsv($stream, 4, ',', $quote, '')
);
fclose($stream);

$stream = fopen($path, 'w+');
var_dump(fputcsv(
    $stream,
    ['a', 'b,c', 'd' . $quote . 'e', 'f' . $slash . 'g', 'h i',
     'j' . $tab . 'k', 'l' . $nl . 'm', 'n' . $cr . 'o', ''],
    ',',
    $quote,
    '',
    '--'
));
var_dump(fputcsv(
    $stream,
    ['a' . $slash . $quote . 'b', 'a' . $slash . 'b'],
    ',',
    $quote,
    $slash
));
var_dump(fputcsv($stream, [null, false, true, 12, 1.5, [1]], ',', $quote, ''));
rewind($stream);
var_dump(fread($stream, 1000));
fclose($stream);

$stream = fopen($path, 'r');
foreach ([
    fn() => fgetcsv($stream, -1, ',', $quote, ''),
    fn() => fgetcsv($stream, PHP_INT_MAX, ',', $quote, ''),
    fn() => fputcsv($stream, [], '', $quote, ''),
    fn() => fputcsv($stream, [], ',', 'xx', ''),
    fn() => fputcsv($stream, [], ',', $quote, 'xx'),
] as $invalid) {
    try {
        $invalid();
    } catch (ValueError $error) {
        echo get_class($error), ':', $error->getMessage(), $nl;
    }
}
fclose($stream);
try {
    fgetcsv($stream, null, ',', $quote, '');
} catch (TypeError $error) {
    echo get_class($error), ':', $error->getMessage(), $nl;
}
try {
    fputcsv(42, [], ',', $quote, '');
} catch (TypeError $error) {
    echo get_class($error), ':', $error->getMessage(), $nl;
}
unlink($path);
restore_error_handler();
`, csvTarget))

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestCryptNativeUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
class CryptStringable {
    public function __construct(private string $value) {}
    public function __toString(): string {
        echo "stringify:", $this->value, "\n";
        return $this->value;
    }
}

function crypt_case(string $label, callable $probe): void {
    echo $label, ":";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});

foreach ([
    ["des", "password", "rl"],
    ["extended-des", "password", "_J9..rasm"],
    ["md5", "password", "$1$salt$"],
    ["sha256", "password", "$5$salt$"],
    ["sha256-rounds", "password", "$5$rounds=10000$salt$"],
    ["sha512", "password", "$6$salt$"],
    ["sha512-rounds", "password", "$6$rounds=10000$salt$"],
    ["bcrypt-a", "password", "$2a$04$abcdefghijklmnopqrstuu"],
    ["bcrypt-x", "password", "$2x$04$abcdefghijklmnopqrstuu"],
    ["bcrypt-y", "password", "$2y$04$abcdefghijklmnopqrstuu"],
    ["bcrypt-b", "password", "$2b$04$abcdefghijklmnopqrstuu"],
    ["empty", "password", ""],
    ["one-byte", "password", "r"],
    ["invalid", "password", "$9$salt$"],
    ["failure-zero", "password", "*0"],
    ["failure-one", "password", "*1"],
    ["nul-password", "pass\0tail", "$6$salt$"],
    ["nul-salt", "password", "rl\0tail"],
    ["long-salt", "password", str_repeat("a", 140)],
] as [$label, $password, $salt]) {
    crypt_case($label, fn() => crypt($password, $salt));
}

foreach ([
    null, false, true, 0, 1, 1.5,
    new CryptStringable("password"), [], new stdClass(),
] as $index => $password) {
    crypt_case("password-" . $index, fn() => crypt($password, "rl"));
}
foreach ([
    null, false, true, 0, 1, 1.5,
    new CryptStringable("rl"), [], new stdClass(),
] as $index => $salt) {
    crypt_case("salt-" . $index, fn() => crypt("password", $salt));
}

crypt_case("named", fn() => crypt(salt: "rl", string: "password"));
crypt_case("missing", fn() => crypt());
crypt_case("one-argument", fn() => crypt("password"));
crypt_case("too-many", fn() => crypt("password", "rl", true));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestPasswordMetadataAndIncludePathUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
class PasswordStringable {
    public function __construct(private string $value) {}
    public function __toString(): string {
        echo "stringify:", $this->value, "\n";
        return $this->value;
    }
}

function password_case(string $label, callable $probe): void {
    echo $label, ":";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}

function password_facts(string $hash): array {
    return [
        strlen($hash) === 60,
        substr($hash, 0, 7) === '$2y$04$',
        strspn(substr($hash, 7), './ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789') === 53,
        password_verify("secret", $hash),
        password_verify("wrong", $hash),
        password_get_info($hash),
        password_needs_rehash($hash, PASSWORD_BCRYPT, ["cost" => 4]),
        password_needs_rehash($hash, PASSWORD_BCRYPT, ["cost" => 5]),
    ];
}

set_error_handler(static function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});

foreach ([
    ["null", null], ["zero", 0], ["one", 1], ["name", "2y"],
    ["false", false], ["true", true], ["float", 1.5],
    ["stringable", new PasswordStringable("2y")],
    ["bad-int", 2], ["bad-name", "bcrypt"], ["empty", ""],
] as [$label, $algo]) {
    password_case("hash-algo-" . $label, static function () use ($algo) {
        return password_facts(password_hash("secret", $algo, ["cost" => 4]));
    });
}

foreach ([
    ["null", null], ["false", false], ["true", true], ["zero", 0],
    ["three", 3], ["four", 4], ["float", 4.9], ["string", "4"],
    ["string-bad", "x"], ["empty-array", []], ["array", [1]], ["too-high", 32],
] as [$label, $cost]) {
    password_case("hash-cost-" . $label, static function () use ($cost) {
        return password_facts(password_hash("secret", PASSWORD_BCRYPT, ["cost" => $cost]));
    });
}

password_case("hash-salt", static function () {
    return password_facts(password_hash(
        "secret",
        PASSWORD_BCRYPT,
        ["cost" => 4, "salt" => "ignored"]
    ));
});
password_case("hash-nul", fn() => password_hash("sec\0ret", PASSWORD_BCRYPT, ["cost" => 4]));
password_case("hash-password-stringable", static function () {
    return password_facts(password_hash(
        new PasswordStringable("secret"),
        PASSWORD_BCRYPT,
        ["cost" => 4]
    ));
});
password_case("hash-password-array", fn() => password_hash([], PASSWORD_BCRYPT, ["cost" => 4]));
password_case("hash-options-null", fn() => password_hash("secret", PASSWORD_BCRYPT, null));
password_case("hash-named", static function () {
    return password_facts(password_hash(
        options: ["cost" => 4],
        algo: PASSWORD_BCRYPT,
        password: "secret"
    ));
});
password_case("hash-missing", fn() => password_hash());
password_case("hash-too-many", fn() => password_hash("secret", PASSWORD_BCRYPT, [], true));

$first = password_hash("secret", PASSWORD_BCRYPT, ["cost" => 4]);
$second = password_hash("secret", PASSWORD_BCRYPT, ["cost" => 4]);
var_dump($first !== $second);
$long = str_repeat("a", 72);
$long_hash = password_hash($long . "x", PASSWORD_BCRYPT, ["cost" => 4]);
var_dump(password_verify($long . "y", $long_hash));

$dollar = chr(36);
$fixed = crypt("secret", $dollar . "2y" . $dollar . "04" . $dollar . "abcdefghijklmnopqrstuu");
foreach ([
    ["bcrypt-y", "secret", $fixed],
    ["wrong", "wrong", $fixed],
    ["nul-after", "secret\0tail", $fixed],
    ["nul-before", "sec\0ret", $fixed],
    ["bcrypt-a", "secret", substr_replace($fixed, "a", 2, 1)],
    ["bcrypt-b", "secret", substr_replace($fixed, "b", 2, 1)],
    ["bcrypt-x", "secret", substr_replace($fixed, "x", 2, 1)],
    ["bad-fourth", "secret", substr_replace($fixed, "X", 3, 1)],
    ["short", "secret", substr($fixed, 0, 59)],
    ["long", "secret", $fixed . "x"],
    ["md5", "secret", crypt("secret", $dollar . "1" . $dollar . "salt" . $dollar)],
    ["sha256", "secret", crypt("secret", $dollar . "5" . $dollar . "salt" . $dollar)],
    ["sha512", "secret", crypt("secret", $dollar . "6" . $dollar . "salt" . $dollar)],
    ["des", "secret", crypt("secret", "rl")],
    ["empty", "secret", ""],
] as [$label, $password, $hash]) {
    password_case("verify-" . $label, fn() => password_verify($password, $hash));
}
password_case("verify-password-array", fn() => password_verify([], $fixed));
password_case("verify-hash-array", fn() => password_verify("secret", []));
password_case("verify-named", fn() => password_verify(hash: $fixed, password: "secret"));
password_case("verify-missing", fn() => password_verify());
password_case("verify-too-many", fn() => password_verify("secret", $fixed, true));
restore_error_handler();

function bcryptish(string $cost): string {
    $dollar = chr(36);
    $prefix = $dollar . "2y" . $dollar . $cost . $dollar;
    return substr($prefix . str_repeat("x", 60), 0, 60);
}

var_dump(password_algos());
foreach (["04", "12", "xx", "-3", "+7", " 8", "999999999999999999999999",
          "-9223372036854775808", "-9223372036854775809"] as $cost) {
    $hash = bcryptish($cost);
    var_dump(password_get_info(hash: $hash));
    var_dump(password_needs_rehash(hash: $hash, algo: PASSWORD_BCRYPT, options: ["cost" => 12]));
}

foreach (["", "invalid", substr(bcryptish("12"), 0, 59), bcryptish("12") . "x"] as $hash) {
    var_dump(password_get_info($hash));
}
foreach ([null, 0, 1, "2y", "1", "argon2id", "bogus"] as $algo) {
    var_dump(password_needs_rehash("invalid", $algo));
}
foreach ([null, false, true, 0, 1, 12, 12.9, "12", "12x", "x12", [], [1]] as $cost) {
    var_dump(password_needs_rehash(bcryptish("12"), PASSWORD_BCRYPT, ["cost" => $cost]));
}

$old = get_include_path();
var_dump(set_include_path(include_path: "one:two"), get_include_path(), ini_get("include_path"));
try {
    set_include_path("bad\0path");
} catch (ValueError $error) {
    echo get_class($error), ":", $error->getMessage(), "\n";
}
var_dump(get_include_path());
set_include_path($old);
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestTickFunctionsMatchEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function tick_case(string $label, callable $probe): void {
    echo "==", $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}

function tick_print(string $label): void {
    echo "tick:", $label, "\n";
}

var_dump(register_tick_function("tick_print", "shared"));
declare(ticks=2) {
    echo "first\n";
}
declare(ticks=2) {
    echo "second\n";
}
var_dump(unregister_tick_function("tick_print"));

register_tick_function("tick_print", "duplicate-one");
register_tick_function("tick_print", "duplicate-two");
unregister_tick_function("tick_print");
declare(ticks=1) {
    echo "duplicate-body\n";
}
unregister_tick_function("tick_print");

register_tick_function("tick_print", "case-sensitive");
unregister_tick_function("TICK_PRINT");
declare(ticks=1) {
    echo "case-body\n";
}
unregister_tick_function("tick_print");

$closure_one = function (): void { echo "closure-one\n"; };
$closure_two = function (): void { echo "closure-two\n"; };
register_tick_function($closure_one);
register_tick_function($closure_two);
unregister_tick_function($closure_two);
declare(ticks=1) {
    echo "closure-body\n";
}
unregister_tick_function($closure_one);

class TickInvoker {
    public function __construct(public int $value) {}
    public function __invoke(): void { echo "invoker:", $this->value, "\n"; }
}
$invoker_one = new TickInvoker(1);
$invoker_two = new TickInvoker(1);
register_tick_function($invoker_one);
register_tick_function($invoker_two);
unregister_tick_function($invoker_two);
declare(ticks=1) {
    echo "invoker-body\n";
}
unregister_tick_function($invoker_two);

function tick_remove_later(): void {
    echo "remove-later\n";
    unregister_tick_function("tick_later");
}
function tick_later(): void { echo "later\n"; }
register_tick_function("tick_remove_later");
register_tick_function("tick_later");
declare(ticks=1) {
    echo "remove-body\n";
}
unregister_tick_function("tick_remove_later");

$tick_appended = false;
function tick_append(): void {
    global $tick_appended;
    echo "append\n";
    if (!$tick_appended) {
        $tick_appended = true;
        register_tick_function("tick_tail");
    }
}
function tick_tail(): void { echo "tail\n"; }
register_tick_function("tick_append");
declare(ticks=1) {
    echo "append-body\n";
}
unregister_tick_function("tick_append");
unregister_tick_function("tick_tail");

function tick_self_remove(): void {
    echo "self-remove:";
    try {
        unregister_tick_function("tick_self_remove");
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}
register_tick_function("tick_self_remove");
declare(ticks=1) {
    echo "self-body\n";
}
unregister_tick_function("tick_self_remove");

$nested_once = false;
function tick_nested_a(): void {
    global $nested_once;
    echo "nested-a\n";
    if (!$nested_once) {
        $nested_once = true;
        declare(ticks=1) {
            echo "nested-inner\n";
        }
    }
}
function tick_nested_b(): void { echo "nested-b\n"; }
register_tick_function("tick_nested_a");
register_tick_function("tick_nested_b");
declare(ticks=1) {
    echo "nested-body\n";
}
unregister_tick_function("tick_nested_a");
unregister_tick_function("tick_nested_b");

function tick_throw(): void { throw new Exception("tick boom"); }
function tick_after_throw(): void { echo "after-throw\n"; }
register_tick_function("tick_throw");
register_tick_function("tick_after_throw");
try {
    declare(ticks=1) {
        echo "throw-body\n";
    }
} catch (Throwable $error) {
    echo $error::class, ":", $error->getMessage(), "\n";
}
unregister_tick_function("tick_throw");
unregister_tick_function("tick_after_throw");

function tick_suspend(): void {
    try {
        Fiber::suspend();
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}
register_tick_function("tick_suspend");
$fiber = new Fiber(function (): void {
    declare(ticks=1) {
        echo "fiber-body\n";
    }
});
$fiber->start();
unregister_tick_function("tick_suspend");

set_error_handler(function (int $level, string $message): true {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});
class TickScope {
    public static function install(): void {
        register_tick_function(["self", "callback"]);
    }
    public static function callback(): void { echo "scoped\n"; }
}
TickScope::install();
declare(ticks=1) {
    echo "scope-body\n";
}
unregister_tick_function(["TickScope", "callback"]);
restore_error_handler();

tick_case("invalid register", fn() => register_tick_function("does_not_exist"));
tick_case("invalid unregister", fn() => unregister_tick_function("does_not_exist"));
tick_case("missing register", fn() => register_tick_function());
tick_case("excess unregister", fn() => unregister_tick_function("tick_print", true));
tick_case("unknown named", fn() => register_tick_function(callback: "tick_print", unknown: true));
tick_case("named", function (): mixed {
    $result = register_tick_function(callback: "tick_print");
    unregister_tick_function(callback: "tick_print");
    return $result;
});
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", first.String(), want)
	}
	if second.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", second.String(), want)
	}
}

func TestChdirPersistsVirtualCwdAndControlsIncludes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
$base = __DIR__;
if (getcwd() === $base) {
    echo "root\n";
    var_dump(chdir($base . "/lib"));
} else {
    echo "persisted\n";
}
var_dump(getcwd() === $base . "/lib");
include "choice.php";
`)
	mustWrite(t, filepath.Join(root, "choice.php"), `<?php echo "caller-choice\n";`)
	mustWrite(t, filepath.Join(root, "lib", "choice.php"), `<?php echo "cwd-choice\n";`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	}()
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	wantFirst, evalErr := engine.Eval(fmt.Sprintf(
		"chdir(%q); require %q;", root, filepath.Join(root, "main.php"),
	))
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if err := engine.Reset(); err != nil {
		t.Fatal(err)
	}
	wantSecond, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;", filepath.Join(root, "main.php"),
	))
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != wantFirst {
		t.Fatalf("first bundle output = %q, engine output = %q", first.String(), wantFirst)
	}
	if second.String() != wantSecond {
		t.Fatalf("second bundle output = %q, engine output = %q", second.String(), wantSecond)
	}
}

func TestIniRestoreUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function ini_restore_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
set_error_handler(function (int $level, string $message): true {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

$include_path = ini_get('include_path');
ini_set('include_path', 'changed:path');
ini_restore_case('include path return', fn() => ini_restore('include_path'));
var_dump(ini_get('include_path'));

gc_disable();
var_dump(gc_enabled(), ini_get('zend.enable_gc'));
ini_restore_case('gc return', fn() => ini_restore('zend.enable_gc'));
var_dump(gc_enabled(), ini_get('zend.enable_gc'));

ini_set('fiber.stack_size', '1M');
var_dump(ini_get('fiber.stack_size'));
ini_restore_case('fiber return', fn() => ini_restore('fiber.stack_size'));
var_dump(ini_get('fiber.stack_size'));
$fiber = new Fiber(function (): void {});
$fiber->start();
var_dump($fiber->isTerminated());

ini_set('zend.exception_ignore_args', '1');
var_dump(ini_get('zend.exception_ignore_args'));
ini_restore_case(
    'exception args return',
    fn() => ini_restore('zend.exception_ignore_args')
);
var_dump(ini_get('zend.exception_ignore_args'));

$default_reporting = error_reporting();
error_reporting(0);
var_dump(error_reporting());
ini_restore_case('error reporting return', fn() => ini_restore('error_reporting'));
var_dump(error_reporting(), error_reporting() === $default_reporting);

ini_restore_case('unknown', fn() => ini_restore('php2go.unknown'));
ini_restore_case('case variant', fn() => ini_restore('ZEND.ENABLE_GC'));
foreach ([null, false, true, 12, 1.5, [], new stdClass()] as $value) {
    ini_restore_case(
        'value ' . get_debug_type($value),
        fn() => ini_restore($value)
    );
}
ini_restore_case('named', fn() => ini_restore(option: 'zend.enable_gc'));
ini_restore_case('missing', fn() => ini_restore());
ini_restore_case('excess', fn() => ini_restore('zend.enable_gc', true));
ini_set('include_path', $include_path);
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestAssertOptionsUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function assertion_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
function assertion_configure(
    mixed $active = 1,
    mixed $warning = 1,
    mixed $bail = 0,
    mixed $exception = 1,
    mixed $callback = null
): void {
    @assert_options(1, $active);
    @assert_options(4, $warning);
    @assert_options(3, $bail);
    @assert_options(5, $exception);
    @assert_options(2, $callback);
}
function assertion_evaluated(): false {
    echo "evaluated\n";
    return false;
}
function assertion_callback($file, $line, $code, $description): void {
    var_dump(basename($file), $line > 0, $code, $description);
}

foreach ([1, 2, 3, 4, 5] as $option) {
    var_dump(@assert_options($option));
}
foreach ([null, false, true, 0, 1, '', '0', '1', 'off', 'on', 'false', 'true', []] as $value) {
    var_dump(@assert_options(1, $value), @assert_options(1));
}
var_dump(@assert_options(2, ['assertion_callback']), @assert_options(2));
@assert_options(2, null);

set_error_handler(function (int $level, string $message): true {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});
assertion_case('deprecated valid', fn() => assert_options(1));
assertion_case('deprecated invalid option', fn() => assert_options(1000));
assertion_case('deprecated missing', fn() => assert_options());
assertion_case('deprecated excess', fn() => assert_options(1, 2, 3));
restore_error_handler();

assertion_configure(active: 0);
assertion_case('inactive', fn() => assert(assertion_evaluated(), 'inactive-message'));
assertion_configure();
assertion_case('exception custom', fn() => assert(false, 'custom-message'));
assertion_case('exception default', fn() => assert(false));
$throwable = new RuntimeException('object-message');
assertion_case('throwable description', fn() => assert(false, $throwable));

assertion_configure(warning: 1, exception: 0);
set_error_handler(function (int $level, string $message): true {
    echo 'assert-diagnostic:', $level, ':', $message, "\n";
    return true;
});
assertion_case('warning false', fn() => assert(false, 'warn-message'));
restore_error_handler();

assertion_configure(warning: 0, exception: 0, callback: 'assertion_callback');
assertion_case('callback silent', fn() => assert(false, 'callback-message'));
assertion_configure(warning: 1, exception: 1, callback: 'assertion_callback');
assertion_case('callback exception', fn() => assert(false, 'callback-exception'));
assertion_configure(warning: 1, exception: 0, callback: 'missing_callback');
set_error_handler(function (int $level, string $message): true {
    echo 'invalid-diagnostic:', $level, ':', $message, "\n";
    return true;
});
assertion_case('invalid callback', fn() => assert(false, 'invalid-callback'));

restore_error_handler();
assertion_configure(warning: 0, bail: 1, exception: 0);
echo "before bail\n";
assert(false, 'bail-message');
echo "after bail\n";
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestUploadRegistryAbsenceUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
$source = __FILE__;
$destination = __DIR__ . "/moved.php";
$before = file_get_contents($source);
var_dump(
    is_uploaded_file($source),
    move_uploaded_file($source, $destination),
    $before === file_get_contents($source)
);

foreach (["", ".", "missing", "data:text/plain,x", "bad\0path"] as $index => $filename) {
    echo "is-path:", $index, ":";
    try {
        var_dump(is_uploaded_file($filename));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}

foreach ([
    [$source, $destination],
    ["missing", $destination],
    ["bad\0from", $destination],
    [$source, "bad\0to"],
] as $index => $arguments) {
    echo "move-path:", $index, ":";
    try {
        var_dump(move_uploaded_file($arguments[0], $arguments[1]));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}

set_error_handler(function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});
foreach ([null, false, true, 0, 1, 1.5, "x", [], new stdClass] as $index => $filename) {
    echo "is-arg:", $index, ":";
    try {
        var_dump(is_uploaded_file($filename));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}
foreach ([
    [null, null],
    [false, true],
    [0, 1],
    [1.5, "x"],
    ["x", 1.5],
    [[], "x"],
    ["x", []],
    [new stdClass, "x"],
] as $index => $arguments) {
    echo "move-arg:", $index, ":";
    try {
        var_dump(move_uploaded_file($arguments[0], $arguments[1]));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestWrapperAndRealpathCacheStateUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
var_dump(http_get_last_response_headers());
$http_response_header = ["user"];
var_dump(
    http_get_last_response_headers(),
    $http_response_header,
    http_clear_last_response_headers(),
    http_get_last_response_headers(),
    $http_response_header
);

var_dump(
    clearstatcache(),
    clearstatcache(clear_realpath_cache: true),
    clearstatcache(clear_realpath_cache: false, filename: "x")
);

function dump_realpath_cache(string $label): void {
    $cache = realpath_cache_get();
    ksort($cache);
    echo $label, ":size=", realpath_cache_size(), ";count=", count($cache), "\n";
    foreach ($cache as $path => $entry) {
        echo $path, "|", sprintf("%u", $entry["key"]),
            "|", (int) $entry["is_dir"], "|", $entry["realpath"],
            "|", ($entry["expires"] > 0 ? "future" : "none"), "\n";
    }
}

clearstatcache(true);
var_dump(
    ini_get("realpath_cache_size"),
    ini_get("realpath_cache_ttl"),
    ini_set("realpath_cache_size", "1"),
    ini_set("realpath_cache_ttl", "1"),
    realpath_cache_size(),
    realpath_cache_get(),
    realpath("/tmp/../tmp")
);
dump_realpath_cache("populated");
clearstatcache(false);
dump_realpath_cache("stat-only-clear");
clearstatcache(true, "/tmp/../tmp");
dump_realpath_cache("spelled-clear");
clearstatcache(true, "/tmp");
dump_realpath_cache("canonical-clear");
clearstatcache(true);

foreach (["", "/tmp/missing/php2go", "bad\0path"] as $path) {
    try {
        var_dump(realpath($path));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}

set_error_handler(function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});
foreach ([
    [null, null],
    [false, false],
    [true, 1],
    [0, 1.5],
    [1, "x"],
    ["0", []],
    [[], "x"],
    [new stdClass, "x"],
] as $index => $arguments) {
    echo "case:", $index, ":";
    try {
        var_dump(clearstatcache($arguments[0], $arguments[1]));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestRequestParseBodyUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function body_case(string $label, callable $probe): void {
    echo $label, ":";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});

body_case("default", fn() => request_parse_body());
body_case("null", fn() => request_parse_body(null));
body_case("empty", fn() => request_parse_body([]));
body_case("valid", fn() => request_parse_body([
    "POST_MAX_SIZE" => "2K",
    "max_input_vars" => 3,
    "upload_max_filesize" => -1,
    "max_file_uploads" => 0,
    "max_multipart_body_parts" => 1,
]));

body_case("bad-key", fn() => request_parse_body(["foo" => 1]));
body_case("empty-key", fn() => request_parse_body(["" => 1]));
body_case("integer-key", fn() => request_parse_body([42 => 1]));

foreach ([null, true, false, 1.5, [], new stdClass, fn() => 1] as $value) {
    body_case(
        "bad-value-" . get_debug_type($value),
        fn() => request_parse_body(["post_max_size" => $value])
    );
}
$stream = fopen("data:text/plain,x", "r");
body_case(
    "bad-value-resource",
    fn() => request_parse_body(["post_max_size" => $stream])
);
fclose($stream);

foreach (["1foo", "0x", "9223372036854775808"] as $quantity) {
    body_case(
        "quantity-" . $quantity,
        fn() => request_parse_body(["post_max_size" => $quantity])
    );
}

$quantity = "2M";
$options = ["post_max_size" => &$quantity];
body_case("referenced-value", fn() => request_parse_body($options));
var_dump($quantity, $options);

body_case("type", fn() => request_parse_body(new stdClass));
body_case("extra", fn() => request_parse_body([], []));
body_case("unknown-named", fn() => request_parse_body(extra: []));

try {
    request_parse_body(options: []);
} catch (RequestParseBodyException $error) {
    var_dump(
        get_class($error),
        $error instanceof Exception,
        $error->getMessage()
    );
}
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", first.String(), want)
	}
	if second.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", second.String(), want)
	}
}

func TestAbsentBrowscapUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
var_dump(ini_set("browscap", __FILE__));
set_error_handler(function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});

$cases = [
    [],
    [null],
    [false],
    [true],
    [0],
    [1],
    [1.5],
    ["x"],
    [""],
    [[]],
    [new stdClass],
    ["x", null],
    ["x", false],
    ["x", true],
    ["x", 0],
    ["x", 1],
    ["x", 1.5],
    ["x", "0"],
    ["x", "x"],
    ["x", []],
    ["x", new stdClass],
    ["x", false, true],
];
foreach ($cases as $index => $arguments) {
    echo "case:", $index, ":";
    try {
        var_dump(get_browser(...$arguments));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}

echo "named:";
var_dump(get_browser(return_array: true, user_agent: "named"));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestBuildMetadataUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
var_dump(PHP_VERSION, phpversion(), phpversion(extension: null));

$ini = ini_get_all(null, false);
var_dump(count($ini), $ini["arg_separator.output"], $ini["fiber.stack_size"]);
var_dump(ini_get_all("json", false));
$standard = ini_get_all("standard", true);
var_dump(count($standard), $standard["default_socket_timeout"]);
ini_set("url_rewriter.tags", "a=href");
var_dump(ini_get_all("standard", true)["url_rewriter.tags"]);
ini_restore("url_rewriter.tags");

foreach ([
    "Core", "date", "hash", "json", "pcre", "random", "Reflection",
    "standard", "SPL", "core", "CORE", "Pcre", "spl", "reflection",
    "session", "filter", "zlib", "Zend OPcache", "",
] as $extension) {
    echo bin2hex($extension), ":";
    var_dump(phpversion(extension: $extension));
}

foreach ([
    "html_errors",
    "implicit_flush",
    "max_execution_time",
    "max_input_time",
    "output_buffering",
    "register_argc_argv",
    "HTML_ERRORS",
    "Html_Errors",
    "html_errors\0x",
    " html_errors",
    "html_errors ",
    "",
] as $option) {
    echo bin2hex($option), ":";
    var_dump(get_cfg_var(option: $option));
}
$startup = get_cfg_var("html_errors");
ini_set("html_errors", "1");
var_dump($startup, get_cfg_var("html_errors"));

set_error_handler(function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});
foreach ([null, false, true, 0, 1, 1.5, "Core", [], new stdClass] as $index => $value) {
    echo "version:", $index, ":";
    try {
        var_dump(phpversion($value));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}
foreach ([null, false, true, 0, 1, 1.5, "html_errors", [], new stdClass] as $index => $value) {
    echo "config:", $index, ":";
    try {
        var_dump(get_cfg_var($value));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestPhpcreditsUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function credits_case(string $label, callable $call): void {
    ob_start();
    try {
        $result = $call();
        $output = ob_get_clean();
        echo $label, ":";
        var_dump($result, bin2hex($output));
    } catch (Throwable $error) {
        ob_end_clean();
        echo $label, ":", $error::class, ":", $error->getMessage(), "\n";
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});

foreach ([
    ['none', 0],
    ['group', CREDITS_GROUP],
    ['general', CREDITS_GENERAL],
    ['sapi', CREDITS_SAPI],
    ['modules', CREDITS_MODULES],
    ['docs', CREDITS_DOCS],
    ['fullpage', CREDITS_FULLPAGE],
    ['qa', CREDITS_QA],
    ['web', 128],
    ['combined', CREDITS_GROUP | CREDITS_MODULES],
    ['all', CREDITS_ALL],
    ['negative', -1],
    ['wrapped', 4294967297],
] as [$label, $flags]) {
    credits_case($label, fn() => phpcredits(flags: $flags));
}
credits_case('default', fn() => phpcredits());

foreach ([null, false, true, 1.5, "1", "x", [], new stdClass()] as $index => $flags) {
    credits_case("coerce-" . $index, fn() => phpcredits($flags));
}
credits_case('too-many', fn() => phpcredits(1, 2));
credits_case('unknown-named', fn() => phpcredits(nope: 1));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestPhpinfoUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
putenv('PHP2GO_INFO_TEST=value<&"');
$_GET['info_key'] = 'query<&"';
ini_set('highlight.comment', '#123456');

function info_case(string $label, int $flags): void {
    ob_start();
    $result = phpinfo($flags);
    $report = ob_get_clean();
    echo $label, ':';
    var_dump([
        'return' => $result,
        'prefix' => str_starts_with($report, '<!DOCTYPE html PUBLIC'),
        'suffix' => str_ends_with($report, '</div></body></html>'),
        'general' => str_contains($report, '<h1 class="p">PHP Version '),
        'configuration' => str_contains($report, '<h1>Configuration</h1>'),
        'modules' => str_contains($report, 'name="module_core"'),
        'environment' => str_contains($report, '<h2>Environment</h2>'),
        'environment_value' => str_contains($report, 'value&lt;&amp;&quot;'),
        'variables' => str_contains($report, '<h2>PHP Variables</h2>'),
        'variable_name' => str_contains($report, 'info_key'),
        'variable_value' => str_contains($report, 'query&lt;&amp;&quot;'),
        'credits' => str_contains($report, '<h1>PHP Credits</h1>'),
        'license' => str_contains($report, '<h2>PHP License</h2>'),
        'ini_value' => str_contains($report, 'color: #123456'),
    ]);
    if ($flags === 0) {
        var_dump(bin2hex($report));
    }
}

foreach ([
    'none' => 0,
    'general' => INFO_GENERAL,
    'credits' => INFO_CREDITS,
    'configuration' => INFO_CONFIGURATION,
    'modules' => INFO_MODULES,
    'environment' => INFO_ENVIRONMENT,
    'variables' => INFO_VARIABLES,
    'license' => INFO_LICENSE,
    'all-known' => INFO_GENERAL | INFO_CREDITS | INFO_CONFIGURATION |
        INFO_MODULES | INFO_ENVIRONMENT | INFO_VARIABLES | INFO_LICENSE,
] as $label => $flags) {
    info_case($label, $flags);
}

foreach ([null, false, true, 1.5, '1'] as $index => $flags) {
    ob_start();
    try {
        $result = phpinfo($flags);
        ob_end_clean();
        echo 'coerce-', $index, ':';
        var_dump($result);
    } catch (Throwable $error) {
        ob_end_clean();
        echo 'coerce-', $index, ':', $error::class, ':', $error->getMessage(), "\n";
    }
}
foreach ([[], new stdClass()] as $index => $flags) {
    try {
        phpinfo($flags);
    } catch (Throwable $error) {
        echo 'invalid-', $index, ':', $error::class, ':', $error->getMessage(), "\n";
    }
}
try {
    phpinfo(1, 2);
} catch (Throwable $error) {
    echo 'too-many:', $error::class, ':', $error->getMessage(), "\n";
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestDebugZvalDumpNativeMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
echo "scalars\n";
debug_zval_dump(null, false, true, 42, 1.25, "literal");

$dynamic = substr("Xdynamic", 1);
echo "dynamic\n";
debug_zval_dump($dynamic);

echo "empty-literal\n";
debug_zval_dump([]);
$empty = array_filter([0]);
echo "empty-dynamic\n";
debug_zval_dump($empty);

$reference = 1;
$array = [];
$array[] =& $reference;
echo "reference-one\n";
debug_zval_dump($array);
$array[] =& $reference;
echo "reference-two\n";
debug_zval_dump($array);
unset($array[1]);
echo "reference-one-again\n";
debug_zval_dump($array);
unset($reference);
echo "reference-array-only\n";
debug_zval_dump($array);

$literal_reference = 2;
$literal_array = [&$literal_reference];
echo "reference-literal\n";
debug_zval_dump($literal_array);

$built = [];
$built[] = "inserted";
echo "built-array\n";
debug_zval_dump($built);

$object = new stdClass();
$object->property = "value";
echo "object\n";
debug_zval_dump($object);

$resource = fopen("data:text/plain,resource", "r");
ob_start();
$result = debug_zval_dump($resource);
$resource_dump = ob_get_clean();
echo "resource:";
var_dump(
    $result,
    str_starts_with($resource_dump, "resource("),
    str_contains($resource_dump, " of type (stream) refcount(2)")
);
fclose($resource);

echo "return\n";
var_dump(debug_zval_dump(7));
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestStrptimeLibcUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
class StrptimeStringable {
    public function __toString(): string { return "2006"; }
}

function strptime_case(string $label, callable $probe): void {
    echo $label, ":";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});

foreach ([
    ["date", "2006-08-20", "%Y-%m-%d"],
    ["complete", "08:10:20 PM August 20 2006 UTC", "%H:%M:%S %p %B %d %Y %Z"],
    ["partial", "Sunday August 20 20:10 tail", "%A %B %e %R"],
    ["invalid", "foo", "%Y"],
    ["empty", "", ""],
    ["empty-format", "abc", ""],
    ["timestamp-nul", "2001\0tail", "%Y"],
    ["format-nul", "2001", "%Y\0ignored"],
] as [$label, $timestamp, $format]) {
    strptime_case($label, fn() => strptime($timestamp, $format));
}

foreach ([null, false, true, 2006, 1.5, new StrptimeStringable(), [], new stdClass()] as $index => $timestamp) {
    strptime_case("timestamp-" . $index, fn() => strptime($timestamp, "%Y"));
}
foreach ([null, false, true, 2006, 1.5, [], new stdClass()] as $index => $format) {
    strptime_case("format-" . $index, fn() => strptime("2006", $format));
}

strptime_case("named", fn() => strptime(format: "%Y", timestamp: "2006"));
strptime_case("missing", fn() => strptime());
strptime_case("one", fn() => strptime("2006"));
strptime_case("excess", fn() => strptime("2006", "%Y", true));
restore_error_handler();

set_error_handler(static function (int $level, string $message): never {
    throw new Exception($message);
});
strptime_case("deprecation-first", fn() => strptime([], "%Y"));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestStrcollLibcMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
class CollationStringable {
    public function __construct(private string $value) {}
    public function __toString(): string {
        echo "cast:", bin2hex($this->value), "\n";
        return $this->value;
    }
}

function collation_case(string $label, callable $probe): void {
    echo $label, ":";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}

function collation_sign(string $left, string $right): array {
    $result = strcoll($left, $right);
    return [$result <=> 0, $result === 0];
}

set_error_handler(static function (int $level, string $message): true {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});
$high_left = 255;
$high_right = 128;
foreach ([
    ["ascending", "a", "b"],
    ["descending", "b", "a"],
    ["equal", "same", "same"],
    ["left-empty", "", "a"],
    ["right-empty", "a", ""],
    ["prefix-left", "abc", "abcd"],
    ["prefix-right", "abcd", "abc"],
    ["high-bytes", chr($high_left), chr($high_right)],
    ["nul-equal", "prefix\0left", "prefix\0right"],
] as [$label, $left, $right]) {
    collation_case($label, fn() => collation_sign($left, $right));
}

collation_case("stringable", fn() => collation_sign(
    new CollationStringable("a"),
    new CollationStringable("b")
));
collation_case("coercions", fn() => [
    strcoll(null, false) <=> 0,
    strcoll(true, 12) <=> 0,
    strcoll(1.5, "1.5") <=> 0,
]);
collation_case("named", fn() => strcoll(string2: "b", string1: "a") <=> 0);
collation_case("array", fn() => strcoll([], "a"));
collation_case("object", fn() => strcoll(new stdClass(), "a"));
collation_case("missing", fn() => strcoll());
collation_case("one", fn() => strcoll("a"));
collation_case("excess", fn() => strcoll("a", "b", true));
collation_case("unknown-named", fn() => strcoll(string1: "a", string2: "b", extra: true));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestClockAndSleepUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function clock_case(string $label, callable $probe): void {
    echo $label, ':';
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

$wall = gettimeofday();
$wall_float = gettimeofday(true);
$micro = microtime();
$micro_float = microtime(true);
$micro_parts = explode(' ', $micro);
clock_case('wall-shape', fn() => [
    array_keys($wall),
    array_map('get_debug_type', $wall),
    $wall['usec'] >= 0 && $wall['usec'] < 1000000,
    $wall['minuteswest'], $wall['dsttime'],
    $wall_float >= $wall['sec'] && $wall_float < $wall['sec'] + 2,
]);
clock_case('micro-shape', fn() => [
    strlen($micro_parts[0]),
    substr($micro_parts[0], 0, 2),
    strspn(substr($micro_parts[0], 2), '0123456789') === 8,
    strspn($micro_parts[1], '0123456789') === strlen($micro_parts[1]),
    $micro_float >= (float) $micro_parts[1] &&
        $micro_float < (float) $micro_parts[1] + 2,
]);

$hr_first = hrtime();
$hr_number = hrtime(true);
$hr_last = hrtime();
clock_case('hr-shape', fn() => [
    count($hr_first),
    get_debug_type($hr_first[0]), get_debug_type($hr_first[1]),
    get_debug_type($hr_number),
    $hr_first[1] >= 0 && $hr_first[1] < 1000000000,
    $hr_first[0] * 1000000000 + $hr_first[1] <= $hr_number,
    $hr_number <= $hr_last[0] * 1000000000 + $hr_last[1],
]);

$start = hrtime(true);
clock_case('nanosleep-result', fn() => time_nanosleep(0, 1000000));
clock_case('nanosleep-elapsed', fn() => hrtime(true) - $start >= 500000);
$start = hrtime(true);
clock_case('usleep-result', fn() => usleep(1000));
clock_case('usleep-elapsed', fn() => hrtime(true) - $start >= 500000);
clock_case('sleep-zero', fn() => sleep(0));
$start = hrtime(true);
clock_case(
    'sleep-until-result',
    fn() => time_sleep_until(microtime(true) + 0.002)
);
clock_case('sleep-until-elapsed', fn() => hrtime(true) - $start >= 1000000);

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});
foreach (['gettimeofday', 'microtime', 'hrtime'] as $function) {
    clock_case($function . '-null', fn() => get_debug_type($function(null)));
    clock_case($function . '-array', fn() => $function([]));
    clock_case($function . '-named', function () use ($function) {
        if ($function === 'hrtime') {
            return get_debug_type($function(as_number: true));
        }
        return get_debug_type($function(as_float: true));
    });
    clock_case($function . '-arity', fn() => $function(false, true));
}
clock_case('sleep-negative', fn() => sleep(-1));
clock_case('sleep-float', fn() => sleep(0.5));
clock_case('sleep-missing', fn() => sleep());
clock_case('sleep-named', fn() => sleep(seconds: 0));
clock_case('usleep-negative', fn() => usleep(-1));
clock_case('usleep-float', fn() => usleep(0.5));
clock_case('usleep-named', fn() => usleep(microseconds: 0));
foreach ([
    'negative-seconds' => [-1, 0],
    'negative-nanoseconds' => [0, -1],
    'large-nanoseconds' => [0, 1000000000],
] as $label => [$seconds, $nanoseconds]) {
    clock_case(
        'nanosleep-' . $label,
        fn() => time_nanosleep($seconds, $nanoseconds)
    );
}
clock_case(
    'nanosleep-named',
    fn() => time_nanosleep(nanoseconds: 0, seconds: 0)
);
clock_case('nanosleep-arity', fn() => time_nanosleep(0));
clock_case('until-past', fn() => time_sleep_until(0));
clock_case('until-nan', fn() => time_sleep_until(NAN));
clock_case('until-negative', fn() => time_sleep_until(-1));
clock_case(
    'until-named',
    fn() => time_sleep_until(timestamp: microtime(true) + 0.001)
);
clock_case('until-arity', fn() => time_sleep_until());
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestEmbedSapiStateAndIdentityUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
var_dump(
    php_ini_loaded_file(),
    php_ini_scanned_files(),
    connection_status(),
    connection_aborted()
);

function identity_snapshot(): array {
    return [
        get_current_user(), getlastmod(), getmygid(),
        getmyinode(), getmypid(), getmyuid(),
    ];
}
var_dump(identity_snapshot(), identity_snapshot());
foreach ([
    'get_current_user', 'getlastmod', 'getmygid',
    'getmyinode', 'getmypid', 'getmyuid',
] as $function) {
    echo 'identity-arity:', $function, ':';
    try {
        $function(true);
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
    echo 'identity-named:', $function, ':';
    try {
        $function(value: true);
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

ignore_user_abort(enable: false);
var_dump(ignore_user_abort(), connection_status(), connection_aborted());
foreach ([true, false, true, null, 0, 1, "0", "1", 0.0, 1.5, [], new stdClass] as $index => $enable) {
    echo "case:", $index, ":";
    try {
        var_dump(
            ignore_user_abort($enable),
            ignore_user_abort(),
            connection_status(),
            connection_aborted()
        );
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
        var_dump(ignore_user_abort());
    }
}
ignore_user_abort(false);
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", first.String(), want)
	}
	if second.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", second.String(), want)
	}
}

func TestSetlocalePersistsAcrossBundleRequests(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
$before = setlocale(LC_TIME, 0);
if ($before === "C") {
    echo "first\n";
    var_dump(setlocale(LC_TIME, ["C.UTF-8", "C.utf8"]));
} else {
    echo "persisted\n";
}
var_dump(setlocale(LC_TIME, 0), nl_langinfo(DAY_1));
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	original, evalErr := engine.Eval("echo setlocale(LC_ALL, 0);")
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	restore := func() {
		if err := engine.Reset(); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Eval(fmt.Sprintf("setlocale(LC_ALL, %q);", original)); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.Reset(); err != nil {
		t.Fatal(err)
	}
	wantFirst, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;", filepath.Join(root, "main.php"),
	))
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if err := engine.Reset(); err != nil {
		t.Fatal(err)
	}
	wantSecond, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;", filepath.Join(root, "main.php"),
	))
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	restore()

	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}
	restore()
	if first.String() != wantFirst {
		t.Fatalf("first bundle output = %q, engine output = %q", first.String(), wantFirst)
	}
	if second.String() != wantSecond {
		t.Fatalf("second bundle output = %q, engine output = %q", second.String(), wantSecond)
	}
}

func TestLocaleInformationNativeMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
var_dump(localeconv());

$items = [14, 65536, 65537];
foreach ([[131072, 131121], [262144, 262159], [327680, 327683]] as [$first, $last]) {
    for ($item = $first; $item <= $last; $item++) {
        $items[] = $item;
    }
}
foreach ($items as $item) {
    $value = nl_langinfo(item: $item);
    echo $item, ":", bin2hex($value), "\n";
}
echo "named:", bin2hex(nl_langinfo(item: CODESET)), "\n";

set_error_handler(function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});
foreach ([null, false, true, 1.5, "14", "14x", [], new stdClass] as $index => $item) {
    echo "case:", $index, ":";
    try {
        $value = nl_langinfo($item);
        echo $value === false ? "false" : bin2hex($value), "\n";
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestInetConversionUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function show_pton(string $ip): void {
    try {
        $packed = inet_pton(ip: $ip);
        echo bin2hex($ip), ":", $packed === false ? "false" : bin2hex($packed), "\n";
    } catch (Throwable $error) {
        echo bin2hex($ip), ":", get_class($error), ":", $error->getMessage(), "\n";
    }
}

foreach (["0.0.0.0", "127.0.0.1", "255.255.255.255", "01.2.3.4", "1.2.3",
          "1.2.3.256", "1.2.+3.4", "::", "::1", "2001:db8::1",
          "::ffff:192.0.2.1", "fe80::1%lo", " 1.2.3.4", "1.2.3.4 ", "a\0b"] as $ip) {
    show_pton($ip);
}
for ($i = 0; $i < 1024; $i++) {
    $a = ($i * 73) % 256;
    $b = ($i * 151 + 1) % 256;
    $c = ($i * 199 + 2) % 256;
    $d = ($i * 251 + 3) % 256;
    $ip = $a . "." . $b . "." . $c . "." . $d;
    switch ($i % 5) {
        case 1: $ip = "0" . $ip; break;
        case 2: $ip .= ".1"; break;
        case 3: $ip = substr($ip, 0, strrpos($ip, ".")); break;
        case 4: $ip = "+" . $ip; break;
    }
    show_pton($ip);
}

foreach (["", "000000", "00000000", "7f000001", "ffffffff",
          str_repeat("00", 16), str_repeat("00", 17)] as $hex) {
    $packed = hex2bin($hex);
    $text = inet_ntop(ip: $packed);
    echo bin2hex($packed), ":", $text === false ? "false" : $text, "\n";
}
for ($i = 0; $i < 1024; $i++) {
    $packed = chr(($i * 67) % 256) . chr(($i * 109 + 1) % 256) .
        chr(($i * 173 + 2) % 256) . chr(($i * 229 + 3) % 256);
    echo bin2hex($packed), ":", inet_ntop($packed), "\n";
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output differs from engine")
	}
}

func TestArrayRandomizationUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function random_case(string $label, callable $callback): void {
    echo $label, ':';
    try {
        var_dump($callback());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

function seeded_randomization(int $seed): void {
    echo 'seed:', $seed, "\n";
    $list = ['a', 'b', 'c', 'd', 'e', 'f'];
    $map = [7 => 'a', 'x' => 'b', -2 => 'c', 11 => 'd', 'y' => 'e'];

    mt_srand($seed);
    var_dump(
        array_rand($list),
        array_rand($list, 3),
        array_rand($map),
        array_rand($map, 4),
        mt_rand()
    );

    mt_srand($seed);
    $shuffled = $map;
    var_dump(shuffle($shuffled), $shuffled, mt_rand());

    mt_srand($seed);
    $empty = [];
    var_dump(shuffle($empty), $empty, mt_rand());

    mt_srand($seed);
    var_dump(array_rand($list, count($list)), mt_rand());
}

set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});
foreach ([1234, 0, -1] as $seed) {
    seeded_randomization($seed);
}

mt_srand(1234, 1);
$legacy = [10 => 'a', 20 => 'b', 30 => 'c', 40 => 'd'];
var_dump(array_rand($legacy, 2), shuffle($legacy), $legacy, mt_rand());

$holes = range(0, 9);
unset($holes[1], $holes[4], $holes[8], $holes[9]);
mt_srand(7);
var_dump(array_rand($holes), array_rand($holes, 4), mt_rand());

$shared = 'shared';
$references = [&$shared, 'plain', &$shared, 'tail'];
mt_srand(1234);
shuffle($references);
$references[2] = 'changed';
var_dump($shared, $references);

$original = ['a' => 1, 9 => 2, 'b' => 3];
$copy = $original;
$alias =& $original;
mt_srand(1234);
var_dump(shuffle($original), $original, $alias, $copy);
reset($original);
var_dump(key($original), current($original));

$named = ['a', 'b', 'c'];
mt_srand(9);
var_dump(
    array_rand(array: [4 => 'x', 'k' => 'y'], num: 2),
    shuffle(array: $named),
    $named,
    mt_rand()
);

random_case('array-rand-empty', fn() => array_rand([]));
random_case('array-rand-empty-zero', fn() => array_rand([], 0));
random_case('array-rand-zero', fn() => array_rand([1, 2, 3], 0));
random_case('array-rand-negative', fn() => array_rand([1, 2, 3], -1));
random_case('array-rand-excess', fn() => array_rand([1, 2, 3], 4));
random_case('array-rand-type', fn() => array_rand('not-array'));
random_case('array-rand-num-type', fn() => array_rand([1], []));
random_case('array-rand-missing', fn() => array_rand());
random_case('array-rand-arity', fn() => array_rand([1], 1, true));
$not_array = null;
$too_many = null;
random_case('shuffle-literal', fn() => shuffle([]));
random_case('shuffle-type', fn() => shuffle($not_array = 'x'));
random_case('shuffle-missing', fn() => shuffle());
random_case('shuffle-arity', fn() => shuffle($too_many = [], true));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestArrayMultisortUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function am_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

$primary = ['row1' => 2, 'row2' => 1, 'row3' => 1];
$secondary = ['row1' => 2, 'row2' => 'aa', 'row3' => '1'];
am_case('multiple columns', function () use (&$primary, &$secondary) {
    $result = array_multisort(
        $primary,
        SORT_ASC,
        SORT_REGULAR,
        $secondary,
        SORT_DESC,
        SORT_STRING
    );
    return [$result, $primary, $secondary];
});

$keyed = ['z' => 2, 5 => 1, 'a' => 1, 9 => 3];
$paired = [10 => 'b', 's' => 'c', 20 => 'a', 't' => 'd'];
am_case('mixed keys', function () use (&$keyed, &$paired) {
    $result = array_multisort(
        $keyed,
        SORT_ASC,
        SORT_NUMERIC,
        $paired,
        SORT_DESC,
        SORT_STRING
    );
    return [$result, $keyed, $paired];
});

foreach ([
    'regular' => SORT_REGULAR,
    'regular case' => SORT_REGULAR | SORT_FLAG_CASE,
    'numeric' => SORT_NUMERIC,
    'numeric case' => SORT_NUMERIC | SORT_FLAG_CASE,
    'string' => SORT_STRING,
    'string case' => SORT_STRING | SORT_FLAG_CASE,
    'locale' => SORT_LOCALE_STRING,
    'locale case' => SORT_LOCALE_STRING | SORT_FLAG_CASE,
    'natural' => SORT_NATURAL,
    'natural case' => SORT_NATURAL | SORT_FLAG_CASE,
] as $label => $flag) {
    am_case($label, function () use ($flag) {
        $values = ['a2', 'A10', 'a10', 'A2', 2, '02', null, false, true];
        $result = array_multisort($values, $flag);
        return [$result, $values];
    });
}

am_case('descending and three columns', function () {
    $first = [4, 3, 3, 3];
    $second = [9, 3, 2, 2];
    $third = [9, 9, 2, 1];
    $result = array_multisort(
        $first,
        SORT_DESC,
        $second,
        SORT_ASC,
        $third,
        SORT_DESC
    );
    return [$result, $first, $second, $third];
});

am_case('stable', function () {
    $first = array_fill(0, 40, 0);
    $second = [];
    for ($index = 0; $index < 40; $index++) {
        $second[] = ($index & 1) === 0 ? 0 : '0';
    }
    $original = $second;
    $result = array_multisort($first, $second);
    return [$result, $second === $original];
});

am_case('large numeric precision', function () {
    $values = [
        PHP_INT_MAX,
        PHP_INT_MAX - 1,
        (string) PHP_INT_MAX,
        (string) (PHP_INT_MAX - 1),
    ];
    $result = array_multisort($values, SORT_NUMERIC);
    return [$result, $values];
});

am_case('temporary arrays', function () {
    $first = [2, 1];
    $result = array_multisort($first, ['b', 'a']);
    return [$result, $first, array_multisort([2, 1])];
});

$alias = [2, 1];
$same =& $alias;
am_case('aliased columns', function () use (&$alias, &$same) {
    $result = array_multisort(
        $alias,
        SORT_ASC,
        $same,
        SORT_DESC
    );
    return [$result, $alias, $same];
});

$externalFirst = 3;
$externalSecond = 1;
$references = [
    'named' => &$externalFirst,
    9 => &$externalSecond,
];
am_case(
    'element references',
    function () use (
        &$references,
        &$externalFirst,
        &$externalSecond
    ) {
        $result = array_multisort($references);
        $references[0] = 10;
        $references['named'] = 20;
        return [
            $result,
            $references,
            $externalFirst,
            $externalSecond,
        ];
    }
);

foreach ([
    'first scalar' => function () {
        $value = 1;
        return array_multisort($value);
    },
    'first invalid flag' => function () {
        $value = 7;
        return array_multisort($value);
    },
    'rest scalar' => function () {
        $array = [1];
        $value = '4';
        return array_multisort($array, $value);
    },
    'rest invalid flag' => function () {
        $array = [1];
        $value = 7;
        return array_multisort($array, $value);
    },
    'repeat order' => function () {
        $array = [1];
        return array_multisort($array, SORT_ASC, SORT_DESC);
    },
    'repeat type' => function () {
        $array = [1];
        return array_multisort($array, SORT_STRING, SORT_NUMERIC);
    },
    'inconsistent sizes' => function () {
        $first = [1, 2];
        $second = [1];
        return array_multisort($first, $second);
    },
] as $label => $probe) {
    am_case($label, $probe);
}

class AMNoString {}
$objects = [new AMNoString(), new AMNoString()];
$before = $objects;
am_case('comparison exception', function () use (&$objects, $before) {
    try {
        array_multisort($objects, SORT_STRING);
    } catch (Throwable $error) {
        return [
            get_class($error),
            $error->getMessage(),
            $objects === $before,
        ];
    }
});
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}

func TestArrayWalkRecursiveUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function walk_emit(mixed $value): void {
    echo serialize($value), "\n";
}

$input = ["x" => 1, "nested" => ["y" => 2, 3], "object" => (object) ["p" => 4]];
$log = [];
$result = array_walk_recursive($input, function (&$value, $key, ...$rest) use (&$log) {
    $log[] = [$key, $value, count($rest)];
    if (is_int($value)) {
        $value *= 10;
    }
}, "userdata");
walk_emit([$result, $input, $log]);

$input = [1, [2], 3];
$log = [];
array_walk_recursive($input, function (&$value, $key) use (&$input, &$log) {
    $log[] = $key;
    if ($key === 0) {
        unset($input[2]);
        $input[9] = 9;
        $input[1] = "changed";
    }
    $value = "v" . $value;
});
walk_emit([$input, $log]);

$shared = 1;
$input = [&$shared, ["b" => &$shared]];
array_walk_recursive($input, function (&$value) { $value++; });
walk_emit([$input, $shared]);

class WalkBox {
    private int $private = 1;
    protected int $protected = 2;
    public array $public = ["deep" => 3];
}
$object = new WalkBox();
$log = [];
array_walk_recursive($object, function (&$value, $key) use (&$log) {
    $log[] = [$key, $value];
    $value += 10;
});
walk_emit([$object, $log]);

$input = [];
$input["first"] = 1;
$input["self"] =& $input;
$log = [];
try {
    array_walk_recursive($input, function ($value, $key) use (&$log) { $log[] = $key; });
} catch (Throwable $error) {
    walk_emit([get_class($error), $error->getMessage(), $log]);
}

$nested = ["value" => 1];
$input = ["left" => &$nested, "right" => &$nested];
array_walk_recursive($input, function (&$value) { $value++; });
walk_emit([$input, $nested]);

foreach ([false, true] as $withArgument) {
    $input = [1];
    $seen = null;
    $callback = function () use (&$seen) { $seen = func_num_args(); };
    $result = $withArgument
        ? array_walk_recursive($input, $callback, null)
        : array_walk_recursive($input, $callback);
    walk_emit([$withArgument, $result, $seen]);
}

$input = [1, [2, 3], 4];
$userdata = 10;
$warnings = [];
set_error_handler(function ($level, $message) use (&$warnings) {
    $warnings[] = [$level, $message];
    return true;
});
array_walk_recursive($input, function ($value, $key, &$copy) use (&$warnings) {
    $warnings[] = $copy;
    $copy++;
}, $userdata);
restore_error_handler();
walk_emit([$userdata, $warnings]);

$input = [1, 2];
$log = [];
try {
    array_walk_recursive($input, function (&$value, $key) use (&$input, &$log) {
        $log[] = $key;
        $input = 42;
    });
} catch (Throwable $error) {
    $replacementError = [get_class($error), $error->getMessage()];
}
walk_emit([$input, $log, $replacementError ?? null]);

$input = [1, [2, 3], 4];
try {
    array_walk_recursive($input, function (&$value) {
        $value *= 10;
        if ($value === 30) {
            throw new Exception("stop");
        }
    });
} catch (Throwable $error) {
    walk_emit([get_class($error), $error->getMessage(), $input]);
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestUserComparatorArrayFamilyMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function comparator_emit(mixed $value): void {
    echo serialize($value), "\n";
}

$a = ["A" => "x", "b" => "X", 3 => "3", "04" => "4", "dup" => "x"];
$b = ["a" => "x", "B" => "y", 3 => 3, 4 => "4", "z" => "x"];
$run = function (string $kind) use ($a, $b): void {
    $log = [];
    $values = function ($first, $second) use (&$log): int {
        $log[] = ["v", $first, $second];
        return strcasecmp((string) $first, (string) $second);
    };
    $keys = function ($first, $second) use (&$log): int {
        $log[] = ["k", $first, $second];
        return strcasecmp((string) $first, (string) $second);
    };
    $result = match ($kind) {
        "diff_uassoc" => array_diff_uassoc($a, $b, $keys),
        "diff_ukey" => array_diff_ukey($a, $b, $keys),
        "intersect_uassoc" => array_intersect_uassoc($a, $b, $keys),
        "intersect_ukey" => array_intersect_ukey($a, $b, $keys),
        "udiff" => array_udiff($a, $b, $values),
        "udiff_assoc" => array_udiff_assoc($a, $b, $values),
        "udiff_uassoc" => array_udiff_uassoc($a, $b, $values, $keys),
        "uintersect" => array_uintersect($a, $b, $values),
        "uintersect_assoc" => array_uintersect_assoc($a, $b, $values),
        "uintersect_uassoc" => array_uintersect_uassoc($a, $b, $values, $keys),
    };
    comparator_emit([$kind, $result, $log]);
};
foreach ([
    "diff_uassoc", "diff_ukey", "intersect_uassoc", "intersect_ukey",
    "udiff", "udiff_assoc", "udiff_uassoc", "uintersect",
    "uintersect_assoc", "uintersect_uassoc",
] as $kind) {
    $run($kind);
}

$large = [17, 3, 15, 1, 13, 5, 11, 7, 9, 2, 16, 4, 14, 6, 12, 8, 10];
$log = [];
$result = array_udiff($large, [10], function ($first, $second) use (&$log): int {
    $log[] = "$first:$second";
    return $first <=> $second;
});
comparator_emit([$result, $log]);

$large = [];
for ($index = 0; $index < 1025; $index++) {
    $large[] = ($index * 613) % 1025;
}
$calls = 0;
$trace = 0;
$result = array_udiff($large, [], function ($first, $second) use (&$calls, &$trace): int {
    $calls++;
    $trace = ($trace * 131 + (($first + 1) * 17 + $second + 1)) % 1000000007;
    return $first <=> $second;
});
comparator_emit([count($result), $result[0], $result[1024], $calls, $trace]);

$compare = static fn($first, $second): int => $first <=> $second;
comparator_emit([
    array_udiff([3, 1, 2], $compare),
    array_uintersect([1, 2, 2, 3], [2, 3], [2], $compare),
    array_udiff([1, 2, 3, 4], [2], [4], $compare),
]);

for ($seed = 0; $seed < 32; $seed++) {
    $generated = [[], [], []];
    for ($which = 0; $which < 3; $which++) {
        $length = ($seed * 7 + $which * 5) % 14;
        for ($index = 0; $index < $length; $index++) {
            $key_number = ($index * 5 + $seed + $which) % 11;
            $key = (($index + $seed + $which) % 3 === 0)
                ? "k" . $key_number
                : $key_number;
            $selector = ($index * 3 + $seed + $which) % 4;
            $value = match ($selector) {
                0 => ($index + $seed) % 7,
                1 => (string) (($index * 2 + $which) % 7),
                2 => chr(65 + (($index + $seed + $which) % 5)),
                default => chr(97 + (($index * 2 + $seed) % 5)),
            };
            $generated[$which][$key] = $value;
        }
    }
    [$first, $second, $third] = $generated;
    $values = static fn($left, $right): int =>
        strcasecmp((string) $left, (string) $right);
    $keys = static fn($left, $right): int =>
        strcasecmp((string) $left, (string) $right);
    comparator_emit([
        array_diff_uassoc($first, $second, $third, $keys),
        array_diff_ukey($first, $second, $third, $keys),
        array_intersect_uassoc($first, $second, $third, $keys),
        array_intersect_ukey($first, $second, $third, $keys),
        array_udiff($first, $second, $third, $values),
        array_udiff_assoc($first, $second, $third, $values),
        array_udiff_uassoc($first, $second, $third, $values, $keys),
        array_uintersect($first, $second, $third, $values),
        array_uintersect_assoc($first, $second, $third, $values),
        array_uintersect_uassoc($first, $second, $third, $values, $keys),
    ]);
}

$warnings = [];
set_error_handler(function ($level, $message) use (&$warnings): bool {
    $warnings[] = [$level, $message];
    return true;
});
$bool_diff = array_udiff([3, 1, 2], [2], static fn($first, $second): bool => $first > $second);
$bool_direct = array_udiff_assoc(
    ["a" => 1, "b" => 2],
    ["a" => 1],
    static fn($first, $second): bool => $first !== $second
);
restore_error_handler();
comparator_emit([$bool_diff, $bool_direct, $warnings]);

$errors = [];
$cases = [
    static fn() => array_udiff(),
    static fn() => array_udiff([], 42),
    static fn() => array_udiff([], [], "missing_callback"),
    static fn() => array_udiff([], 42, static fn() => 0),
    static fn() => array_udiff_uassoc([], static fn() => 0),
    static fn() => array_udiff_uassoc([], [], static fn() => 0),
];
foreach ($cases as $case) {
    try {
        $case();
        $errors[] = null;
    } catch (Throwable $error) {
        $errors[] = [get_class($error), $error->getMessage()];
    }
}
comparator_emit($errors);

$shared = 1;
$with_reference = ["keep" => &$shared, "drop" => 2];
$reference_result = array_udiff($with_reference, [2], $compare);
$shared = 9;
comparator_emit([$reference_result, $shared]);

$warnings = [];
set_error_handler(function ($level, $message) use (&$warnings): bool {
    $warnings[] = [$level, $message];
    return true;
});
array_udiff([2, 1], [1], function (&$first, &$second): int {
    return $first <=> $second;
});
restore_error_handler();
comparator_emit($warnings);

$log = [];
try {
    array_udiff([4, 3, 2, 1], [2], function ($first, $second) use (&$log): int {
        $log[] = [$first, $second];
        if (count($log) === 3) {
            throw new Exception("stop");
        }
        return $first <=> $second;
    });
} catch (Throwable $error) {
    comparator_emit([get_class($error), $error->getMessage(), $log]);
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestFileContentUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "content.bin"), "a\n\nB\r\nC\rD\x00E")
	mustWrite(t, filepath.Join(root, "empty.txt"), "")
	mustWrite(t, filepath.Join(root, "included", "item.txt"), "included\n")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function file_content_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

function file_content_hex_lines(array|false $lines): array|false {
    return $lines === false ? false : array_map('bin2hex', $lines);
}

$content = __DIR__ . '/content.bin';
foreach ([
    0,
    FILE_IGNORE_NEW_LINES,
    FILE_SKIP_EMPTY_LINES,
    FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES,
] as $flags) {
    file_content_case(
        'file flags ' . $flags,
        fn() => file_content_hex_lines(file($content, $flags))
    );
}
file_content_case(
    'empty file',
    fn() => file(__DIR__ . '/empty.txt')
);
file_content_case(
    'hashes',
    fn() => [
        md5_file($content),
        bin2hex(md5_file($content, true)),
        sha1_file($content),
        bin2hex(sha1_file($content, true)),
    ]
);
file_content_case('readfile', function () use ($content) {
    ob_start();
    $count = readfile($content);
    $bytes = ob_get_clean();
    return [bin2hex($bytes), $count];
});

$old_include_path = set_include_path(__DIR__ . '/included');
file_content_case(
    'file include path',
    fn() => file_content_hex_lines(
        file('item.txt', FILE_USE_INCLUDE_PATH)
    )
);
file_content_case('readfile include path', function () {
    ob_start();
    $count = readfile('item.txt', true);
    $bytes = ob_get_clean();
    return [bin2hex($bytes), $count];
});
set_include_path($old_include_path);

file_content_case(
    'data wrapper',
    fn() => [
        file_content_hex_lines(file('data:text/plain,a%0A%0Ab')),
        md5_file('data:text/plain,a%00b'),
        file_content_hex_lines(file('data:text/plain,a+b')),
        file_content_hex_lines(file('data:text/plain,a%2Bb')),
        md5_file('data:text/plain;base64,YQ=='),
        sha1_file('data:text/plain,a%00b'),
    ]
);

$handle = fopen('data:text/plain,context', 'r');
file_content_case(
    'file invalid context resource',
    fn() => file($content, 0, $handle)
);
file_content_case(
    'readfile invalid context resource',
    fn() => readfile($content, false, $handle)
);
fclose($handle);

foreach ([
    'bad flags' => fn() => file($content, 8),
    'empty path' => fn() => file(''),
    'nul path' => fn() => sha1_file("x\0y"),
    'file scalar context' => fn() => file($content, 0, 42),
    'readfile scalar context' => fn() => readfile($content, false, 42),
] as $label => $probe) {
    file_content_case($label, $probe);
}

set_error_handler(static function (int $level, string $message): bool {
    echo 'warning:', $level, ':', $message, "\n";
    return true;
});
foreach ([
    'file missing' => fn() => file('php2go-missing-content-probe'),
    'readfile missing' => fn() => readfile('php2go-missing-content-probe'),
    'md5 missing' => fn() => md5_file('php2go-missing-content-probe'),
    'sha1 missing' => fn() => sha1_file('php2go-missing-content-probe'),
] as $label => $probe) {
    file_content_case($label, $probe);
}
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}
func TestGetMetaTagsUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "basic.html"), `<html><head>
<meta name="author" content="name">
<meta name="DESCRIPTION" content="a php manual">
<meta name="geo.position" content="49.33;-86.59">
<meta name=dup content=first><meta content=second name=dup>
</head><meta name=after content=no></html>`)
	mustWrite(t, filepath.Join(root, "recovery.html"), `<meta name="author" content="name"
<meta name="keywords" content="php documentation">
<meta <meta name="inside" content="yes">
<meta name="unfinished" content="no"`)
	mustWrite(t, filepath.Join(root, "tokens.html"), `<!-- <META CONTENT="x" NAME="A.B-c:d"> -->
<meta name="a.b/c\d?e[f]g+h" content="raw&amp;b&#65;">
<meta name=123 content=num><meta name=x>
<meta http-equiv=refresh content="5">
<meta name=q content='single quoted'>
<meta name=gt content="a>b"><meta name=next content=ok>`)
	mustWrite(t, filepath.Join(root, "whitespace.html"), "<meta\tname=\"tab\"\ncontent=\"yes\">"+
		"<meta name =\"before\" content =\"space\">"+
		"<meta name= \"after\" content=\"ignored\">"+
		"<meta name=\"empty\" content= \"ignored\">")
	mustWrite(t, filepath.Join(root, "nul.html"),
		"<meta name=before content=yes>\x00<meta name=after content=no>")
	mustWrite(t, filepath.Join(root, "inc", "included.html"),
		"<meta name=include content=found>")
	hostTarget := filepath.Join(t.TempDir(), "meta-host.html")
	source := strings.Replace(`<?php
function meta_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

foreach ([
    'basic' => __DIR__ . '/basic.html',
    'recovery' => __DIR__ . '/recovery.html',
    'tokens' => __DIR__ . '/tokens.html',
    'whitespace' => __DIR__ . '/whitespace.html',
    'nul byte' => __DIR__ . '/nul.html',
] as $label => $path) {
    meta_case($label, fn() => get_meta_tags($path));
}

set_include_path(__DIR__ . '/inc');
meta_case(
    'include path',
    fn() => get_meta_tags('included.html', true)
);

meta_case(
    'data uri',
    fn() => get_meta_tags(
        'data:text/html,%3Cmeta+name%3Ddata+content%3Duri%3E'
    )
);

file_put_contents(
    __HOST_PATH__,
    '<meta name=host content=fallback>'
);
meta_case(
    'host path',
    fn() => get_meta_tags(__HOST_PATH__)
);
foreach ([8191, 8192, 8193] as $length) {
    file_put_contents(
        __HOST_PATH__,
        '<meta name="' . str_repeat('a', $length) .
        '" content="' . str_repeat('b', $length) . '">'
    );
    meta_case(
        'token cap ' . $length,
        function () {
            $result = get_meta_tags(__HOST_PATH__);
            $key = array_key_first($result);
            return [count($result), strlen($key), strlen($result[$key])];
        }
    );
}
unlink(__HOST_PATH__);

set_error_handler(static function (int $level, string $message): bool {
    echo 'warning:', $level, ':', $message, "\n";
    return true;
});
meta_case(
    'missing',
    fn() => get_meta_tags('php2go-meta-missing')
);
restore_error_handler();

meta_case('empty path', fn() => get_meta_tags(''));
meta_case('nul path', fn() => get_meta_tags("bad\0path"));
meta_case('type', fn() => get_meta_tags([]));
`, "__HOST_PATH__", fmt.Sprintf("%q", hostTarget), -1)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}

func TestIptcUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	fixture, err := os.ReadFile(filepath.Join(
		"..", "..", "third_party", "php-src", "ext", "standard",
		"tests", "image", "test1pix.jpg",
	))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "image.jpg"), string(fixture))
	mustWrite(t, filepath.Join(root, "invalid.jpg"), "-1-1")
	mustWrite(t, filepath.Join(root, "scan.jpg"), "\xff\xd8\xff\xda\x00\x02AB")
	mustWrite(t, filepath.Join(root, "eoi.jpg"), "\xff\xd8\xff\xd9")
	replacementTarget := filepath.Join(t.TempDir(), "iptc-replacement.jpg")
	source := strings.Replace(`<?php
function iptc_test_byte(int $value): string {
    return chr($value);
}

function iptc_tag(
    int $record,
    int $dataset,
    string $value,
    bool $long = false
): string {
    $prefix = iptc_test_byte(28) .
        iptc_test_byte($record) . iptc_test_byte($dataset);
    if ($long) {
        return $prefix . iptc_test_byte(128) . iptc_test_byte(4) .
            pack('N', strlen($value)) . $value;
    }
    return $prefix . pack('n', strlen($value)) . $value;
}

function iptc_case(string $label, callable $probe): void {
    echo '==', $label, "==\n";
    try {
        var_dump($probe());
    } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}

function iptc_embed_summary(
    string $data,
    string $filename,
    int $spool
): array {
    ob_start();
    $result = iptcembed($data, $filename, $spool);
    $captured = ob_get_clean();
    return [
        get_debug_type($result),
        is_string($result) ? strlen($result) : $result,
        is_string($result) ? md5($result) : null,
        is_string($result) ? iptcparse($result) : null,
        strlen($captured),
        $captured === '' ? null : md5($captured),
        $captured === '' ? null : iptcparse($captured),
        is_string($result) && $captured !== ''
            ? $captured === $result
            : null,
    ];
}

$parsed =
    "prefix" .
    iptc_tag(2, 5, 'A') .
    iptc_tag(2, 5, 'B') .
    iptc_tag(1, 90, 'long', true) .
    "tail";
iptc_case('parse records', fn() => iptcparse($parsed));

$emptyThenValue =
    iptc_tag(2, 1, '') .
    iptc_tag(2, 2, 'x');
iptc_case('parse empty record', fn() => iptcparse($emptyThenValue));

foreach ([
    '',
    'x',
    iptc_test_byte(28),
    iptc_test_byte(28) . iptc_test_byte(2) . iptc_test_byte(5) . iptc_test_byte(0),
    iptc_test_byte(28) . iptc_test_byte(2) . iptc_test_byte(5) . iptc_test_byte(0) . iptc_test_byte(2) . 'A',
    iptc_test_byte(28) . iptc_test_byte(2) . iptc_test_byte(5) . iptc_test_byte(128) . '___',
    'junk' . iptc_test_byte(28) . iptc_test_byte(3) . iptc_test_byte(1) . iptc_test_byte(0) . iptc_test_byte(1) . 'x',
] as $index => $malformed) {
    iptc_case(
        'parse malformed ' . $index,
        fn() => iptcparse($malformed)
    );
}

$image = __DIR__ . '/image.jpg';
foreach ([
    'one' => iptc_tag(2, 5, 'A'),
    'two' => iptc_tag(2, 5, 'AB'),
] as $label => $data) {
    foreach ([-1, 0, 1, 2, 3] as $spool) {
        iptc_case(
            'embed ' . $label . ' spool ' . $spool,
            fn() => iptc_embed_summary($data, $image, $spool)
        );
    }
}

$first = iptcembed(iptc_tag(2, 5, 'old'), $image);
$written = file_put_contents(__REPLACE_PATH__, $first);
iptc_case('write replacement', fn() => [
    $written,
    file_exists(__REPLACE_PATH__),
]);
$second = iptcembed(
    iptc_tag(2, 5, 'new'),
    __REPLACE_PATH__
);
iptc_case('replace app13', fn() => [
    strlen($first),
    strlen($second),
    md5($first),
    md5($second),
    iptcparse($second),
]);
unlink(__REPLACE_PATH__);

iptc_case(
    'scan marker',
    fn() => bin2hex(iptcembed(
        iptc_tag(2, 5, 'x'),
        __DIR__ . '/scan.jpg'
    ))
);
iptc_case(
    'eoi marker',
    fn() => bin2hex(iptcembed(
        iptc_tag(2, 5, 'x'),
        __DIR__ . '/eoi.jpg'
    ))
);
iptc_case(
    'invalid jpeg return',
    fn() => iptcembed(
        iptc_tag(2, 5, 'x'),
        __DIR__ . '/invalid.jpg'
    )
);
iptc_case('invalid jpeg spool', function () {
    ob_start();
    $result = iptcembed(
        iptc_tag(2, 5, 'x'),
        __DIR__ . '/invalid.jpg',
        1
    );
    return [$result, bin2hex(ob_get_clean())];
});

set_error_handler(static function (int $level, string $message): bool {
    echo 'warning:', $level, ':', $message, "\n";
    return true;
});
iptc_case(
    'missing',
    fn() => iptcembed('x', 'php2go-iptc-missing')
);
iptc_case('empty path', fn() => iptcembed('x', ''));
restore_error_handler();

iptc_case(
    'nul path',
    fn() => iptcembed('x', "bad\0path")
);
`, "__REPLACE_PATH__", fmt.Sprintf("%q", replacementTarget), -1)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;",
		filepath.Join(root, "main.php"),
	))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf(
			"bundle output = %q, engine output = %q",
			got.String(),
			want,
		)
	}
}
func TestDNSQueriesMatchEngine(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function stable_dns(array $records): array {
    foreach ($records as &$record) {
        $record["ttl"] = 0;
        if (isset($record["data"])) {
            $record["data"] = bin2hex($record["data"]);
        }
    }
    return $records;
}
var_dump(
    dns_check_record("localhost", "A"),
    checkdnsrr("localhost", "A"),
    dns_check_record("localhost", "MX")
);
$authority = "old";
$additional = "old";
var_dump(stable_dns(dns_get_record("localhost", DNS_A, $authority, $additional)));
var_dump(stable_dns($authority), stable_dns($additional));
var_dump(stable_dns(dns_get_record("localhost", 1, $authority, $additional, true)));
var_dump(dns_get_mx("localhost", $hosts, $weights), $hosts, $weights);
var_dump(getmxrr("localhost", $alias_hosts, $alias_weights), $alias_hosts, $alias_weights);
$a = $b = null;
foreach ([
    fn() => dns_check_record("", "A"),
    fn() => checkdnsrr("", "A"),
    fn() => dns_check_record("localhost", "HINFO"),
    fn() => checkdnsrr("localhost", "HINFO"),
    fn() => dns_get_record("localhost", -1),
    fn() => dns_get_record("localhost", 0, $a, $b, true),
] as $case) {
    try {
        $case();
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestLibcNetworkDatabasesMatchEngine(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
set_error_handler(function (int $level, string $message): bool {
    echo "W:", $level, ":", $message, "\n";
    return true;
}, E_WARNING);

var_dump(
    gethostbyname("localhost"),
    gethostbynamel("localhost"),
    gethostbyaddr("127.0.0.1"),
    gethostbyname("1.2.3"),
    gethostbynamel("01.2.3.4"),
    gethostbyaddr("255.255.255.255"),
    gethostbyname(str_repeat("a", 256)),
    gethostbynamel(str_repeat("a", 256))
);

var_dump(
    getprotobyname("tcp"),
    getprotobyname("TCP"),
    getprotobyname("IP-ENCAP"),
    getprotobyname("ip-encap"),
    getprotobynumber(6),
    getprotobynumber(4294967302),
    getprotobynumber(999)
);

var_dump(
    getservbyname("http", "tcp"),
    getservbyname("www", "tcp"),
    getservbyname("HTTP", "tcp"),
    getservbyname("http", "TCP"),
    getservbyport(80, "tcp"),
    getservbyport(65616, "tcp"),
    getservbyport(80, "TCP")
);

foreach ([
    fn() => gethostbyname("a\0b"),
    fn() => gethostbynamel("a\0b"),
    fn() => gethostbyaddr("a\0b"),
    fn() => getprotobyname("a\0b"),
    fn() => getservbyname("a\0b", "tcp"),
    fn() => getservbyname("http", "t\0cp"),
    fn() => getservbyport(80, "t\0cp"),
] as $case) {
    try {
        $case();
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestNetworkInterfacesMatchEngine(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function stable_interfaces(mixed $interfaces): mixed {
    if (!is_array($interfaces)) {
        return $interfaces;
    }
    $valid = $interfaces !== [];
    $loopback = null;
    $allowed = ['flags', 'family', 'address', 'netmask', 'broadcast', 'ptp'];
    foreach ($interfaces as $name => $interface) {
        if (!is_string($name) || !is_array($interface) ||
            array_keys($interface) !== ['unicast', 'up'] ||
            !is_array($interface['unicast']) || !is_bool($interface['up'])) {
            $valid = false;
            continue;
        }
        foreach ($interface['unicast'] as $unicast) {
            if (!is_array($unicast) || array_keys($unicast)[0] !== 'flags' ||
                !is_int($unicast['flags'])) {
                $valid = false;
                continue;
            }
            foreach ($unicast as $key => $value) {
                if (!in_array($key, $allowed, true)) {
                    $valid = false;
                }
                if ($key === 'family' && !is_int($value)) {
                    $valid = false;
                } elseif ($key !== 'flags' && $key !== 'family' &&
                    !is_string($value)) {
                    $valid = false;
                }
            }
            if (($unicast['address'] ?? null) === '127.0.0.1') {
                $loopback = ['up' => $interface['up'], 'unicast' => $unicast];
            }
        }
    }
    return [$valid, $loopback];
}
var_dump(stable_interfaces(net_get_interfaces()));
try {
    net_get_interfaces(1);
} catch (Throwable $error) {
    echo $error::class, ':', $error->getMessage(), "\n";
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf(
		"require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestResponseHeadersAndCookiesMatchEngine(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
header_remove();
var_dump(headers_sent($file, $line), $file, $line, headers_list(), http_response_code());
header("X-Test: one");
header("X-Test: two");
header("X-Test: three", false);
header("Location: /next");
setcookie("plain", "hello world", ["path" => "/x", "secure" => true, "httponly" => true, "samesite" => "Lax"]);
setrawcookie("raw", "a+b");
setcookie("gone", "");
setcookie("past", "v", 1);
header("Content-Type: text/plain");
var_dump(headers_list(), http_response_code());
var_dump(http_response_code(204), http_response_code());
header_remove("x-test");
var_dump(headers_list());
set_error_handler(static function (int $level, string $message): bool {
    echo $level, ":", $message, "\n";
    return true;
});
header("X-Bad: a\nb");
header_remove("Bad:");
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestHeaderRegisterCallbackEmbedSAPIMatchesEngine(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
class HeaderCallbackTarget {
    public function __destruct() { echo "destruct\n"; }
    public function run() { echo "callback\n"; }
}

function register_case(string $label, mixed $callback): void {
    echo $label, ":";
    try {
        var_dump(header_register_callback($callback));
    } catch (Throwable $error) {
        echo $error::class, ":", $error->getMessage(), "\n";
    }
}

register_case("closure", static function () { echo "callback\n"; });
register_case("function", "strlen");
register_case("missing", "header_callback_missing");
register_case("empty-array", []);
register_case("scalar", 1);

$target = new HeaderCallbackTarget();
var_dump(header_register_callback([$target, "run"]));
unset($target);
echo "after-object\n";

var_dump(header_register_callback(callback: static function () { echo "named\n"; }));
header("X-Callback-Test: one");
echo "after-header\n";

try {
    header_register_callback();
} catch (Throwable $error) {
    echo $error::class, ":", $error->getMessage(), "\n";
}
try {
    header_register_callback(static function () {}, 1);
} catch (Throwable $error) {
    echo $error::class, ":", $error->getMessage(), "\n";
}
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestResponseHeadersSuperglobalIsRequestLocal(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
var_dump($_RESP_HEADERS);
function response_headers_from_function(): void {
    var_dump($_RESP_HEADERS);
}
response_headers_from_function();
`)
	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	ctx := php.NewCtx(&got)
	if _, err := app.Run(ctx); err != nil {
		t.Fatal(err)
	}
	responseHeaders, registered := ctx.Globals.Get(php.StringKey("_RESP_HEADERS"))
	if !registered || responseHeaders.Deref().Kind() != php.KindArray {
		t.Fatalf("$_RESP_HEADERS = %v (registered %t), want request-local array", responseHeaders, registered)
	}
	header := "X-Powered-By: PHP/" + zend.Version()
	one := fmt.Sprintf("array(1) {\n  [0]=>\n  string(%d) %q\n}\n", len(header), header)
	want := one + one
	if got.String() != want {
		t.Fatalf("superglobal output = %q, want %q", got.String(), want)
	}
}

func TestGetoptEmbedSAPIUserlandMatchesEngine(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
class GetoptStringable {
    public function __toString(): string { return "long"; }
}

function getopt_case(string $label, callable $case): void {
    echo $label, ":";
    try {
        var_dump($case());
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}

set_error_handler(static function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});

getopt_case("empty", fn() => getopt(""));
getopt_case("short", fn() => getopt("a:b::c"));
getopt_case("short-null", fn() => getopt(null));
getopt_case("short-array", fn() => getopt([]));
getopt_case("long-type", fn() => getopt("a", null));

$direct_rest = 77;
var_dump(getopt("a", [], $direct_rest), $direct_rest);

$rest = 77;
getopt_case("named", fn() => getopt(
    short_options: "a",
    long_options: ["long:", 12, null, []],
    rest_index: $rest
));
var_dump($rest);

$rest = 77;
getopt_case("stringable", fn() => getopt(
    "a",
    [new GetoptStringable],
    $rest
));
var_dump($rest);

$rest = 77;
getopt_case("bad-object", fn() => getopt("a", [new stdClass], $rest));
var_dump($rest);
getopt_case("literal-reference", fn() => getopt("a", [], 1));
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if got.String() != want {
		t.Fatalf("bundle output = %q, engine output = %q", got.String(), want)
	}
}

func TestUniqidLibcUserlandMatchesEngine(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
class UniqidStringable {
    public function __toString(): string { return "obj-"; }
}

function uniqid_shape(string $value, string $prefix, bool $entropy): array {
    $tail = substr($value, strlen($prefix));
    $base = substr($tail, 0, 13);
    $hex = strspn($base, "0123456789abcdef") === 13;
    $microseconds = hexdec(substr($base, 8, 5));
    $suffix = substr($tail, 13);
    $entropy_shape = $suffix === "";
    if ($entropy) {
        $dot = strpos($suffix, ".");
        $whole = $dot === false ? "" : substr($suffix, 0, $dot);
        $fraction = $dot === false ? "" : substr($suffix, $dot + 1);
        $entropy_shape = ($whole === "0" || $whole === "1" ||
            $whole === "2" || $whole === "3" || $whole === "4" ||
            $whole === "5" || $whole === "6" || $whole === "7" ||
            $whole === "8" || $whole === "9" || $whole === "10") &&
            strlen($fraction) === 8 &&
            strspn($fraction, "0123456789") === 8 &&
            (float) $suffix >= 0.0 && (float) $suffix <= 10.0;
    }
    return [
        strncmp($value, $prefix, strlen($prefix)) === 0,
        strlen($base) === 13,
        $hex,
        $microseconds >= 0 && $microseconds <= 999999,
        $entropy_shape,
    ];
}

foreach ([["", false], ["p", false], ["pre-", true], ["a\0b", false]] as [$prefix, $entropy]) {
    $effective = strpos($prefix, "\0");
    $effective = $effective === false ? $prefix : substr($prefix, 0, $effective);
    var_dump(uniqid_shape(uniqid($prefix, $entropy), $effective, $entropy));
}

$values = [];
$valid = true;
for ($index = 0; $index < 100; $index++) {
    $value = uniqid();
    $values[] = $value;
    $valid = $valid && uniqid_shape($value, "", false) === [true, true, true, true, true];
}
var_dump($valid, count(array_unique($values)) === 100);
var_dump(uniqid_shape(uniqid(prefix: "named-", more_entropy: true), "named-", true));

set_error_handler(static function (int $level, string $message): bool {
    echo "diagnostic:", $level, ":", $message, "\n";
    return true;
});
foreach ([null, false, true, 12, 1.5, new UniqidStringable, [], new stdClass] as $index => $prefix) {
    echo "prefix:", $index, ":";
    try {
        $value = uniqid($prefix);
        $expected = $prefix === null || $prefix === false ? "" :
            ($prefix === true ? "1" : (string) $prefix);
        var_dump(uniqid_shape($value, $expected, false));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}
foreach ([null, false, true, 0, 1, 1.5, "", "0", [], new stdClass] as $index => $entropy) {
    echo "entropy:", $index, ":";
    try {
        var_dump(uniqid_shape(uniqid("e-", $entropy), "e-", (bool) $entropy));
    } catch (Throwable $error) {
        echo get_class($error), ":", $error->getMessage(), "\n";
    }
}
try { uniqid("", false, true); } catch (Throwable $error) { echo get_class($error), ":", $error->getMessage(), "\n"; }
try { uniqid(extra: true); } catch (Throwable $error) { echo get_class($error), ":", $error->getMessage(), "\n"; }
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	var second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want {
		t.Fatalf("first bundle output = %q, engine output = %q", first.String(), want)
	}
	if second.String() != want {
		t.Fatalf("second bundle output = %q, engine output = %q", second.String(), want)
	}
}

func TestSyslogNativeUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
var_dump(openlog("php2go-bundle", LOG_PID, LOG_USER));
var_dump(syslog(LOG_DEBUG, "php2go bundle syslog differential"));
echo "request-end\n";
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want || second.String() != want {
		t.Fatalf("bundle outputs = %q, %q; engine output = %q",
			first.String(), second.String(), want)
	}
}

func TestProcessExecutionNativeMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
$command = "printf 'one\\ntwo  \\nlast\\t \\n'; exit 7";
var_dump(shell_exec("printf bundle"));
$output = ["seed"];
$code = 99;
var_dump(exec($command, $output, $code), $output, $code);
ob_start();
$last = system($command, $code);
$system_output = ob_get_clean();
var_dump($system_output, $last, $code);
ob_start();
$return = passthru("printf '\\000raw'; exit 3", $code);
$passthru_output = ob_get_clean();
var_dump(bin2hex($passthru_output), $return, $code);
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want || second.String() != want {
		t.Fatalf("bundle outputs = %q, %q; engine output = %q",
			first.String(), second.String(), want)
	}
}

func TestProcessPipeNativeMatchesEngineAndCleansUp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	pipeOutput := filepath.Join(t.TempDir(), "pipe-output.bin")
	mustWrite(t, filepath.Join(root, "main.php"), fmt.Sprintf(`<?php
$read = popen("printf bundle; exit 4", "r");
var_dump(fread($read, 8192), pclose($read));
$write = popen("cat > " . escapeshellarg(%q), "w");
var_dump(fwrite($write, "cleanup\0payload"));
echo "request-end\n";
`, pipeOutput))

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(pipeOutput); err != nil || string(contents) != "cleanup\x00payload" {
		t.Fatalf("first request pipe cleanup = %q, %v", contents, err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(pipeOutput); err != nil || string(contents) != "cleanup\x00payload" {
		t.Fatalf("second request pipe cleanup = %q, %v", contents, err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if contents, err := os.ReadFile(pipeOutput); err != nil || string(contents) != "cleanup\x00payload" {
		t.Fatalf("engine request pipe cleanup = %q, %v", contents, err)
	}
	if first.String() != want || second.String() != want {
		t.Fatalf("bundle outputs = %q, %q; engine output = %q",
			first.String(), second.String(), want)
	}
}

func TestGetHeadersUserlandMatchesEngine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Date", "Thu, 01 Jan 1970 00:00:00 GMT")
		switch request.URL.Path {
		case "/plain":
			writer.Header().Set("Content-Length", "4")
			writer.Header().Set("X-One", "a")
			writer.Header().Add("X-Dup", "first")
			writer.Header().Add("X-Dup", "second")
			writer.Header().Set("X-Space", "value")
			_, _ = io.WriteString(writer, "BODY")
		case "/redirect", "/redirect-post", "/redirect-keep":
			writer.Header().Set("Location", "/plain")
			if request.URL.Path != "/redirect" {
				writer.Header().Set("Location", "/request")
			}
			writer.Header().Set("X-Hop", "redirect")
			if request.URL.Path == "/redirect-keep" {
				writer.WriteHeader(http.StatusTemporaryRedirect)
			} else {
				writer.WriteHeader(http.StatusFound)
			}
		case "/request":
			body, _ := io.ReadAll(request.Body)
			writer.Header().Set("Content-Length", "0")
			writer.Header().Set("X-Body", string(body))
			writer.Header().Set("X-Custom", request.Header.Get("X-Custom"))
			writer.Header().Set("X-Method", request.Method)
			writer.WriteHeader(http.StatusOK)
		case "/proxy":
			writer.Header().Set("Content-Length", "0")
			writer.Header().Set("X-Host", request.Host)
			writer.Header().Set("X-Request-URI", request.RequestURI)
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	proxyAddress := "tcp://" + strings.TrimPrefix(server.URL, "http://")

	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), fmt.Sprintf(`<?php
$base = %q;
var_dump(get_headers($base . '/plain'));
var_dump(get_headers($base . '/plain', true));
var_dump(get_headers($base . '/redirect'));
$context = stream_context_create(['http' => [
    'method' => 'POST',
    'header' => ['X-Custom: custom'],
    'content' => 'abc',
    'follow_location' => 0,
]]);
set_error_handler(function ($level, $message) {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});
var_dump(get_headers($base . '/request', true, $context));
$redirect = stream_context_create(['http' => [
    'method' => 'POST',
    'header' => ['X-Custom: redirect'],
    'content' => 'abc',
]]);
var_dump(get_headers($base . '/redirect-post', true, $redirect));
var_dump(get_headers($base . '/redirect-keep', true, $redirect));
$one = stream_context_create(['http' => ['max_redirects' => 1]]);
var_dump(get_headers($base . '/redirect', false, $one));
$zero = stream_context_create(['http' => ['max_redirects' => 0]]);
var_dump(get_headers($base . '/redirect', false, $zero));
$proxy = stream_context_create(['http' => [
    'proxy' => %q,
    'request_fulluri' => true,
]]);
var_dump(get_headers('http://php2go.invalid/proxy', true, $proxy));
restore_error_handler();
`, server.URL, proxyAddress))

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want || second.String() != want {
		t.Fatalf("bundle outputs = %q, %q; engine output = %q",
			first.String(), second.String(), want)
	}
}

func TestGlobNativeUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := filepath.Join(t.TempDir(), "glob-host")
	if err := os.Mkdir(host, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha.txt", "beta.log", "literal*.txt", ".hidden.txt"} {
		mustWrite(t, filepath.Join(host, name), name)
	}
	if err := os.Mkdir(filepath.Join(host, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha.php", "beta.txt", ".hidden.php"} {
		mustWrite(t, filepath.Join(root, "assets", name), name)
	}
	mustWrite(t, filepath.Join(root, "assets", "nested", "child.php"), "child")

	source := strings.NewReplacer(
		"__HOST__", fmt.Sprintf("%q", host),
	).Replace(`<?php
function glob_shape(array|false $paths): array|false {
    if ($paths === false) {
        return false;
    }
    return array_map(
        fn($path) => [basename(rtrim($path, DIRECTORY_SEPARATOR)), str_ends_with($path, DIRECTORY_SEPARATOR)],
        $paths
    );
}
function glob_error(string $label, callable $call): void {
    echo $label, ':';
    try { var_dump($call()); } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
$host = __HOST__;
var_dump(glob_shape(glob($host . '/*.txt')));
var_dump(glob_shape(glob($host . '/*.{txt,log}', GLOB_BRACE)));
var_dump(glob_shape(glob($host . '/*', GLOB_ONLYDIR)));
var_dump(glob_shape(glob($host . '/*', GLOB_MARK)));
var_dump(glob_shape(glob($host . '/literal\\*.txt')));
var_dump(glob_shape(glob($host . '/literal\\*.txt', GLOB_NOESCAPE)));
var_dump(glob_shape(glob($host . '/missing-*')));
var_dump(glob_shape(glob($host . '/missing-*', GLOB_NOCHECK)));
var_dump(glob_shape(glob($host . '/*')));

$assets = __DIR__ . '/assets';
var_dump(glob_shape(glob($assets . '/*')));
var_dump(glob_shape(glob($assets . '/*.{php,txt}', GLOB_BRACE)));
var_dump(glob_shape(glob($assets . '/*', GLOB_ONLYDIR)));
var_dump(glob_shape(glob($assets . '/*', GLOB_MARK)));
var_dump(glob_shape(glob($assets . '/*/*.php')));
var_dump(glob_shape(glob($assets . '/missing-*', GLOB_NOCHECK)));
var_dump(glob_shape(glob(
    $assets . '/missing-*.{php,txt}',
    GLOB_BRACE | GLOB_NOCHECK
)));

set_error_handler(function ($level, $message) {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});
var_dump(glob($host . '/*', 1 << 29));
var_dump(glob(str_repeat('x', PHP_MAXPATHLEN)));
restore_error_handler();
glob_error('empty', fn() => glob(''));
glob_error('nul', fn() => glob("x\0y"));
glob_error('named', fn() => glob(pattern: $host . '/*.log', flags: 0));
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if _, err := app.Run(php.NewCtx(&first)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Run(php.NewCtx(&second)); err != nil {
		t.Fatal(err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first.String() != want || second.String() != want {
		t.Fatalf("bundle outputs = %q, %q; engine output = %q",
			first.String(), second.String(), want)
	}
}

func TestMailAndErrorLogUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	host := t.TempDir()
	mailBin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "mail-capture")
	mustWrite(t, filepath.Join(mailBin, "-t"), `#!/bin/sh
printf 'ARGS:%s\n' "$*" >> "$PHP2GO_MAIL_CAPTURE"
cat >> "$PHP2GO_MAIL_CAPTURE"
printf '\n--END--\n' >> "$PHP2GO_MAIL_CAPTURE"
`)
	if err := os.Chmod(filepath.Join(mailBin, "-t"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", mailBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PHP2GO_MAIL_CAPTURE", capture)

	source := strings.NewReplacer(
		"__LOG__", fmt.Sprintf("%q", filepath.Join(host, "error.log")),
		"__MISSING__", fmt.Sprintf("%q", filepath.Join(host, "missing", "error.log")),
	).Replace(`<?php
function logging_case(string $label, callable $call): void {
    echo $label, ':';
    try { var_dump($call()); } catch (Throwable $error) {
        echo get_class($error), ':', $error->getMessage(), "\n";
    }
}
set_error_handler(function ($level, $message) {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});
$log = __LOG__;
@unlink($log);
logging_case('append-binary', fn() => error_log("a\0b", 3, $log));
logging_case('append-tail', fn() => error_log('tail', 3, $log));
var_dump(bin2hex(file_get_contents($log)));
logging_case('type2', fn() => error_log('x', 2));
logging_case('type3-null', fn() => error_log('x', 3));
logging_case('type3-missing', fn() => error_log('x', 3, __MISSING__));
logging_case('type3-data', fn() => error_log('x', 3, 'data:text/plain,x'));
logging_case('type3-unknown', fn() => error_log('x', 3, 'zz://x'));
logging_case('type3-nul', fn() => error_log('x', 3, "bad\0path"));
logging_case('email', fn() => error_log(
    "body\0hidden", 1, 'log@example.test', "X-Log: yes\0hidden"
));
logging_case('email-malformed', fn() => error_log(
    'body', 1, 'log@example.test', "X: y\n\nZ: q"
));

logging_case('mail-array', fn() => mail(
    "to@example.test\n",
    "subject\t",
    "message\nline",
    ['X-One' => 'value', 'X-Many' => ['first', 'second']],
    '-f sender@example.test'
));
logging_case('mail-string', fn() => mail(
    'two@example.test', 'two', 'body', "  X-Raw: yes  "
));
logging_case('mail-named', fn() => mail(
    to: 'named@example.test', subject: 'named', message: 'body'
));
logging_case('mail-nul-to', fn() => mail("x\0y", 's', 'm'));
logging_case('mail-nul-subject', fn() => mail('x', "s\0y", 'm'));
logging_case('mail-nul-message', fn() => mail('x', 's', "m\0y"));
logging_case('mail-nul-header', fn() => mail('x', 's', 'm', "X: x\0y"));
logging_case('mail-numeric-header', fn() => mail('x', 's', 'm', [0 => 'x']));
logging_case('mail-to-header', fn() => mail('x', 's', 'm', ['To' => 'y']));
logging_case('mail-subject-header', fn() => mail('x', 's', 'm', ['SUBJECT' => 'y']));
logging_case('mail-bad-name', fn() => mail('x', 's', 'm', ['Bad:Name' => 'y']));
logging_case('mail-bad-lf', fn() => mail('x', 's', 'm', ['X' => "a\nb"]));
logging_case('mail-array-special', fn() => mail('x', 's', 'm', ['From' => ['a', 'b']]));
logging_case('mail-array-key', fn() => mail('x', 's', 'm', ['X' => ['k' => 'v']]));
logging_case('mail-array-value', fn() => mail('x', 's', 'm', ['X' => [1]]));
logging_case('mail-value-type', fn() => mail('x', 's', 'm', ['X' => 1]));
logging_case('mail-malformed', fn() => mail('x', 's', 'm', "X: y\n\nZ: q"));
restore_error_handler();
`)
	mustWrite(t, filepath.Join(root, "main.php"), source)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	runBundle := func() (string, string) {
		t.Helper()
		_ = os.Remove(capture)
		_ = os.Remove(filepath.Join(host, "error.log"))
		var out bytes.Buffer
		if _, err := app.Run(php.NewCtx(&out)); err != nil {
			t.Fatal(err)
		}
		delivered, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		return out.String(), string(delivered)
	}
	first, firstMail := runBundle()
	second, secondMail := runBundle()

	_ = os.Remove(capture)
	_ = os.Remove(filepath.Join(host, "error.log"))
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	wantMail, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if first != want || second != want {
		t.Fatalf("bundle outputs = %q, %q; engine output = %q", first, second, want)
	}
	if firstMail != string(wantMail) || secondMail != string(wantMail) {
		t.Fatalf("bundle mail payloads = %q, %q; engine payload = %q",
			firstMail, secondMail, wantMail)
	}
}

func TestEmbeddedErrorLogDestinationIsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
set_error_handler(function ($level, $message) {
    echo $level, ':', $message, "\n";
    return true;
});
var_dump(error_log('x', 3, __DIR__ . '/embedded.log'));
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if _, err := app.Run(php.NewCtx(&got)); err != nil {
		t.Fatal(err)
	}
	want := "2:error_log(/app/embedded.log): Failed to open stream: Read-only file system\nbool(false)\n"
	if got.String() != want {
		t.Fatalf("bundle output = %q, want %q", got.String(), want)
	}
}

func TestHTMLTranslationTableUserlandMatchesEngine(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	mustWrite(t, filepath.Join(root, "main.php"), `<?php
function translation_case(int $table, int $flags, string $encoding): void {
    $entries = get_html_translation_table($table, $flags, $encoding);
    $first = array_key_first($entries);
    $last = array_key_last($entries);
    printf(
        "%s t=%d f=%d n=%d md5=%s first=%s last=%s\n",
        base64_encode($encoding),
        $table,
        $flags,
        count($entries),
        md5(serialize($entries)),
        base64_encode($first . '=' . $entries[$first]),
        base64_encode($last . '=' . $entries[$last])
    );
}
set_error_handler(static function (int $level, string $message): bool {
    echo 'diagnostic:', $level, ':', $message, "\n";
    return true;
});

$encodings = [
    'UTF-8', 'ISO-8859-1', 'ISO-8859-15', 'CP1252', 'CP1251',
    'ISO-8859-5', 'CP866', 'MacRoman', 'KOI8-R',
];
foreach ($encodings as $encoding) {
    foreach ([ENT_HTML401, ENT_XML1, ENT_XHTML, ENT_HTML5] as $doctype) {
        foreach ([HTML_SPECIALCHARS, HTML_ENTITIES] as $table) {
            foreach ([ENT_NOQUOTES, ENT_COMPAT, ENT_QUOTES] as $quotes) {
                translation_case(
                    $table,
                    $doctype | $quotes | ENT_SUBSTITUTE,
                    $encoding
                );
            }
        }
    }
}

foreach ([
    'utf-8', 'ISO8859-1', 'Windows-1252', '1252', 'Windows-1251',
    'win-1251', 'iso8859-5', '866', 'ibm866', 'koi8-ru', 'koi8r',
    'BIG5', '950', 'GB2312', '936', 'BIG5-HKSCS', 'Shift_JIS',
    'SJIS', '932', 'SJIS-win', 'CP932', 'EUCJP', 'EUC-JP', 'eucJP-win',
    '', "UTF-8\0ignored", 'not-a-charset', "bad\"line\nname",
] as $encoding) {
    translation_case(HTML_ENTITIES, ENT_QUOTES | ENT_HTML5, $encoding);
}
translation_case(-1, ENT_QUOTES | ENT_HTML5, 'UTF-8');
translation_case(2, ENT_QUOTES | ENT_XHTML, 'UTF-8');
translation_case(HTML_SPECIALCHARS, -1, 'UTF-8');

$fresh = get_html_translation_table(HTML_ENTITIES, ENT_QUOTES | ENT_HTML5);
$fresh['x'] = 'changed';
translation_case(HTML_ENTITIES, ENT_QUOTES | ENT_HTML5, 'UTF-8');

try {
    get_html_translation_table(0, 3, []);
} catch (Throwable $error) {
    echo get_class($error), ':', $error->getMessage(), "\n";
}
try {
    get_html_translation_table(
        table: HTML_SPECIALCHARS,
        flags: ENT_QUOTES,
        encoding: 'UTF-8',
        extra: true
    );
} catch (Throwable $error) {
    echo get_class($error), ':', $error->getMessage(), "\n";
}
restore_error_handler();
`)

	output := filepath.Join(t.TempDir(), "generated")
	runtimeRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Root: root, Entry: "main.php", Output: output, RuntimeRoot: runtimeRoot,
	}); err != nil {
		t.Fatal(err)
	}
	app, err := bundle.Open(os.DirFS(filepath.Join(output, "bundle")))
	if err != nil {
		t.Fatal(err)
	}
	runBundle := func() string {
		t.Helper()
		var out bytes.Buffer
		if _, err := app.Run(php.NewCtx(&out)); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	first := runBundle()
	second := runBundle()

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, evalErr := engine.Eval(fmt.Sprintf("require %q;", filepath.Join(root, "main.php")))
	engine.Close()
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if first != want || second != want {
		t.Fatalf("bundle outputs = %q, %q; engine output = %q", first, second, want)
	}
}

func TestDefaultRootIsEntryDirectory(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "main.php")
	mustWrite(t, entry, "<?php echo 7;")
	output := filepath.Join(t.TempDir(), "generated")
	report, err := bundlebuild.Build(bundlebuild.Options{
		Entry: entry, Output: output, RuntimeRoot: "/php2go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Entry != "/app/main.php" {
		t.Fatalf("entry = %q, want /app/main.php", report.Entry)
	}
	if _, err := bundlebuild.Build(bundlebuild.Options{
		Entry: entry, Output: output, RuntimeRoot: "/php2go",
	}); err == nil {
		t.Fatal("second build accepted an existing output directory")
	}
}

func mustWrite(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
