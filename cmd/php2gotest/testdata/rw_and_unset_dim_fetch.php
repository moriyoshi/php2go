<?php
// A dimension fetch has three modes and they differ in what they say and what
// they leave behind. A **write** fetch is silent and creates; a **read-write**
// one warns about the container and about every missing key on the way in, and
// then creates; an **unset** one warns about the container and creates nothing
// -- `unset($m["a"]["b"])` leaves an undefined `$m` undefined.
echo "unset: "; unset($x[0]); var_dump(isset($x));
echo "rw: "; $y['k'] .= "s"; var_dump($y);
echo "rw int: "; $z['k'] += 1; var_dump($z);
echo "nested rw: "; $n['a']['b'] .= "s"; var_dump($n);
echo "unset nested: "; unset($m['a']['b']); var_dump(isset($m));
echo "defined rw: "; $d = ['k' => 1]; $d['k'] += 1; var_dump($d);
echo "defined unset: "; $e = [0 => 1]; unset($e[0]); var_dump($e);
echo "false rw: "; $f = false; unset($f[0]); var_dump($f);

// The write fetch, for contrast: silent, and it creates.
echo "w: "; $w['a']['b'] = 1; var_dump($w);
echo "append: "; $p[] = 1; var_dump($p);

// An unset chain still runs the **fetch's** conversion, it just has nothing to
// keep: `$q = false; unset($q[0][0]);` deprecates and leaves `$q` as false.
echo "false nested unset: ";
$q = false;
unset($q[0][0]);
var_dump($q);
echo "null nested unset: ";
$r = null;
unset($r[0][0]);
var_dump($r);
