<?php
// opendir/readdir/rewinddir/closedir/scandir over a local directory. The
// root, by absolute path: the compiled program and the engine run in
// different working directories, and only facts that do not depend on what
// the directory holds are printed.
$base = '/';
$d = opendir($base);
var_dump(is_resource($d), get_resource_type($d));
$names = [];
while (($entry = readdir($d)) !== false) {
    $names[] = $entry;
}
var_dump(in_array('.', $names, true), in_array('..', $names, true), in_array('tmp', $names, true));
var_dump(readdir($d));
rewinddir($d);
$again = [];
// The last directory opened is the default handle.
while (($entry = readdir()) !== false) {
    $again[] = $entry;
}
var_dump($again === $names);
closedir($d);
var_dump(get_debug_type($d));
try {
    readdir($d);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    readdir();
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
$sorted = scandir($base);
var_dump($sorted[0], $sorted[1], count($sorted) === count($names));
$copy = $names;
sort($copy, SORT_STRING);
var_dump($copy === $sorted);
$descending = scandir($base, SCANDIR_SORT_DESCENDING);
var_dump($descending === array_reverse($sorted));
var_dump(count(scandir($base, SCANDIR_SORT_NONE)) === count($names));
var_dump(opendir("/php2go-no-such-directory"));
var_dump(scandir("/php2go-no-such-directory"));
$viaWrapper = opendir('file://' . $base);
var_dump(is_resource($viaWrapper));
closedir($viaWrapper);
$plain = fopen('php://memory', 'r');
try {
    readdir($plain);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    rewinddir(fopen('php://memory', 'r'));
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
fclose($plain);
