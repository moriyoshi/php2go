//go:build !cgo || windows

package libc

import "os"

type TouchStage int

const (
	TouchComplete TouchStage = iota
	TouchCreate
	TouchUtime
)

func Chmod(string, int64) (int, bool) { return 0, false }

func Chdir(string) (int, bool) { return 0, false }

func RealPath(string) (string, int, bool) { return "", 0, false }

func Mkdir(string, int64) (int, bool) { return 0, false }

func Rmdir(string) (int, bool) { return 0, false }

func Link(string, string) (int, bool) { return 0, false }

func Symlink(string, string) (int, bool) { return 0, false }

func TemporaryFile(string, string) (*os.File, string, int, bool) {
	return nil, "", 0, false
}

func Rename(string, string) (int, bool, int, bool) { return 0, false, 0, false }

func Umask(int64, bool) (int, bool) { return 0, false }

func ReadDirectory(string) ([]string, int, bool) { return nil, 0, false }

type Directory struct{}

func OpenDirectory(string) (*Directory, int, bool) { return nil, 0, false }

func (*Directory) Read() (string, int, bool) { return "", 0, false }

func (*Directory) Rewind() {}

func (*Directory) Close() {}

func Chown(string, int64, bool, bool) (int, bool) { return 0, false }

func UserID(string) (int64, bool, bool) { return 0, false, false }

func GroupID(string) (int64, bool, bool) { return 0, false, false }

func Touch(string, bool, int64, int64) (int, TouchStage, bool) {
	return 0, TouchComplete, false
}
