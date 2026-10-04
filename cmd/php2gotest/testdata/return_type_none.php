<?php function t($f) { try { $f(); echo "no error\n"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; } }
function a(): int {} function b(): mixed {} function c(): void {} function d(): ?int {} function e2() {}
function g(): ?int { return null; } function h(): int { return 5; }
t("a"); t("b"); t("c"); t("d"); t("e2"); var_dump(g()); var_dump(h());