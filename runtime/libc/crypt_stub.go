//go:build !cgo || !linux

package libc

func Crypt(string, string) (string, bool) { return "", false }
