<?php
// From inside a class that declares a property private, on an instance of a
// subclass that redeclares the name with hooks, the name is the class's own
// private: its hooks run, or none do. A public (or protected) property is one
// property, so the subclass's hooks run from anywhere.

class A {
    private $plain = "A's own";
    private $hooked {
        get { return "A::hooked::get"; }
        set { echo "A::hooked::set\n"; }
    }
    public $pub = "base";
    protected $prot = "base";

    public function probe() {
        var_dump($this->plain, $this->hooked, $this->pub, $this->prot);
        $this->hooked = 1;
        $this->pub = 2;
        $this->prot = 3;
    }
}

class B extends A {
    public $plain { get { return "B::plain::get"; } set { echo "B::plain::set\n"; } }
    public $hooked { get { return "B::hooked::get"; } set { echo "B::hooked::set\n"; } }
    public $pub { get { return "B::pub::get"; } set { echo "B::pub::set\n"; } }
    protected $prot { get { return "B::prot::get"; } set { echo "B::prot::set\n"; } }
}

(new A)->probe();
echo "--\n";
$b = new B;
$b->probe();
echo "--\n";
var_dump($b->plain, $b->hooked, $b->pub);
$b->hooked = 1;
