//go:build cgo && !windows

package libc

/*
#include <errno.h>
#include <dirent.h>
#include <fcntl.h>
#include <grp.h>
#include <limits.h>
#include <pwd.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>
#include <utime.h>

static void php2go_free_directory(char **names, size_t count) {
	if (names == NULL) {
		return;
	}
	for (size_t index = 0; index < count; index++) {
		free(names[index]);
	}
	free(names);
}

static char **php2go_read_directory(
	const char *path, size_t *count, int *error_number
) {
	*count = 0;
	*error_number = 0;
	DIR *directory = opendir(path);
	if (directory == NULL) {
		*error_number = errno;
		return NULL;
	}
	size_t capacity = 16;
	char **names = calloc(capacity, sizeof(char *));
	if (names == NULL) {
		closedir(directory);
		*error_number = ENOMEM;
		return NULL;
	}
	errno = 0;
	struct dirent *entry;
	while ((entry = readdir(directory)) != NULL) {
		if (*count == capacity) {
			capacity *= 2;
			char **grown = realloc(names, capacity * sizeof(char *));
			if (grown == NULL) {
				php2go_free_directory(names, *count);
				closedir(directory);
				*count = 0;
				*error_number = ENOMEM;
				return NULL;
			}
			names = grown;
		}
		size_t length = strlen(entry->d_name);
		names[*count] = malloc(length + 1);
		if (names[*count] == NULL) {
			php2go_free_directory(names, *count);
			closedir(directory);
			*count = 0;
			*error_number = ENOMEM;
			return NULL;
		}
		memcpy(names[*count], entry->d_name, length + 1);
		(*count)++;
	}
	if (errno != 0) {
		*error_number = errno;
		php2go_free_directory(names, *count);
		closedir(directory);
		*count = 0;
		return NULL;
	}
	closedir(directory);
	return names;
}

static DIR *php2go_open_directory(const char *path, int *error_number) {
	DIR *directory = opendir(path);
	*error_number = directory == NULL ? errno : 0;
	return directory;
}

static char *php2go_directory_read(DIR *directory, int *error_number) {
	errno = 0;
	struct dirent *entry = readdir(directory);
	if (entry == NULL) {
		*error_number = errno;
		return NULL;
	}
	size_t length = strlen(entry->d_name);
	char *name = malloc(length + 1);
	if (name == NULL) {
		*error_number = ENOMEM;
		return NULL;
	}
	memcpy(name, entry->d_name, length + 1);
	*error_number = 0;
	return name;
}

static int php2go_chmod(const char *path, int64_t mode) {
	if (chmod(path, (mode_t) mode) == 0) {
		return 0;
	}
	return errno;
}

static int php2go_chdir(const char *path) {
	return chdir(path) == 0 ? 0 : errno;
}

static char *php2go_realpath(const char *path, int *error_number) {
	errno = 0;
	char *resolved = realpath(path, NULL);
	*error_number = resolved == NULL ? errno : 0;
	return resolved;
}

static int php2go_mkdir(const char *path, int64_t mode) {
	return mkdir(path, (mode_t) mode) == 0 ? 0 : errno;
}

static int php2go_rmdir(const char *path) {
	return rmdir(path) == 0 ? 0 : errno;
}

static int php2go_link(const char *target, const char *link_path) {
	return link(target, link_path) == 0 ? 0 : errno;
}

static int php2go_symlink(const char *target, const char *link_path) {
	return symlink(target, link_path) == 0 ? 0 : errno;
}

static int php2go_temporary_file(
	const char *directory,
	const char *prefix,
	uint64_t random_value,
	char **opened_path,
	int *error_number
) {
	static const char alphabet[] = "0123456789abcdefghijklmnopqrstuv";
	*opened_path = NULL;
	*error_number = 0;

	char *resolved = realpath(directory, NULL);
	if (resolved == NULL) {
		*error_number = errno;
		return -1;
	}
	size_t directory_length = strlen(resolved);
	size_t prefix_length = strlen(prefix);
	size_t slash_length =
		directory_length > 0 && resolved[directory_length - 1] == '/' ? 0 : 1;
	size_t path_length =
		directory_length + slash_length + prefix_length + 13 + 6;
	if (path_length + 1 > PATH_MAX) {
		free(resolved);
		*error_number = ENAMETOOLONG;
		return -1;
	}

	char *path = malloc(path_length + 1);
	if (path == NULL) {
		free(resolved);
		*error_number = ENOMEM;
		return -1;
	}
	char *cursor = path;
	memcpy(cursor, resolved, directory_length);
	cursor += directory_length;
	if (slash_length != 0) {
		*cursor++ = '/';
	}
	memcpy(cursor, prefix, prefix_length);
	cursor += prefix_length;
	for (size_t index = 0; index < 13; index++) {
		*cursor++ = alphabet[random_value % 32];
		random_value /= 32;
	}
	memcpy(cursor, "XXXXXX", 7);
	free(resolved);

	int descriptor = mkstemp(path);
	if (descriptor < 0) {
		*error_number = errno;
		free(path);
		return -1;
	}
	*opened_path = path;
	return descriptor;
}

static int php2go_rename(
	const char *from, const char *to, int *source_directory, int *warning_number
) {
	*source_directory = 0;
	*warning_number = 0;
	if (rename(from, to) == 0) {
		return 0;
	}
	if (errno != EXDEV) {
		return errno;
	}

	struct stat source_stat;
	if (stat(from, &source_stat) != 0) {
		return errno;
	}
	if (S_ISDIR(source_stat.st_mode)) {
		*source_directory = 1;
		return EXDEV;
	}

	mode_t old_mask = umask(077);
	int source = open(from, O_RDONLY);
	if (source < 0) {
		int saved = errno;
		umask(old_mask);
		return saved;
	}
	int destination = open(to, O_WRONLY | O_CREAT | O_TRUNC, 0666);
	if (destination < 0) {
		int saved = errno;
		close(source);
		umask(old_mask);
		return saved;
	}

	int copy_error = 0;
	char buffer[8192];
	for (;;) {
		ssize_t received = read(source, buffer, sizeof(buffer));
		if (received == 0) {
			break;
		}
		if (received < 0) {
			if (errno == EINTR) {
				continue;
			}
			copy_error = errno;
			break;
		}
		ssize_t written = 0;
		while (written < received) {
			ssize_t count = write(
				destination, buffer + written, (size_t) (received - written)
			);
			if (count == 0) {
				copy_error = EIO;
				break;
			}
			if (count < 0) {
				if (errno == EINTR) {
					continue;
				}
				copy_error = errno;
				break;
			}
			written += count;
		}
		if (copy_error != 0) {
			break;
		}
	}
	if (close(source) != 0 && copy_error == 0) {
		copy_error = errno;
	}
	if (close(destination) != 0 && copy_error == 0) {
		copy_error = errno;
	}
	umask(old_mask);
	if (copy_error != 0) {
		return copy_error;
	}

	if (chown(to, source_stat.st_uid, source_stat.st_gid) != 0) {
		*warning_number = errno;
		if (errno != EPERM) {
			return errno;
		}
	}
	if (chmod(to, source_stat.st_mode) != 0) {
		if (*warning_number == 0) {
			*warning_number = errno;
		}
		if (errno != EPERM) {
			return errno;
		}
	}
	(void) unlink(from);
	return 0;
}

static int php2go_umask(int64_t mask, int set_mask) {
	mode_t old_mask = umask(077);
	umask(set_mask ? (mode_t) mask : old_mask);
	return (int) old_mask;
}

static int php2go_chown(
	const char *path, int64_t id, int change_group, int follow
) {
	uid_t uid = change_group ? (uid_t) -1 : (uid_t) id;
	gid_t gid = change_group ? (gid_t) id : (gid_t) -1;
	int result = follow ? chown(path, uid, gid) : lchown(path, uid, gid);
	return result == 0 ? 0 : errno;
}

static int php2go_user_id(const char *name, int64_t *id) {
	struct passwd *entry = getpwnam(name);
	if (entry == NULL) {
		return 0;
	}
	*id = (int64_t) entry->pw_uid;
	return 1;
}

static int php2go_group_id(const char *name, int64_t *id) {
	struct group *entry = getgrnam(name);
	if (entry == NULL) {
		return 0;
	}
	*id = (int64_t) entry->gr_gid;
	return 1;
}

static int php2go_touch(
	const char *path,
	int set_times,
	int64_t modification_time,
	int64_t access_time,
	int *stage
) {
	*stage = 0;
	if (access(path, F_OK) != 0) {
		FILE *file = fopen(path, "w");
		if (file == NULL) {
			*stage = 1;
			return errno;
		}
		fclose(file);
	}
	struct utimbuf times;
	struct utimbuf *times_pointer = NULL;
	if (set_times) {
		times.modtime = (time_t) modification_time;
		times.actime = (time_t) access_time;
		times_pointer = &times;
	}
	if (utime(path, times_pointer) != 0) {
		*stage = 2;
		return errno;
	}
	return 0;
}
*/
import "C"

import (
	"crypto/rand"
	"encoding/binary"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var filesystemProcessState sync.Mutex
var temporaryFallback atomic.Uint64

// TouchStage identifies which libc-backed part of touch failed.
type TouchStage int

const (
	TouchComplete TouchStage = iota
	TouchCreate
	TouchUtime
)

// Chdir changes libc's process working directory and returns errno directly.
// The lock serializes it with the other wrappers that temporarily mutate
// process-wide filesystem state.
func Chdir(path string) (int, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	filesystemProcessState.Lock()
	errorNumber := C.php2go_chdir(cpath)
	filesystemProcessState.Unlock()
	return int(errorNumber), true
}

// RealPath resolves an existing path through libc realpath(3). The allocated
// C result is copied before it is released, and errno is preserved separately
// because a nil pointer is the only failure indication from realpath itself.
func RealPath(path string) (string, int, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var errorNumber C.int
	filesystemProcessState.Lock()
	resolved := C.php2go_realpath(cpath, &errorNumber)
	filesystemProcessState.Unlock()
	if resolved == nil {
		return "", int(errorNumber), true
	}
	defer C.free(unsafe.Pointer(resolved))
	return C.GoString(resolved), 0, true
}

func Chmod(path string, mode int64) (int, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	return int(C.php2go_chmod(cpath, C.int64_t(mode))), true
}

func Mkdir(path string, mode int64) (int, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	return int(C.php2go_mkdir(cpath, C.int64_t(mode))), true
}

func Rmdir(path string) (int, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	return int(C.php2go_rmdir(cpath)), true
}

func Link(target, linkPath string) (int, bool) {
	ctarget := C.CString(target)
	defer C.free(unsafe.Pointer(ctarget))
	clink := C.CString(linkPath)
	defer C.free(unsafe.Pointer(clink))
	return int(C.php2go_link(ctarget, clink)), true
}

func Symlink(target, linkPath string) (int, bool) {
	ctarget := C.CString(target)
	defer C.free(unsafe.Pointer(ctarget))
	clink := C.CString(linkPath)
	defer C.free(unsafe.Pointer(clink))
	return int(C.php2go_symlink(ctarget, clink)), true
}

// TemporaryFile resolves directory and creates a mode-0600 file with mkstemp.
// The process umask remains authoritative, as it is for php-src's helper.
func TemporaryFile(directory, prefix string) (*os.File, string, int, bool) {
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		binary.LittleEndian.PutUint64(
			randomBytes[:],
			uint64(time.Now().UnixNano())^temporaryFallback.Add(1),
		)
	}
	cdirectory := C.CString(directory)
	defer C.free(unsafe.Pointer(cdirectory))
	cprefix := C.CString(prefix)
	defer C.free(unsafe.Pointer(cprefix))
	var openedPath *C.char
	var errorNumber C.int
	filesystemProcessState.Lock()
	descriptor := C.php2go_temporary_file(
		cdirectory,
		cprefix,
		C.uint64_t(binary.LittleEndian.Uint64(randomBytes[:])),
		&openedPath,
		&errorNumber,
	)
	filesystemProcessState.Unlock()
	if descriptor < 0 {
		return nil, "", int(errorNumber), true
	}
	defer C.free(unsafe.Pointer(openedPath))
	path := C.GoString(openedPath)
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		C.close(descriptor)
		return nil, "", int(C.EBADF), true
	}
	return file, path, 0, true
}

func Rename(from, to string) (int, bool, int, bool) {
	cfrom := C.CString(from)
	defer C.free(unsafe.Pointer(cfrom))
	cto := C.CString(to)
	defer C.free(unsafe.Pointer(cto))
	var sourceDirectory C.int
	var warningNumber C.int
	filesystemProcessState.Lock()
	errorNumber := C.php2go_rename(cfrom, cto, &sourceDirectory, &warningNumber)
	filesystemProcessState.Unlock()
	return int(errorNumber), sourceDirectory != 0, int(warningNumber), true
}

func Umask(mask int64, setMask bool) (int, bool) {
	set := C.int(0)
	if setMask {
		set = 1
	}
	filesystemProcessState.Lock()
	oldMask := C.php2go_umask(C.int64_t(mask), set)
	filesystemProcessState.Unlock()
	return int(oldMask), true
}

func ReadDirectory(path string) ([]string, int, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var count C.size_t
	var errorNumber C.int
	names := C.php2go_read_directory(cpath, &count, &errorNumber)
	if names == nil {
		return nil, int(errorNumber), true
	}
	defer C.php2go_free_directory(names, count)
	entries := unsafe.Slice((**C.char)(unsafe.Pointer(names)), int(count))
	result := make([]string, len(entries))
	for index, entry := range entries {
		result[index] = C.GoString(entry)
	}
	return result, 0, true
}

// Directory is a live libc DIR stream. Its finalizer is a leak guard; PHP's
// explicit closedir path calls Close deterministically.
type Directory struct {
	pointer *C.DIR
}

func OpenDirectory(path string) (*Directory, int, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var errorNumber C.int
	pointer := C.php2go_open_directory(cpath, &errorNumber)
	if pointer == nil {
		return nil, int(errorNumber), true
	}
	directory := &Directory{pointer: pointer}
	runtime.SetFinalizer(directory, (*Directory).Close)
	return directory, 0, true
}

func (directory *Directory) Read() (string, int, bool) {
	if directory == nil || directory.pointer == nil {
		return "", 0, false
	}
	var errorNumber C.int
	name := C.php2go_directory_read(directory.pointer, &errorNumber)
	if name == nil {
		return "", int(errorNumber), false
	}
	defer C.free(unsafe.Pointer(name))
	return C.GoString(name), 0, true
}

func (directory *Directory) Rewind() {
	if directory != nil && directory.pointer != nil {
		C.rewinddir(directory.pointer)
	}
}

func (directory *Directory) Close() {
	if directory == nil || directory.pointer == nil {
		return
	}
	runtime.SetFinalizer(directory, nil)
	C.closedir(directory.pointer)
	directory.pointer = nil
}

func Chown(path string, id int64, group, follow bool) (int, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	return int(C.php2go_chown(
		cpath,
		C.int64_t(id),
		C.int(boolInt(group)),
		C.int(boolInt(follow)),
	)), true
}

func UserID(name string) (int64, bool, bool) {
	databaseMu.Lock()
	defer databaseMu.Unlock()
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	var id C.int64_t
	found := C.php2go_user_id(cname, &id) != 0
	return int64(id), found, true
}

func GroupID(name string) (int64, bool, bool) {
	databaseMu.Lock()
	defer databaseMu.Unlock()
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	var id C.int64_t
	found := C.php2go_group_id(cname, &id) != 0
	return int64(id), found, true
}

func Touch(path string, setTimes bool, modificationTime, accessTime int64) (int, TouchStage, bool) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var stage C.int
	errorNumber := C.php2go_touch(
		cpath,
		C.int(boolInt(setTimes)),
		C.int64_t(modificationTime),
		C.int64_t(accessTime),
		&stage,
	)
	return int(errorNumber), TouchStage(stage), true
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
