<?php
// A backed case's value written as a constant expression is evaluated when
// the enum is first used, then validated as literal cases are.
const X = 'x';
interface I { const A = 'a'; const B = 'b'; const ONE = 1; }
enum E: string implements I {
    case P = I::A;
    case Q = X . 'q';
    case R = self::B . '!';
}
var_dump(E::P, E::Q->value, E::R->value, E::from('a'), E::tryFrom('xq'), E::tryFrom('zz'));
var_dump(E::cases());
enum F: int {
    case A = 1 + 1;
    case B = 2 * 2;
    case C = 7;
}
var_dump(F::A->value, F::from(4), F::C->value);
enum G: int {
    case A = 1;
    case B = 0 + 1;
}
var_dump(count(G::cases()));
try {
    var_dump(G::A);
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    var_dump(G::tryFrom(1));
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
enum H: string {
    case A = 'h';
    case B = I::A . 1 + 0;
}
try {
    var_dump(H::A);
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
enum J: string {
    case A = UNDEF_C;
    case B = 'b';
}
try {
    var_dump(J::cases());
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    var_dump(J::B);
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    var_dump(J::from('b'));
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
// A constant defined after the declaration, before the first use, is seen.
enum K: string {
    case A = LATE;
}
define('LATE', 'late');
var_dump(K::A->value);
// A failed evaluation is tried again at the next use.
enum L: string {
    case A = LATER;
}
try {
    var_dump(L::A);
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
define('LATER', 'now');
var_dump(L::A->value);
enum A2: string {
    case B = 'C';
}
enum D: string {
    case E = A2::B->name;
    case F = A2::B->value;
}
var_dump(D::E->value, D::F->value);
class Holder {
    const V = F::A;
}
var_dump(Holder::V);
// The first fault in declaration order, with the expression's value known:
// A and B collide before A and C do.
enum S: int {
    case A = 1;
    case B = 1 + 0;
    case C = 1;
}
try {
    var_dump(S::A);
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
// Values the compiler cannot fold are validated when evaluated: a duplicate
// and a type mismatch, the first in declaration order winning, and cases()
// exempt from both.
define('RUNTIME_ONE', 1);
define('RUNTIME_A', 'a');
enum T: int {
    case A = 1;
    case B = RUNTIME_ONE;
    case C = 1;
}
var_dump(count(T::cases()));
try {
    var_dump(T::C);
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    var_dump(T::from(1));
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
enum U: int {
    case A = RUNTIME_A;
    case B = 2;
    case C = 2;
}
try {
    var_dump(U::B);
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
