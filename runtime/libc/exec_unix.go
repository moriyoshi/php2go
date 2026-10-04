//go:build cgo && !windows

package libc

/*
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/wait.h>
#include <unistd.h>

static int php2go_nice(int increment) {
	errno = 0;
	(void)nice(increment);
	return errno;
}

// php2go_shell_command follows ext/standard's popen("r") boundary and copies
// stdout before pclose. The caller owns the returned binary buffer.
static char *php2go_shell_command(
	const char *command, size_t *length, int *status, int *error_number
) {
	*length = 0;
	*status = -1;
	*error_number = 0;
	errno = 0;
	FILE *pipe = popen(command, "r");
	if (pipe == NULL) {
		*error_number = errno;
		return NULL;
	}

	size_t capacity = 4096;
	char *output = malloc(capacity);
	if (output == NULL) {
		*error_number = ENOMEM;
		pclose(pipe);
		return NULL;
	}
	for (;;) {
		if (*length == capacity) {
			size_t next_capacity = capacity + 4096;
			char *next = realloc(output, next_capacity);
			if (next == NULL) {
				free(output);
				*error_number = ENOMEM;
				pclose(pipe);
				return NULL;
			}
			output = next;
			capacity = next_capacity;
		}
		size_t read = fread(output + *length, 1, capacity - *length, pipe);
		*length += read;
		if (read == 0) {
			break;
		}
	}

	int result = pclose(pipe);
	if (result != -1 && WIFEXITED(result)) {
		result = WEXITSTATUS(result);
	}
	*status = result;
	return output;
}

static FILE *php2go_pipe_open(
	const char *command, const char *mode, int *descriptor, int *error_number
) {
	errno = 0;
	FILE *pipe = popen(command, mode);
	if (pipe == NULL) {
		*descriptor = -1;
		*error_number = errno;
		return NULL;
	}
	*descriptor = fileno(pipe);
	*error_number = 0;
	return pipe;
}

static int php2go_pipe_close(FILE *pipe, int *error_number) {
	errno = 0;
	int result = pclose(pipe);
	*error_number = errno;
	if (result != -1 && WIFEXITED(result)) {
		result = WEXITSTATUS(result);
	}
	return result;
}

static int php2go_mail_command(
	const char *command, const char *payload, size_t length,
	int *status, int *error_number
) {
	*status = -1;
	*error_number = 0;
	errno = 0;
	FILE *pipe = popen(command, "w");
	if (pipe == NULL) {
		*error_number = errno;
		return 0;
	}
	int open_error = errno;
	if (length != 0) {
		(void)fwrite(payload, 1, length, pipe);
	}
	int result = pclose(pipe);
	*error_number = open_error;
	if (result != -1 && WIFEXITED(result)) {
		result = WEXITSTATUS(result);
	}
	*status = result;
	return 1;
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

// Nice changes the calling process's scheduling priority through libc nice(3).
// errorNumber is the errno php-src uses to distinguish a valid -1 result from
// failure; available distinguishes builds without this native boundary.
func Nice(increment int64) (errorNumber int, available bool) {
	return int(C.php2go_nice(C.int(increment))), true
}

// MailCommand writes one binary payload to a libc popen(3) writer and returns
// php-src's normalized pclose(3) status. The public mail facade owns header
// construction and status policy; this leaf owns only the native pipe.
func MailCommand(command, payload string) (
	status, errorNumber int, started, available bool,
) {
	commandText := C.CString(command)
	if commandText == nil {
		return -1, int(C.ENOMEM), false, true
	}
	defer C.free(unsafe.Pointer(commandText))
	payloadBytes := C.CBytes([]byte(payload))
	if payloadBytes == nil && payload != "" {
		return -1, int(C.ENOMEM), false, true
	}
	defer C.free(payloadBytes)
	var nativeStatus C.int
	var nativeError C.int
	startedNative := C.php2go_mail_command(
		commandText,
		(*C.char)(payloadBytes),
		C.size_t(len(payload)),
		&nativeStatus,
		&nativeError,
	)
	return int(nativeStatus), int(nativeError), startedNative != 0, true
}

// Pipe owns the FILE pointer returned by libc popen(3). Reads and writes use
// its descriptor, but only pclose(3) may release it and reap the child.
type Pipe struct {
	mu         sync.Mutex
	file       unsafe.Pointer
	descriptor int
	status     int
	closed     bool
}

// OpenPipe starts a live libc pipe. available distinguishes the non-native
// build stub; errorNumber is errno when libc refuses the operation.
func OpenPipe(command, mode string) (*Pipe, int, bool) {
	commandText := C.CString(command)
	if commandText == nil {
		return nil, int(C.ENOMEM), true
	}
	defer C.free(unsafe.Pointer(commandText))
	modeText := C.CString(mode)
	if modeText == nil {
		return nil, int(C.ENOMEM), true
	}
	defer C.free(unsafe.Pointer(modeText))
	var descriptor C.int
	var errorNumber C.int
	file := C.php2go_pipe_open(commandText, modeText, &descriptor, &errorNumber)
	if file == nil {
		return nil, int(errorNumber), true
	}
	return &Pipe{
		file:       unsafe.Pointer(file),
		descriptor: int(descriptor),
		status:     -1,
	}, 0, true
}

// FD returns the descriptor backing the pipe, or -1 after close.
func (p *Pipe) FD() int {
	if p == nil {
		return -1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return -1
	}
	return p.descriptor
}

// Close releases the pipe through pclose(3) and returns php-src's normalized
// wait status. It is idempotent so request cleanup can revisit an explicit
// fclose/pclose safely.
func (p *Pipe) Close() (status, errorNumber int, available bool) {
	if p == nil {
		return -1, 0, true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return p.status, 0, true
	}
	var nativeError C.int
	p.status = int(C.php2go_pipe_close((*C.FILE)(p.file), &nativeError))
	p.closed = true
	p.file = nil
	p.descriptor = -1
	return p.status, int(nativeError), true
}

// ShellCommand executes command through libc popen(3), captures binary stdout,
// and returns php-src's normalized pclose status. available distinguishes a
// missing native leg; started distinguishes popen/allocation failure.
func ShellCommand(command string) (
	output string, status, errorNumber int, started, available bool,
) {
	commandText := C.CString(command)
	if commandText == nil {
		return "", -1, int(C.ENOMEM), false, true
	}
	defer C.free(unsafe.Pointer(commandText))
	var length C.size_t
	var nativeStatus C.int
	var nativeError C.int
	result := C.php2go_shell_command(commandText, &length, &nativeStatus, &nativeError)
	if result == nil {
		return "", int(nativeStatus), int(nativeError), false, true
	}
	defer C.free(unsafe.Pointer(result))
	output = string(unsafe.Slice((*byte)(unsafe.Pointer(result)), int(length)))
	return output, int(nativeStatus), 0, true, true
}
