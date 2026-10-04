//go:build !cgo || windows

package libc

func SyslogOpen(string, int64, int64) bool { return false }

func SyslogWrite(int64, string) bool { return false }

func SyslogClose() bool { return false }
