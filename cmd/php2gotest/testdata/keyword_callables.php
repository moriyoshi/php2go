<?php
// `["C", "parent::func"]` names the parent's method. It is **deprecated, not
// refused**, and `self::` and `static::` are the same shape. The message quotes
// the class of element 0 whichever spelling it was, so `[$this, "parent::func"]`
// still reports `["C", "parent::func"]`.
class P { public function func() { echo "P::func\n"; } public static function sfunc() { echo "P::sfunc\n"; } }
class C extends P {
  public function func() { echo "C::func\n"; }
  public static function sfunc() { echo "C::sfunc\n"; }
  public function run() {
    call_user_func(["C", "parent::func"]);
    call_user_func([$this, "parent::func"]);
    call_user_func(["C", "self::sfunc"]);
    call_user_func(["C", "static::sfunc"]);
  }
}
(new C)->run();

// An ordinary callable is untouched by any of that.
class Plain {
    public function m() { echo "Plain::m\n"; }
    public static function s() { echo "Plain::s\n"; }
}
$p = new Plain;
call_user_func([$p, "m"]);
call_user_func(["Plain", "s"]);
call_user_func("Plain::s");
call_user_func(function () { echo "closure\n"; });

// A keyword as the **class** of a callable is deprecated too, in both
// spellings, and resolves against the calling scope -- with `static` meaning
// late static binding: the class the call named, not the one that declared the
// running body.
class KP { public static function s() { echo "KP::s\n"; } }
class KC extends KP {
    public static function s() { echo "KC::s\n"; }
    public static function go() {
        call_user_func('self::s');
        call_user_func('static::s');
        call_user_func('parent::s');
        call_user_func(['self', 's']);
        call_user_func(['static', 's']);
        call_user_func(['parent', 's']);
    }
}
KC::go();

class LP { static function only() { echo "LP::only\n"; } static function run() { call_user_func(['static', 'only']); } }
class LC extends LP { static function only() { echo "LC::only\n"; } }
LC::run();
