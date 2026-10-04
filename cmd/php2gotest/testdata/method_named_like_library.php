<?php
// A method named like a library function is the class's own: `$f->touch()`,
// `F::stat()` and `[$f, 'unlink']()` run the method, with its own arity,
// argument checks and file -- never the library function of that name, which
// the program also links here because it names these strings.
class F {
    public $calls = [];
    function touch() { $this->calls[] = 'touch'; return func_num_args(); }
    function unlink(int $n) { return $n * 2; }
    static function stat($path = 'none') { return "stat($path)"; }
    static function chdir() { throw new RuntimeException('from chdir'); }
}
$f = new F;
var_dump($f->touch(), $f->touch(1, 2, 3, 4, 5));
var_dump($f->unlink(21));
try { $f->unlink('x'); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $f->unlink(); } catch (ArgumentCountError $e) { echo $e->getMessage(), "\n"; }
var_dump(F::stat(), F::stat('p', 'extra'));
try { F::chdir(); } catch (RuntimeException $e) { echo $e->getMessage(), ' @', $e->getLine(), "\n", $e->getTraceAsString(), "\n"; }
$m = 'touch'; var_dump($f->$m(1));
$s = 'stat'; var_dump(F::$s('dyn'));
var_dump(call_user_func([$f, 'unlink'], 5), call_user_func('F::stat', 'cuf'), [$f, 'touch']());
var_dump(array_map([$f, 'unlink'], [1, 2]));
var_dump($f->calls);
