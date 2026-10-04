<?php
class H {
    public int $backed = 1 { get { return $this->backed * 2; } set { $this->backed = $value + 1; } }
    public int $virt { get { return 42; } }
    public int $getOnly = 3 { get => $this->getOnly + 10; }
}
$h = new H();
echo $h->backed, "\n";
$h->backed = 5;
echo $h->backed, "\n";
echo $h->virt, "\n";
echo $h->getOnly, "\n";
var_dump($h);
