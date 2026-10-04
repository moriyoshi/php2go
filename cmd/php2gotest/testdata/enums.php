<?php
enum Suit { case Hearts; case Spades; }
enum Level: int { case Low = 1; case High = 9; }
$h = Suit::Hearts;
var_dump($h);
var_dump($h->name);
var_dump($h === Suit::Hearts);
var_dump(Suit::cases());
$l = Level::High;
var_dump($l->name, $l->value);
var_dump(Level::from(1));
var_dump(Level::tryFrom(5));
var_dump($h instanceof Suit);
echo get_class($h), "\n";
try { Level::from(5); } catch (ValueError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
// A case is a singleton, so it survives a round trip through an array.
$all = Suit::cases();
var_dump($all[0] === Suit::Hearts);
// Enums can have methods and constants.
enum Colour: string {
    case Red = "r";
    case Blue = "b";
    public function label(): string { return $this->name . "/" . $this->value; }
}
echo Colour::Red->label(), "\n";
var_dump(Colour::Blue->value);
