<?php
function guarded(bool $early): int {
    try {
        if ($early) {
            return 1;
        }
        return 2;
    } finally {
        echo "done\n";
    }
}

class Guard
{
    public string $last = '';

    // The property store lives in the finally body, so every inlined copy of it
    // is an ASSIGN_OBJ spliced into a block it does not belong to -- along with
    // the OP_DATA carrying its value.
    public function run(bool $early): int
    {
        try {
            if ($early) {
                return 1;
            }
            return 2;
        } finally {
            $this->last = 'done';
        }
    }
}
