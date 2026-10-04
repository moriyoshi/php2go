//go:build cgo && !windows

package libc

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestNetworkDatabases(t *testing.T) {
	if !Available() {
		t.Fatal("libc network databases are unavailable in a cgo build")
	}
	firstSeconds, firstMicroseconds, ok := UniqueIDTime()
	if !ok {
		t.Fatal("libc unique ID clock is unavailable in a cgo build")
	}
	secondSeconds, secondMicroseconds, ok := UniqueIDTime()
	if !ok || firstSeconds == secondSeconds && firstMicroseconds == secondMicroseconds {
		t.Fatalf("UniqueIDTime repeated (%d, %d)", secondSeconds, secondMicroseconds)
	}
	if firstMicroseconds > 999999 || secondMicroseconds > 999999 {
		t.Fatalf("UniqueIDTime microseconds = %d, %d", firstMicroseconds, secondMicroseconds)
	}
	if delta := int64(secondSeconds) - time.Now().Unix(); delta < -2 || delta > 2 {
		t.Fatalf("UniqueIDTime seconds = %d, want current Unix time", secondSeconds)
	}
	if got, ok := HostByName("localhost"); !ok || got != "127.0.0.1" {
		t.Fatalf("HostByName(localhost) = %q, %v", got, ok)
	}
	if got, ok := HostByNameList("127.0.0.1"); !ok ||
		len(got) != 1 || got[0] != "127.0.0.1" {
		t.Fatalf("HostByNameList(127.0.0.1) = %#v, %v", got, ok)
	}
	if got, ok := HostByAddress("127.0.0.1"); !ok || got == "" {
		t.Fatalf("HostByAddress(127.0.0.1) = %q, %v", got, ok)
	}
	if got, ok := ProtocolByName("tcp"); !ok || got != 6 {
		t.Fatalf("ProtocolByName(tcp) = %d, %v", got, ok)
	}
	if got, ok := ProtocolByNumber(4294967302); !ok || got != "tcp" {
		t.Fatalf("ProtocolByNumber(wrapped 6) = %q, %v", got, ok)
	}
	if got, ok := ServiceByName("http", "tcp"); !ok || got != 80 {
		t.Fatalf("ServiceByName(http, tcp) = %d, %v", got, ok)
	}
	if got, ok := ServiceByPort(65616, "tcp"); !ok || got != "http" {
		t.Fatalf("ServiceByPort(wrapped 80, tcp) = %q, %v", got, ok)
	}
	interfaces, errorNumber := Interfaces()
	if errorNumber != 0 {
		t.Fatalf("Interfaces() errno = %d", errorNumber)
	}
	if len(interfaces) == 0 {
		t.Fatal("Interfaces() returned an empty snapshot")
	}
	loopback := false
	for _, address := range interfaces {
		if address.Name == "" {
			t.Fatal("Interfaces() returned an entry without a name")
		}
		if address.Address != "" && !address.HasFamily {
			t.Fatalf("Interfaces() address without family: %#v", address)
		}
		if address.Family == 2 && address.Address == "127.0.0.1" {
			loopback = true
		}
	}
	if !loopback {
		t.Fatal("Interfaces() did not expose the IPv4 loopback address")
	}
}

func TestReadLink(t *testing.T) {
	directory := t.TempDir()
	link := filepath.Join(directory, "link")
	if err := os.Symlink("relative-target", link); err != nil {
		t.Fatal(err)
	}
	got, errorNumber, available := ReadLink(link)
	if !available || errorNumber != 0 || got != "relative-target" {
		t.Fatalf("ReadLink(link) = %q, %d, %v", got, errorNumber, available)
	}
	if _, errorNumber, available := ReadLink(directory); !available || errorNumber != int(syscall.EINVAL) {
		t.Fatalf("ReadLink(directory) errno = %d, available = %v", errorNumber, available)
	}
}
