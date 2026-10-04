<?php
$choice = getenv('P2G_EVAL_CHOICE') ?: 'A';
eval('echo ' . var_export($choice, true) . ';');
