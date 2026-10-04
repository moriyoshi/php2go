//go:build cgo && linux

package libc

/*
#define _GNU_SOURCE
#cgo LDFLAGS: -lcrypt
#include <crypt.h>
#include <stdlib.h>
#include <string.h>

static char *php2go_crypt(const char *password, const char *salt) {
	struct crypt_data data;
	memset(&data, 0, sizeof(data));
	char *result = crypt_r(password, salt, &data);
	if (result == NULL) {
		return NULL;
	}
	size_t length = strlen(result);
	char *copy = malloc(length + 1);
	if (copy == NULL) {
		return NULL;
	}
	memcpy(copy, result, length + 1);
	return copy;
}
*/
import "C"

import (
	"strings"
	"unsafe"
)

const maxCryptSaltLength = 123

// Crypt applies the platform's reentrant crypt implementation after php-src's
// fixed salt truncation. The linked build's bundled implementation and the
// target libcrypt expose the same supported salt families; callers distinguish
// an unavailable native capability with available.
func Crypt(password, salt string) (result string, available bool) {
	if len(salt) > maxCryptSaltLength {
		salt = salt[:maxCryptSaltLength]
	}
	// php_crypt rejects these markers before entering any algorithm so that
	// the public function can return the opposite marker.
	if strings.HasPrefix(salt, "*0") {
		return "*1", true
	}

	cpassword := C.CString(password)
	defer C.free(unsafe.Pointer(cpassword))
	csalt := C.CString(salt)
	defer C.free(unsafe.Pointer(csalt))

	crypted := C.php2go_crypt(cpassword, csalt)
	if crypted == nil {
		return "*0", true
	}
	defer C.free(unsafe.Pointer(crypted))
	return C.GoString(crypted), true
}
