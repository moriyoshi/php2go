<?php
// chdir in compiled code changes the directory getcwd and relative paths use.
$start = getcwd();
$tmp = realpath(sys_get_temp_dir());
var_dump(chdir($tmp));
var_dump(getcwd() === $tmp);
$name = 'php2go_chdir_' . getmypid() . '.txt';
file_put_contents($name, "here");
var_dump(file_get_contents($tmp . '/' . $name));
unlink($tmp . '/' . $name);
var_dump(chdir('/nonexistent-php2go-dir'));
var_dump(getcwd() === $tmp);
var_dump(chdir(__FILE__));
try {
    chdir("a\0b");
} catch (ValueError $e) {
    echo $e->getMessage(), "\n";
}
var_dump(chdir($start), getcwd() === $start);
