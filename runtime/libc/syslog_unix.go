//go:build cgo && !windows

package libc

/*
#include <stdlib.h>
#include <string.h>
#include <syslog.h>

// openlog(3) may retain the ident pointer, so the native leg owns a copy until
// the next open or close. Calls are serialized across the whole PHP request by
// the interpreter because libc's logger state is process-global.
static char *php2go_syslog_ident = NULL;

static int php2go_syslog_open(
	const char *ident, size_t length, int option, int facility
) {
	char *replacement = malloc(length + 1);
	if (replacement == NULL) {
		return 0;
	}
	memcpy(replacement, ident, length);
	replacement[length] = '\0';
	openlog(replacement, option, facility);
	free(php2go_syslog_ident);
	php2go_syslog_ident = replacement;
	return 1;
}

static void php2go_syslog_write(int priority, const char *message) {
	syslog(priority, "%s", message);
}

static void php2go_syslog_close(void) {
	closelog();
	free(php2go_syslog_ident);
	php2go_syslog_ident = NULL;
}
*/
import "C"

import "unsafe"

// SyslogOpen installs the process-global libc logger identity. The caller must
// serialize it with writes and close for the complete logical request.
func SyslogOpen(prefix string, flags, facility int64) bool {
	bytes := []byte(prefix)
	var data unsafe.Pointer
	if len(bytes) != 0 {
		data = C.CBytes(bytes)
		defer C.free(data)
	} else {
		data = C.malloc(1)
		if data == nil {
			return false
		}
		defer C.free(data)
	}
	return C.php2go_syslog_open(
		(*C.char)(data), C.size_t(len(bytes)), C.int(flags), C.int(facility),
	) != 0
}

// SyslogWrite sends one already-filtered message through libc syslog(3).
func SyslogWrite(priority int64, message string) bool {
	text := C.CString(message)
	if text == nil {
		return false
	}
	defer C.free(unsafe.Pointer(text))
	C.php2go_syslog_write(C.int(priority), text)
	return true
}

// SyslogClose releases libc's logger and the retained identity.
func SyslogClose() bool {
	C.php2go_syslog_close()
	return true
}
