<?php
echo "before\n";
throw new RuntimeException("boom", 7);
