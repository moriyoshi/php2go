//go:build cgo && !windows

package libc

import "testing"

func TestSyslogLifecycle(t *testing.T) {
	// LOG_NDELAY opens the native logger immediately, exercising the retained
	// identity boundary without emitting a test message into the host log.
	if !SyslogOpen("php2go-syslog-test\x00ignored", 0x08, 0x08) {
		t.Fatal("SyslogOpen reported unavailable in a cgo Unix build")
	}
	if !SyslogClose() {
		t.Fatal("SyslogClose reported unavailable in a cgo Unix build")
	}
	if !SyslogClose() {
		t.Fatal("repeated SyslogClose reported unavailable")
	}
}
