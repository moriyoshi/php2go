//go:build cgo && !windows

package libc

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

func TestNiceZero(t *testing.T) {
	if errorNumber, available := Nice(0); !available || errorNumber != 0 {
		t.Fatalf("Nice(0) = errno %d, available %v; want 0, true",
			errorNumber, available)
	}
}

func TestMailCommand(t *testing.T) {
	output := t.TempDir() + "/mail"
	status, errorNumber, started, available := MailCommand(
		fmt.Sprintf("cat > %q", output),
		"To: test@example.test\r\n\r\nbinary\x00body\r\n",
	)
	if !available || !started || errorNumber != 0 || status != 0 {
		t.Fatalf("MailCommand = status %d, errno %d, started %v, available %v",
			status, errorNumber, started, available)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	want := "To: test@example.test\r\n\r\nbinary\x00body\r\n"
	if string(got) != want {
		t.Fatalf("mail payload = %q, want %q", got, want)
	}
}

func TestShellCommand(t *testing.T) {
	output, status, errorNumber, started, available := ShellCommand(
		"printf 'a\\nb\\000'; exit 7",
	)
	if !available || !started || errorNumber != 0 {
		t.Fatalf("ShellCommand unavailable or failed to start: available=%v started=%v errno=%d",
			available, started, errorNumber)
	}
	if output != "a\nb\x00" || status != 7 {
		t.Fatalf("ShellCommand output/status = %q, %d; want binary output and 7", output, status)
	}
}

func TestPipeLifecycle(t *testing.T) {
	pipe, errorNumber, available := OpenPipe("printf 'pipe\\000data'; exit 9", "r")
	if !available || pipe == nil || errorNumber != 0 {
		t.Fatalf("OpenPipe unavailable or failed: available=%v pipe=%v errno=%d",
			available, pipe, errorNumber)
	}
	buffer := make([]byte, 32)
	n, err := syscall.Read(pipe.FD(), buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:n]); got != "pipe\x00data" {
		t.Fatalf("pipe output = %q, want binary payload", got)
	}
	status, errorNumber, available := pipe.Close()
	if !available || errorNumber != 0 || status != 9 {
		t.Fatalf("Pipe.Close = status %d, errno %d, available %v; want 9, 0, true",
			status, errorNumber, available)
	}
	status, errorNumber, available = pipe.Close()
	if !available || errorNumber != 0 || status != 9 {
		t.Fatalf("second Pipe.Close = status %d, errno %d, available %v; want idempotent 9",
			status, errorNumber, available)
	}
}
