<?php
if (class_exists(B::class)) {
    echo "B is already here\n";
    return;
}
class B extends A {}
