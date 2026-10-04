package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"google.golang.org/protobuf/proto"
	"io/fs"
	"reflect"
	"syscall"
	"testing"
	"testing/fstest"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/interp"
)

func TestEmbeddedLoaderResolvesVirtualPaths(t *testing.T) {
	files := map[string]File{
		"/app/main.php":        {Path: "/app/main.php"},
		"/app/lib/value.php":   {Path: "/app/lib/value.php"},
		"/app/vendor/load.php": {Path: "/app/vendor/load.php"},
	}
	loader := &embeddedLoader{files: files, cwd: virtualRoot}
	for _, tc := range []struct {
		requested string
		from      string
		want      string
		found     bool
	}{
		{"value.php", "/app/lib/caller.php", "/app/lib/value.php", true},
		{"vendor/load.php", "/app/main.php", "/app/vendor/load.php", true},
		{"/app/lib/value.php", "/app/main.php", "/app/lib/value.php", true},
		{"../../outside.php", "/app/lib/caller.php", "", false},
		{"https://example.test/a.php", "/app/main.php", "", false},
	} {
		got, found := loader.ResolveFile(tc.requested, tc.from)
		if got != tc.want || found != tc.found {
			t.Errorf("ResolveFile(%q, %q) = %q, %v; want %q, %v",
				tc.requested, tc.from, got, found, tc.want, tc.found)
		}
	}
}

func TestEmbeddedLoaderChangesAndLeavesVirtualWorkingDirectory(t *testing.T) {
	loader := &embeddedLoader{cwd: virtualRoot, files: map[string]File{
		"/app/caller/value.php": {Path: "/app/caller/value.php"},
		"/app/work/value.php":   {Path: "/app/work/value.php"},
		"/app/work/plain":       {Path: "/app/work/plain"},
	}}
	if errorNumber, handled := loader.ChangeWorkingDirectory("work"); errorNumber != 0 || !handled {
		t.Fatalf("ChangeWorkingDirectory(work) = %d, %v", errorNumber, handled)
	}
	if directory, virtual := loader.WorkingDirectory(); directory != "/app/work" || !virtual {
		t.Fatalf("WorkingDirectory() = %q, %v", directory, virtual)
	}
	if got, found := loader.ResolveFile("value.php", "/app/caller/main.php"); got != "/app/work/value.php" || !found {
		t.Fatalf("ResolveFile(value.php) = %q, %v; want cwd copy", got, found)
	}
	if errorNumber, handled := loader.ChangeWorkingDirectory("plain"); errorNumber != int(syscall.ENOTDIR) || !handled {
		t.Fatalf("ChangeWorkingDirectory(plain) = %d, %v; want %d, true",
			errorNumber, handled, syscall.ENOTDIR)
	}
	if errorNumber, handled := loader.ChangeWorkingDirectory("missing"); errorNumber != int(syscall.ENOENT) || !handled {
		t.Fatalf("ChangeWorkingDirectory(missing) = %d, %v; want %d, true",
			errorNumber, handled, syscall.ENOENT)
	}

	loader.UseHostWorkingDirectory()
	if directory, virtual := loader.WorkingDirectory(); directory != "" || virtual {
		t.Fatalf("host WorkingDirectory() = %q, %v", directory, virtual)
	}
	if _, _, handled := loader.ResolvePath("value.php"); handled {
		t.Fatal("relative host path was claimed by virtual loader")
	}
	if errorNumber, handled := loader.ChangeWorkingDirectory("/tmp"); errorNumber != 0 || handled {
		t.Fatalf("ChangeWorkingDirectory(/tmp) = %d, %v; want host fallback", errorNumber, handled)
	}
	if errorNumber, handled := loader.ChangeWorkingDirectory("/app/work"); errorNumber != 0 || !handled {
		t.Fatalf("ChangeWorkingDirectory(/app/work) = %d, %v", errorNumber, handled)
	}
}

func TestEmbeddedLoaderResolvesFilesAndDerivedDirectories(t *testing.T) {
	loader := &embeddedLoader{cwd: virtualRoot, files: map[string]File{
		"/app/main.php":      {Path: "/app/main.php"},
		"/app/lib/value.php": {Path: "/app/lib/value.php"},
	}}
	for _, tc := range []struct {
		requested string
		want      string
		found     bool
		handled   bool
	}{
		{"", "/app", true, true},
		{"main.php", "/app/main.php", true, true},
		{"lib", "/app/lib", true, true},
		{"/app/lib/value.php", "/app/lib/value.php", true, true},
		{"missing", "/app/missing", false, true},
		{"../outside", "", false, true},
		{"/app/../outside", "", false, true},
		{"/tmp/host.php", "", false, false},
	} {
		got, found, handled := loader.ResolvePath(tc.requested)
		if got != tc.want || found != tc.found || handled != tc.handled {
			t.Errorf("ResolvePath(%q) = %q, %v, %v; want %q, %v, %v",
				tc.requested, got, found, handled, tc.want, tc.found, tc.handled)
		}
	}
}

func TestEmbeddedLoaderInspectsFilesAndDerivedDirectories(t *testing.T) {
	loader := &embeddedLoader{cwd: virtualRoot, files: map[string]File{
		"/app/main.php":      {Path: "/app/main.php"},
		"/app/lib/value.php": {Path: "/app/lib/value.php"},
	}}
	file := interp.PathStatus{Exists: true, File: true, Readable: true}
	directory := interp.PathStatus{
		Exists: true, Dir: true, Readable: true, Executable: true,
	}
	for _, tc := range []struct {
		requested string
		want      interp.PathStatus
		handled   bool
	}{
		{"", interp.PathStatus{}, true},
		{".", directory, true},
		{"main.php", file, true},
		{"lib", directory, true},
		{"/app/lib/value.php", file, true},
		{"missing", interp.PathStatus{}, true},
		{"../outside", interp.PathStatus{}, true},
		{"/app/../outside", interp.PathStatus{}, true},
		{"/tmp/host.php", interp.PathStatus{}, false},
	} {
		got, handled := loader.InspectPath(tc.requested)
		if got != tc.want || handled != tc.handled {
			t.Errorf("InspectPath(%q) = %+v, %v; want %+v, %v",
				tc.requested, got, handled, tc.want, tc.handled)
		}
	}
}

func TestEmbeddedLoaderStatsFilesAndDerivedDirectories(t *testing.T) {
	loader := &embeddedLoader{cwd: virtualRoot, files: map[string]File{
		"/app/main.php":      {Path: "/app/main.php", Size: 8},
		"/app/lib/value.php": {Path: "/app/lib/value.php", Size: 3},
	}}
	file := interp.PathStat{
		Dev: 12, Mode: 0o100444, NLink: 1, RDev: -1,
		Size: 8, BlockSize: -1, Blocks: -1,
	}
	directory := interp.PathStat{
		Dev: 12, Mode: 0o040555, NLink: 2, RDev: -1,
		BlockSize: -1, Blocks: -1,
	}
	for _, tc := range []struct {
		requested string
		follow    bool
		want      interp.PathStat
		found     bool
		handled   bool
	}{
		{"", true, interp.PathStat{}, false, true},
		{"main.php", true, file, true, true},
		{"main.php", false, file, true, true},
		{"lib", true, directory, true, true},
		{"missing", true, interp.PathStat{}, false, true},
		{"../outside", true, interp.PathStat{}, false, true},
		{"/tmp/host.php", true, interp.PathStat{}, false, false},
	} {
		got, found, handled := loader.StatPath(tc.requested, tc.follow)
		if got != tc.want || found != tc.found || handled != tc.handled {
			t.Errorf("StatPath(%q, %v) = %+v, %v, %v; want %+v, %v, %v",
				tc.requested, tc.follow, got, found, handled,
				tc.want, tc.found, tc.handled)
		}
	}
}

func TestEmbeddedLoaderReadsDerivedDirectories(t *testing.T) {
	loader := &embeddedLoader{cwd: virtualRoot, files: map[string]File{
		"/app/main.php":         {Path: "/app/main.php"},
		"/app/lib/a.php":        {Path: "/app/lib/a.php"},
		"/app/lib/nested/b.php": {Path: "/app/lib/nested/b.php"},
	}}
	for _, tc := range []struct {
		requested string
		want      []string
		wantErr   error
		handled   bool
	}{
		{".", []string{".", "..", "lib", "main.php"}, nil, true},
		{"lib", []string{".", "..", "a.php", "nested"}, nil, true},
		{"lib/a.php", nil, syscall.ENOTDIR, true},
		{"missing", nil, fs.ErrNotExist, true},
		{"/tmp/host", nil, nil, false},
	} {
		got, err, handled := loader.ReadDirectory(tc.requested)
		if !reflect.DeepEqual(got, tc.want) || !errors.Is(err, tc.wantErr) || handled != tc.handled {
			t.Errorf("ReadDirectory(%q) = %#v, %v, %v; want %#v, %v, %v",
				tc.requested, got, err, handled, tc.want, tc.wantErr, tc.handled)
		}
	}
}

func TestOpenAndSourceValidatePayload(t *testing.T) {
	source := []byte("<?php echo 42;")
	sourceHash := hashForTest(source)
	unitData, err := proto.Marshal(&pb.Unit{File: &pb.OpArray{Filename: "/app/main.php"}})
	if err != nil {
		t.Fatal(err)
	}
	unitHash := hashForTest(unitData)
	manifestData, err := proto.Marshal(&pb.Manifest{})
	if err != nil {
		t.Fatal(err)
	}
	indexData, err := json.Marshal(Index{
		Format: FormatVersion, Entry: "/app/main.php", Manifest: "manifest.pb",
		Files: []File{{
			Path: "/app/main.php", Source: "source/" + sourceHash,
			SourceSHA256: sourceHash, Size: int64(len(source)),
			Unit: "units/" + unitHash + ".pb", UnitSHA256: unitHash,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{
		IndexFile:                   {Data: indexData},
		"manifest.pb":               {Data: manifestData},
		"source/" + sourceHash:      {Data: source},
		"units/" + unitHash + ".pb": {Data: unitData},
	}
	app, err := Open(fsys)
	if err != nil {
		t.Fatal(err)
	}
	got, err := app.Source("main.php")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(source) {
		t.Fatalf("Source = %q, want %q", got, source)
	}

	fsys["source/"+sourceHash] = &fstest.MapFile{Data: []byte("tampered")}
	app, err = Open(fsys)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.Source("/app/main.php")
	if err == nil {
		t.Fatal("tampered source was accepted")
	}
}

func TestStaticBundleRejectsEvalCompilation(t *testing.T) {
	loader := &embeddedLoader{}
	_, err := loader.CompileString("echo 1;")
	var unsupported *interp.UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("CompileString error = %T %v, want UnsupportedError", err, err)
	}
}

func TestEmbeddedLoaderReadSourceDefersOutsideVirtualRoot(t *testing.T) {
	loader := &embeddedLoader{}
	if _, err := loader.ReadSource("/tmp/host.jpg"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadSource outside /app error = %v, want fs.ErrNotExist", err)
	}
	if _, err := loader.ReadSource("/app/../tmp/host.jpg"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadSource escaping /app error = %v, want invalid path", err)
	}
}

func TestOpenRejectsMissingEntry(t *testing.T) {
	indexData, err := json.Marshal(Index{
		Format: FormatVersion, Entry: "/app/main.php", Manifest: "manifest.pb",
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestData, err := proto.Marshal(&pb.Manifest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Open(fstest.MapFS{
		IndexFile:     {Data: indexData},
		"manifest.pb": {Data: manifestData},
	})
	if err == nil {
		t.Fatal("missing entry was accepted")
	}
}

func hashForTest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
