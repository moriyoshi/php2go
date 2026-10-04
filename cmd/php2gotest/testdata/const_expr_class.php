<?php
// A class constant named inside a *deferred* initializer. The engine keeps such
// an initializer as an AST, and the class and member sit in the node's
// children -- not in a name field, which is empty for this node kind. Reading
// the empty field produced a well-formed call that evaluated to NULL, so this
// fixture asserts values rather than the absence of an error.

enum E
{
    public const E SELF_CASE = E::Foo;
    case Foo;
    case Bar;
}

class Base
{
    const string GREETING = 'hello';
    const int N = 2;
}

class Derived extends Base
{
    const string FROM_PARENT = parent::GREETING;
    const string FROM_SELF = self::GREETING;
    const int DOUBLED = self::N * 2;
    const E CASE_FROM_ENUM = E::Bar;
    const E CASE_VIA_CONST = E::SELF_CASE;
}

define('LATE', 'defined after the class');

class Late
{
    const string L = LATE;
    const string COMBINED = LATE . '/' . Base::GREETING;
}

var_dump(Derived::FROM_PARENT, Derived::FROM_SELF, Derived::DOUBLED);
var_dump(Derived::CASE_FROM_ENUM, Derived::CASE_VIA_CONST, E::SELF_CASE);
var_dump(Late::L, Late::COMBINED);

// Read twice: a deferred initializer is evaluated once and cached, and a
// second read must give the same answer rather than re-running into a hole.
var_dump(Derived::CASE_FROM_ENUM, Late::COMBINED);
