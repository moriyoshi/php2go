//go:build cgo && !windows

package libc

import "testing"

func TestLocaleState(t *testing.T) {
	original, ok, available := SetLocale(6, "0", true)
	if !available || !ok || original == "" {
		t.Fatalf("query locale = %q, %v, %v", original, ok, available)
	}
	defer func() {
		if restored, ok, available := SetLocale(6, original, false); !available || !ok {
			t.Errorf("restore locale = %q, %v, %v", restored, ok, available)
		}
	}()

	if changed, ok, available := SetLocale(6, "C", false); !available || !ok || changed != "C" {
		t.Fatalf("SetLocale(C) = %q, %v, %v", changed, ok, available)
	}
	if queried, ok, available := SetLocale(6, "ignored", true); !available || !ok || queried != "C" {
		t.Fatalf("query C locale = %q, %v, %v", queried, ok, available)
	}
	if _, ok, available := SetLocale(999, "C", false); !available || ok {
		t.Fatalf("SetLocale(invalid category) ok = %v, available = %v", ok, available)
	}
	if changed, ok, available := SetLocale(6, "C\x00ignored", false); !available || !ok || changed != "C" {
		t.Fatalf("SetLocale(NUL) = %q, %v, %v", changed, ok, available)
	}

	convention, available := LocaleConv()
	if !available || convention.DecimalPoint != "." ||
		convention.ThousandsSep != "" || convention.IntFracDigits != 255 {
		t.Fatalf("C LocaleConv() = %#v, %v", convention, available)
	}
	day, available := NlLangInfo(131079)
	if !available || day != "Sunday" {
		t.Fatalf("C NlLangInfo(DAY_1) = %q, %v", day, available)
	}

	if changed, ok, _ := SetLocale(6, "de_DE.utf8", false); ok {
		if changed != "de_DE.utf8" {
			t.Fatalf("SetLocale(de_DE) = %q", changed)
		}
		convention, available = LocaleConv()
		if !available || convention.DecimalPoint != "," ||
			len(convention.Grouping) != 1 || convention.Grouping[0] != 3 {
			t.Fatalf("de_DE LocaleConv() = %#v, %v", convention, available)
		}
		day, available = NlLangInfo(131079)
		if !available || day != "Sonntag" {
			t.Fatalf("de_DE NlLangInfo(DAY_1) = %q, %v", day, available)
		}
	}
}

func TestStrptime(t *testing.T) {
	parsed, ok := Strptime("2006-08-20 tail", "%Y-%m-%d")
	if !ok {
		t.Fatal("Strptime rejected a complete date")
	}
	if parsed.Second != 0 || parsed.Minute != 0 || parsed.Hour != 0 ||
		parsed.MonthDay != 20 || parsed.Month != 7 || parsed.Year != 106 ||
		parsed.WeekDay != 0 || parsed.YearDay != 231 ||
		parsed.Unparsed != " tail" {
		t.Fatalf("Strptime date = %#v", parsed)
	}

	empty, ok := Strptime("abc", "")
	if !ok || empty.Unparsed != "abc" {
		t.Fatalf("Strptime empty format = %#v, %v", empty, ok)
	}
	if _, ok := Strptime("not-a-year", "%Y"); ok {
		t.Fatal("Strptime accepted an invalid year")
	}

	nul, ok := Strptime("2001\x00tail", "%Y\x00ignored")
	if !ok || nul.Year != 101 || nul.Unparsed != "" {
		t.Fatalf("Strptime NUL truncation = %#v, %v", nul, ok)
	}
}

func TestStrcoll(t *testing.T) {
	equal, ok := Strcoll("same", "same")
	if !ok || equal != 0 {
		t.Fatalf("Strcoll equal = %d, %v", equal, ok)
	}

	forward, ok := Strcoll("a", "b")
	if !ok || forward >= 0 {
		t.Fatalf("Strcoll forward = %d, %v", forward, ok)
	}
	reverse, ok := Strcoll("b", "a")
	if !ok || reverse <= 0 {
		t.Fatalf("Strcoll reverse = %d, %v", reverse, ok)
	}

	nul, ok := Strcoll("prefix\x00left", "prefix\x00right")
	if !ok || nul != 0 {
		t.Fatalf("Strcoll NUL truncation = %d, %v", nul, ok)
	}
}
