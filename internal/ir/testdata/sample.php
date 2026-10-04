<?php
function total(int $a, int $b = 2): int { return $a + $b; }

final class Cart {
    private array $items = [];
    public function add(string $sku, int $qty): void { $this->items[$sku] = $qty; }
}

$sum = total(6, 7);
echo $sum;
