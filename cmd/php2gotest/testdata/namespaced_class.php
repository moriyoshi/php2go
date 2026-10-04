<?php
namespace Foo;
if (false) { class Bar {} }
try { new Bar(); } catch (\Error $e) { echo $e->getMessage(), "\n"; }
