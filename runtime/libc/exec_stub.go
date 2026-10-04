//go:build !cgo || windows

package libc

type Pipe struct{}

func Nice(int64) (int, bool) { return 0, false }

func MailCommand(string, string) (int, int, bool, bool) {
	return -1, 0, false, false
}

func OpenPipe(string, string) (*Pipe, int, bool) { return nil, 0, false }

func (*Pipe) FD() int { return -1 }

func (*Pipe) Close() (int, int, bool) { return -1, 0, false }

func ShellCommand(string) (string, int, int, bool, bool) {
	return "", -1, 0, false, false
}
