<?php
// A library function registered for shutdown and refusing its arguments
// raises with no active file.
register_shutdown_function('explode');
echo "end\n";
