//go:build cgo && !windows

package libc

/*
#include <glob.h>
#include <fnmatch.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>

static void php2go_glob_free(char **paths, size_t count) {
	if (paths == NULL) {
		return;
	}
	for (size_t index = 0; index < count; index++) {
		free(paths[index]);
	}
	free(paths);
}

static int php2go_glob(
	const char *pattern, int flags, char ***paths, size_t *count
) {
	*paths = NULL;
	*count = 0;
	glob_t result;
	memset(&result, 0, sizeof(result));
	int status = glob(pattern, flags, NULL, &result);
#ifdef GLOB_NOMATCH
	if (status == GLOB_NOMATCH) {
		globfree(&result);
		return 0;
	}
#endif
	if (status != 0) {
		globfree(&result);
		return status;
	}
	if (result.gl_pathc == 0 || result.gl_pathv == NULL) {
		globfree(&result);
		return 0;
	}
	char **copied = calloc(result.gl_pathc, sizeof(char *));
	if (copied == NULL) {
		globfree(&result);
		return GLOB_NOSPACE;
	}
	for (size_t index = 0; index < result.gl_pathc; index++) {
		size_t length = strlen(result.gl_pathv[index]);
		copied[index] = malloc(length + 1);
		if (copied[index] == NULL) {
			php2go_glob_free(copied, index);
			globfree(&result);
			return GLOB_NOSPACE;
		}
		memcpy(copied[index], result.gl_pathv[index], length + 1);
	}
	*paths = copied;
	*count = result.gl_pathc;
	globfree(&result);
	return 0;
}

static int php2go_glob_brace(void) {
#ifdef GLOB_BRACE
	return GLOB_BRACE;
#else
	return 0;
#endif
}
static int php2go_glob_err(void) { return GLOB_ERR; }
static int php2go_glob_mark(void) { return GLOB_MARK; }
static int php2go_glob_nocheck(void) { return GLOB_NOCHECK; }
static int php2go_glob_noescape(void) { return GLOB_NOESCAPE; }
static int php2go_glob_nosort(void) { return GLOB_NOSORT; }
static int php2go_glob_onlydir(void) {
#ifdef GLOB_ONLYDIR
	return GLOB_ONLYDIR;
#else
	return 0;
#endif
}

static int php2go_glob_match(
	const char *pattern, const char *name, int no_escape
) {
	int flags = FNM_PERIOD;
#ifdef FNM_NOESCAPE
	if (no_escape) {
		flags |= FNM_NOESCAPE;
	}
#endif
	return fnmatch(pattern, name, flags) == 0;
}
*/
import "C"

import "unsafe"

type GlobFlags struct {
	Brace, Err, Mark, NoCheck, NoEscape, NoSort, OnlyDir int64
}

func PlatformGlobFlags() (GlobFlags, bool) {
	return GlobFlags{
		Brace:    int64(C.php2go_glob_brace()),
		Err:      int64(C.php2go_glob_err()),
		Mark:     int64(C.php2go_glob_mark()),
		NoCheck:  int64(C.php2go_glob_nocheck()),
		NoEscape: int64(C.php2go_glob_noescape()),
		NoSort:   int64(C.php2go_glob_nosort()),
		OnlyDir:  int64(C.php2go_glob_onlydir()),
	}, true
}

// Glob copies glob(3)'s path vector before releasing the native allocation.
// A no-match result is the successful empty slice PHP exposes; other native
// failures retain their status so the public facade can return false.
func Glob(pattern string, flags int64) ([]string, int, bool) {
	patternCString := C.CString(pattern)
	defer C.free(unsafe.Pointer(patternCString))
	var paths **C.char
	var count C.size_t
	status := C.php2go_glob(
		patternCString,
		C.int(flags),
		&paths,
		&count,
	)
	if paths != nil {
		defer C.php2go_glob_free(paths, count)
	}
	if status != 0 {
		return nil, int(status), true
	}
	result := make([]string, int(count))
	if count == 0 {
		return result, 0, true
	}
	native := unsafe.Slice(paths, int(count))
	for index, path := range native {
		result[index] = C.GoString(path)
	}
	return result, 0, true
}

func GlobMatch(pattern, name string, noEscape bool) (bool, bool) {
	patternCString := C.CString(pattern)
	defer C.free(unsafe.Pointer(patternCString))
	nameCString := C.CString(name)
	defer C.free(unsafe.Pointer(nameCString))
	flag := C.int(0)
	if noEscape {
		flag = 1
	}
	return C.php2go_glob_match(patternCString, nameCString, flag) != 0, true
}
