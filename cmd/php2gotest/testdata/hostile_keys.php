<?php
$a = ['say "hi"' => 1, 'Ten%Off' => 2, 'Foo\Bar' => 3];
echo $a['say "hi"'], $a['Ten%Off'], $a['Foo\Bar'], "\n";
echo $a['missing "quoted"'] ?? "silent", "\n";
echo $a['no%such'], "\n";
echo $a['Ns\Missing'], "\n";
