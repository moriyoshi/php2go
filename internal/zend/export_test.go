package zend

// DanglingClassNameCaches reports how many interned class names still cache a
// class entry the class table no longer holds. Harvesting must leave it at
// zero: zend_lookup_class_ex answers from that cache before it consults the
// table, so a surviving slot hands the next file's compiler freed memory.
//
// Exported for the tests only. It walks the class table once per cached name,
// which is fine for a fixture and not for a harvest.
func DanglingClassNameCaches() int { return danglingClassNameCaches() }
