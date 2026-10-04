//go:build !cgo || windows

package libc

func Available() bool { return false }

func UniqueIDTime() (uint32, uint32, bool) { return 0, 0, false }

func ReadLink(string) (string, int, bool) { return "", 0, false }

type InterfaceAddress struct {
	Name         string
	Flags        uint64
	Family       int
	HasFamily    bool
	Up           bool
	Address      string
	Netmask      string
	Broadcast    string
	PointToPoint string
}

func Interfaces() ([]InterfaceAddress, int) { return nil, 0 }

func HostByName(string) (string, bool) { return "", false }

func HostByNameList(string) ([]string, bool) { return nil, false }

func HostByAddress(string) (string, bool) { return "", false }

func ProtocolByName(string) (int64, bool) { return 0, false }

func ProtocolByNumber(int64) (string, bool) { return "", false }

func ServiceByName(string, string) (int64, bool) { return 0, false }

func ServiceByPort(int64, string) (string, bool) { return "", false }
