package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLIHelpAndErrors(t *testing.T) {
	tests := []struct {
		name, wantOut, wantErr string
		args                   []string
		code                   int
	}{
		{"top-level help", "Commands:", "", []string{"--help"}, 0},
		{"bundle help", "Usage: php2go bundle", "", []string{"bundle", "--help"}, 0},
		{"help subcommand", "Usage: php2go profile", "", []string{"help", "profile"}, 0},
		{"missing command", "", "Usage: php2go", nil, 2},
		{"unknown command", "", "unknown command", []string{"buldne"}, 2},
		{"missing bundle output", "", "bundle needs -o", []string{"bundle", "index.php"}, 1},
		{"incompatible dump flags", "", "-decode cannot be combined", []string{"dump", "-decode", "unit.pb", "-o", "other.pb"}, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := runCLI(test.args, &stdout, &stderr); code != test.code {
				t.Errorf("exit code = %d, want %d", code, test.code)
			}
			if !strings.Contains(stdout.String(), test.wantOut) || !strings.Contains(stderr.String(), test.wantErr) {
				t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}
