<?php
// Reflection of library symbols: a library function (getClosure, invoke,
// the fixed answers), SensitiveParameterValue::$value, and
// ReflectionReference::fromArrayElement (probed).
$r = new ReflectionFunction('SUBSTR');
var_dump($r->getName(), $r->name, $r->getShortName(), $r->getNamespaceName(), $r->inNamespace(), $r->isInternal(), $r->isUserDefined(), $r->isClosure(), $r->getFileName(), $r->getStartLine(), $r->getEndLine(), $r->getDocComment(), $r->getAttributes(), $r->invoke("hello", 1, 2), $r->invokeArgs(["abc", 1]), $r->isDeprecated(), $r->isStatic());
echo get_class($r), " ", $r->name, "\n";
try { new ReflectionFunction('nope'); } catch (ReflectionException $e) { echo $e->getMessage(), "\n"; }
var_dump((new ReflectionFunction('sin'))->getClosure());
$x = (new ReflectionFunction("strtoupper"))->getClosure();
var_dump($x("abc"));
class a {}
var_dump($x->call(new a));
$v = new SensitiveParameterValue('secret');
$p = (new ReflectionClass($v))->getProperty('value');
var_dump($p->getValue($v), $p->name, $p->class, $p->isPrivate(), $p->isReadOnly(), $p->isPublic());
$q = new ReflectionProperty('SensitiveParameterValue', 'value'); var_dump($q->getValue($v));
try { $q->getValue(new stdClass); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo get_class($p), " ", $p->name, " ", $p->class, "\n";
$a = [1, 2]; $r = &$a[0];
var_dump(ReflectionReference::fromArrayElement($a, 1));
$x = ReflectionReference::fromArrayElement($a, 0); echo get_class($x), "\n";
unset($r); echo get_class(ReflectionReference::fromArrayElement($a, 0)), "\n";
try { ReflectionReference::fromArrayElement($a, 5); } catch (ReflectionException $e) { echo $e->getMessage(), "\n"; }
try { ReflectionReference::fromArrayElement("x", 0); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$b = ['k' => 1]; $y = &$b['k']; echo get_class(ReflectionReference::fromArrayElement($b, "k")), "\n";
try { var_dump(ReflectionReference::fromArrayElement(['1' => 5], "1")); } catch (ReflectionException $e) { echo $e->getMessage(), " at line ", $e->getLine(), "\n"; }
$ga=[1]; $gr=&$ga[0]; $b=[&$gr]; $x=ReflectionReference::fromArrayElement($ga,0); $y=ReflectionReference::fromArrayElement($b,0); var_dump(strlen($x->getId()), $x->getId()===$y->getId()); $c=[1]; $r2=&$c[0]; var_dump($x->getId()===ReflectionReference::fromArrayElement($c,0)->getId()); try { $x->getId(1);} catch(Throwable $e){echo get_class($e),": ",$e->getMessage(),"\n";}
