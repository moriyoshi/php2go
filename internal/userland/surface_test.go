package userland

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/internal/zend"
)

//go:embed surface.tsv
var surfaceSource string

func TestSurfaceClassification(t *testing.T) {
	statuses := map[string]string{}
	counts := map[string]int{}
	previous := ""
	for lineNumber, line := range strings.Split(strings.TrimSpace(surfaceSource), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			t.Fatalf("surface.tsv:%d: want name and disposition", lineNumber+1)
		}
		name, disposition := fields[0], fields[1]
		if name <= previous {
			t.Fatalf("surface.tsv:%d: %q is not sorted after %q", lineNumber+1, name, previous)
		}
		if disposition != "native" && disposition != "userland" &&
			disposition != "pending" && disposition != "unavailable" {
			t.Fatalf("surface.tsv:%d: unknown disposition %q", lineNumber+1, disposition)
		}
		statuses[name] = disposition
		counts[disposition]++
		previous = name
	}
	if len(statuses) != 549 {
		t.Fatalf("classified functions = %d, want 549; run go generate ./internal/userland", len(statuses))
	}

	catalogued := map[string]bool{}
	for _, library := range Libraries() {
		for _, function := range library.Functions {
			catalogued[function.Name] = true
			if statuses[function.Name] != "userland" {
				t.Errorf("catalog function %q has disposition %q, want userland", function.Name, statuses[function.Name])
			}
		}
	}
	if counts["userland"] != len(catalogued) {
		t.Errorf("surface has %d userland functions, catalog has %d", counts["userland"], len(catalogued))
	}
	if counts["unavailable"] != 15 {
		t.Errorf("surface has %d unavailable functions, want linked-build 15", counts["unavailable"])
	}
}

func TestSurfaceAvailabilityMatchesLinkedEngine(t *testing.T) {
	statuses := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(surfaceSource), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		statuses[fields[0]] = fields[1]
	}
	names := make([]string, 0, len(statuses))
	for name := range statuses {
		names = append(names, name)
	}
	sort.Strings(names)

	var source strings.Builder
	source.WriteString("foreach ([")
	for _, name := range names {
		fmt.Fprintf(&source, "%q,", name)
	}
	source.WriteString(`] as $name) { echo $name, "=", function_exists($name) ? "1" : "0", "\n"; }`)

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	output, err := engine.Eval(source.String())
	if err != nil {
		t.Fatal(err)
	}
	available := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		name, value, found := strings.Cut(line, "=")
		if !found {
			t.Fatalf("malformed engine availability line %q", line)
		}
		available[name] = value == "1"
	}
	for _, name := range names {
		want := statuses[name] != "unavailable"
		if available[name] != want {
			t.Errorf("%s availability = %v, disposition %s", name, available[name], statuses[name])
		}
	}
}
