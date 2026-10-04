//go:build cgo && linux

package libc

import "testing"

func TestCrypt(t *testing.T) {
	cases := []struct {
		name     string
		password string
		salt     string
		want     string
	}{
		{"des", "password", "rl", "rl0uE0e2WKB0."},
		{"extended-des", "password", "_J9..rasm", "_J9..rasmEedsvB6g8/6"},
		{"md5", "password", "$1$salt$", "$1$salt$qJH7.N4xYta3aEG/dfqo/0"},
		{"sha256", "password", "$5$salt$", "$5$salt$Gcm6FsVtF/Qa77ZKD.iwsJlCVPY0XSMgLJL0Hnww/c1"},
		{"sha512", "password", "$6$salt$", "$6$salt$IxDD3jeSOb5eB1CX5LBsqZFVkJdido3OUILO5Ifz5iwMuTS4XMS130MTSuDDl3aCI6WouIL9AjRbLCelDCy.g."},
		{"bcrypt", "password", "$2y$04$abcdefghijklmnopqrstuu", "$2y$04$abcdefghijklmnopqrstuughE8Ev8uGFaUgY2cNEySvxngrb/Jzdm"},
		{"invalid", "password", "$9$salt$", "*0"},
		{"failure-marker", "password", "*0", "*1"},
		{"nul-password", "pass\x00tail", "$6$salt$", "$6$salt$3aEJgflnzWuw1O3tr0IYSmhUY0cZ7iBQeBP392T7RXjLP3TKKu3ddIapQaCpbD4p9ioeGaVIjOHaym7HvCuUm0"},
		{"nul-salt", "password", "rl\x00tail", "rl0uE0e2WKB0."},
		{"long-salt", "password", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "aajfMKNH1hTm2"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, available := Crypt(test.password, test.salt)
			if !available {
				t.Fatal("Crypt reported unavailable with cgo on Linux")
			}
			if got != test.want {
				t.Fatalf("Crypt(%q, %q) = %q, want %q", test.password, test.salt, got, test.want)
			}
		})
	}
}
