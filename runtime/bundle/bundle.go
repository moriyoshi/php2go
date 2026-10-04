// Package bundle runs a PHP project whose sources and harvested units live in
// an fs.FS, normally one populated by go:embed.
//
// Producing a bundle needs the Zend compiler. Reading and running one does not:
// this package depends only on the serialized IR and the Go interpreter.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"google.golang.org/protobuf/proto"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/interp"
	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

const (
	// FormatVersion is the bundle index format understood by this runtime.
	FormatVersion = 2
	// IndexFile is the fixed name of the bundle index.
	IndexFile   = "index.json"
	virtualRoot = "/app"
)

// Index describes every source and compiled unit in a bundle. Slices rather
// than maps keep its JSON deterministic and readily inspectable.
type Index struct {
	Format   int               `json:"format"`
	Entry    string            `json:"entry"`
	Manifest string            `json:"manifest"`
	Files    []File            `json:"files"`
	Userland []UserlandLibrary `json:"userland,omitempty"`
}

// UserlandLibrary identifies a harvested PHP library and the engine functions
// whose bodies it supplies.
type UserlandLibrary struct {
	File         string             `json:"file"`
	Initializers []string           `json:"initializers,omitempty"`
	Functions    []UserlandFunction `json:"functions"`
}

// UserlandFunction maps an engine-visible name to a private PHP implementation.
type UserlandFunction struct {
	Name           string `json:"name"`
	Implementation string `json:"implementation"`
}

// File maps one stable virtual path to its original bytes and, for PHP source,
// the unit harvested from those bytes.
type File struct {
	Path         string `json:"path"`
	Source       string `json:"source"`
	SourceSHA256 string `json:"source_sha256"`
	Size         int64  `json:"size"`
	Unit         string `json:"unit,omitempty"`
	UnitSHA256   string `json:"unit_sha256,omitempty"`
}

// App is a validated embedded PHP application.
type App struct {
	fsys                fs.FS
	index               Index
	files               map[string]File
	manifest            *pb.Manifest
	persistentSockets   *stdlib.PersistentSocketPool
	evalCompilerFactory func(*pb.Manifest) (EvalCompiler, error)
	cwdMu               sync.Mutex
	cwd                 string
}

// EvalCompiler compiles an eval string during one App.Run request. The
// compiler is closed before that request returns.
type EvalCompiler interface {
	CompileString(code string) (*pb.Unit, error)
	Close()
}

// SetEvalCompilerFactory enables runtime compilation of eval strings. A
// factory creates a fresh compiler for each request, so the default bundle
// remains independent of Zend and repeated runs cannot reuse engine state.
func (a *App) SetEvalCompilerFactory(factory func(*pb.Manifest) (EvalCompiler, error)) {
	a.evalCompilerFactory = factory
}

// Open validates and opens a bundle rooted at fsys.
func Open(fsys fs.FS) (*App, error) {
	data, err := fs.ReadFile(fsys, IndexFile)
	if err != nil {
		return nil, fmt.Errorf("bundle: read %s: %w", IndexFile, err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("bundle: decode %s: %w", IndexFile, err)
	}
	if index.Format != FormatVersion {
		return nil, fmt.Errorf("bundle: format %d is not supported (want %d)", index.Format, FormatVersion)
	}
	if _, ok := cleanVirtual(index.Entry); !ok || index.Entry == virtualRoot {
		return nil, fmt.Errorf("bundle: invalid entry path %q", index.Entry)
	}
	if index.Manifest == "" {
		return nil, fmt.Errorf("bundle: manifest path is empty")
	}
	manifestData, err := fs.ReadFile(fsys, index.Manifest)
	if err != nil {
		return nil, fmt.Errorf("bundle: read manifest: %w", err)
	}
	manifest := &pb.Manifest{}
	if err := proto.Unmarshal(manifestData, manifest); err != nil {
		return nil, fmt.Errorf("bundle: decode manifest: %w", err)
	}

	app := &App{
		fsys: fsys, index: index, files: make(map[string]File, len(index.Files)),
		manifest:          manifest,
		persistentSockets: stdlib.NewPersistentSocketPool(),
		cwd:               virtualRoot,
	}
	for _, file := range index.Files {
		canonical, ok := cleanVirtual(file.Path)
		if !ok || canonical != file.Path || canonical == virtualRoot {
			return nil, fmt.Errorf("bundle: invalid file path %q", file.Path)
		}
		if _, exists := app.files[file.Path]; exists {
			return nil, fmt.Errorf("bundle: duplicate file path %q", file.Path)
		}
		if file.Source == "" || file.SourceSHA256 == "" {
			return nil, fmt.Errorf("bundle: %s has no source payload", file.Path)
		}
		app.files[file.Path] = file
	}
	entry, ok := app.files[index.Entry]
	if !ok {
		return nil, fmt.Errorf("bundle: entry %q is not in the index", index.Entry)
	}
	if entry.Unit == "" {
		return nil, fmt.Errorf("bundle: entry %q has no harvested unit", index.Entry)
	}
	aliases := map[string]bool{}
	for _, library := range index.Userland {
		file, exists := app.files[library.File]
		if !exists || file.Unit == "" {
			return nil, fmt.Errorf("bundle: userland library %q has no harvested unit", library.File)
		}
		if len(library.Functions) == 0 {
			return nil, fmt.Errorf("bundle: userland library %q has no function aliases", library.File)
		}
		for _, function := range library.Functions {
			name := strings.ToLower(strings.TrimPrefix(function.Name, "\\"))
			if name == "" || function.Implementation == "" {
				return nil, fmt.Errorf("bundle: userland library %q has an incomplete function alias", library.File)
			}
			if aliases[name] {
				return nil, fmt.Errorf("bundle: duplicate userland function alias %q", function.Name)
			}
			aliases[name] = true
		}
	}
	return app, nil
}

// Index returns a copy of the bundle metadata.
func (a *App) Index() Index {
	index := a.index
	index.Files = append([]File(nil), a.index.Files...)
	index.Userland = append([]UserlandLibrary(nil), a.index.Userland...)
	for i := range index.Userland {
		index.Userland[i].Initializers = append([]string(nil),
			a.index.Userland[i].Initializers...)
		index.Userland[i].Functions = append([]UserlandFunction(nil),
			a.index.Userland[i].Functions...)
	}
	return index
}

// Source returns the exact source bytes stored for a virtual path.
func (a *App) Source(name string) ([]byte, error) {
	canonical, ok := cleanVirtual(name)
	if !ok {
		return nil, fmt.Errorf("bundle: invalid source path %q", name)
	}
	file, ok := a.files[canonical]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return readChecked(a.fsys, file.Source, file.SourceSHA256, file.Size)
}

// Run executes the bundle entry in a fresh interpreter request.
func (a *App) Run(ctx *php.Ctx) (php.Value, error) {
	a.cwdMu.Lock()
	loader := &embeddedLoader{
		fsys: a.fsys, files: a.files, units: map[string]*pb.Unit{}, cwd: a.cwd,
	}
	defer func() {
		a.cwd = loader.cwd
		a.cwdMu.Unlock()
	}()
	if a.evalCompilerFactory != nil {
		compiler, err := a.evalCompilerFactory(a.manifest)
		if err != nil {
			return php.Undef(), err
		}
		loader.evalCompiler = compiler
		defer compiler.Close()
	}
	entry, err := loader.CompileFile(a.index.Entry)
	if err != nil {
		return php.Undef(), err
	}
	machine := interp.New(a.manifest)
	machine.SetPersistentSocketPool(a.persistentSockets)
	defer machine.ReleasePersistentSockets()
	machine.SetLoader(loader)
	loaded := interp.Load(entry)
	for _, library := range a.index.Userland {
		libraryUnit, err := loader.CompileFile(library.File)
		if err != nil {
			return php.Undef(), err
		}
		aliases := make(map[string]string, len(library.Functions))
		for _, function := range library.Functions {
			aliases[function.Name] = function.Implementation
		}
		if err := machine.InstallUserlandLibrary(loaded, libraryUnit, aliases); err != nil {
			return php.Undef(), fmt.Errorf("bundle: install %s: %w", library.File, err)
		}
		for _, initializer := range library.Initializers {
			if err := machine.InstallUserlandInitializer(loaded, initializer); err != nil {
				return php.Undef(), fmt.Errorf("bundle: initialize %s: %w", library.File, err)
			}
		}
	}
	return machine.RunUnit(ctx, loaded)
}

type embeddedLoader struct {
	fsys         fs.FS
	files        map[string]File
	units        map[string]*pb.Unit
	cwd          string
	evalCompiler EvalCompiler
}

func (l *embeddedLoader) WorkingDirectory() (string, bool) {
	return l.cwd, l.cwd != ""
}

func (l *embeddedLoader) ChangeWorkingDirectory(requested string) (int, bool) {
	if requested == "" {
		if l.cwd == "" {
			return 0, false
		}
		return int(syscall.ENOENT), true
	}
	candidate, owned := l.resolveVirtualPath(requested)
	if !owned {
		return 0, false
	}
	if candidate == "" || !l.hasPath(candidate) {
		return int(syscall.ENOENT), true
	}
	if _, file := l.files[candidate]; file {
		return int(syscall.ENOTDIR), true
	}
	l.cwd = candidate
	return 0, true
}

func (l *embeddedLoader) UseHostWorkingDirectory() { l.cwd = "" }

func (l *embeddedLoader) ResolveFile(requested, from string) (string, bool) {
	if strings.Contains(requested, "://") {
		return "", false
	}
	if strings.HasPrefix(requested, "/") {
		candidate, ok := cleanVirtual(requested)
		if !ok {
			return "", false
		}
		_, found := l.files[candidate]
		return candidate, found
	}
	if l.cwd != "" {
		candidate, valid := cleanVirtual(path.Join(l.cwd, requested))
		if valid {
			if _, found := l.files[candidate]; found {
				return candidate, true
			}
		}
	}

	if from != "" {
		if base, ok := cleanVirtual(from); ok {
			candidate, valid := cleanVirtual(path.Join(path.Dir(base), requested))
			if valid {
				if _, found := l.files[candidate]; found {
					return candidate, true
				}
			}
		}
	}
	return "", false
}

func (l *embeddedLoader) ResolvePath(requested string) (string, bool, bool) {
	candidate, handled := l.resolveVirtualPath(requested)
	if !handled {
		return "", false, false
	}
	if candidate == "" {
		return "", false, true
	}
	return candidate, l.hasPath(candidate), true
}

func (l *embeddedLoader) InspectPath(requested string) (interp.PathStatus, bool) {
	if requested == "" {
		return interp.PathStatus{}, true
	}
	canonical, handled := l.resolveVirtualPath(requested)
	if !handled {
		return interp.PathStatus{}, false
	}
	if canonical == "" {
		return interp.PathStatus{}, true
	}
	if _, found := l.files[canonical]; found {
		return interp.PathStatus{
			Exists: true, File: true, Readable: true,
		}, true
	}
	if !l.hasPath(canonical) {
		return interp.PathStatus{}, true
	}
	return interp.PathStatus{
		Exists: true, Dir: true, Readable: true, Executable: true,
	}, true
}

func (l *embeddedLoader) StatPath(requested string, follow bool) (interp.PathStat, bool, bool) {
	_ = follow // Bundles contain neither links nor other redirecting entries.
	if requested == "" {
		return interp.PathStat{}, false, true
	}
	canonical, handled := l.resolveVirtualPath(requested)
	if !handled {
		return interp.PathStat{}, false, false
	}
	if canonical == "" {
		return interp.PathStat{}, false, true
	}
	if file, found := l.files[canonical]; found {
		return interp.PathStat{
			Dev: 12, Mode: 0o100444, NLink: 1, RDev: -1,
			Size: file.Size, BlockSize: -1, Blocks: -1,
		}, true, true
	}
	if !l.hasPath(canonical) {
		return interp.PathStat{}, false, true
	}
	return interp.PathStat{
		Dev: 12, Mode: 0o040555, NLink: 2, RDev: -1,
		BlockSize: -1, Blocks: -1,
	}, true, true
}

func (l *embeddedLoader) ReadDirectory(requested string) ([]string, error, bool) {
	canonical, found, handled := l.ResolvePath(requested)
	if !handled {
		return nil, nil, false
	}
	if !found {
		return nil, syscall.ENOENT, true
	}
	if _, file := l.files[canonical]; file {
		return nil, syscall.ENOTDIR, true
	}
	prefix := canonical + "/"
	children := map[string]bool{}
	for name := range l.files {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(name, prefix)
		child, _, _ := strings.Cut(remainder, "/")
		if child != "" {
			children[child] = true
		}
	}
	names := make([]string, 0, len(children)+2)
	names = append(names, ".", "..")
	for child := range children {
		names = append(names, child)
	}
	sort.Strings(names[2:])
	return names, nil, true
}

func (l *embeddedLoader) hasPath(candidate string) bool {
	if candidate == virtualRoot {
		return true
	}
	if _, found := l.files[candidate]; found {
		return true
	}
	prefix := candidate + "/"
	for name := range l.files {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// resolveVirtualPath maps loader-owned paths through the current virtual cwd.
// An empty cwd means the process cwd is authoritative, so relative paths must
// fall through to the host filesystem.
func (l *embeddedLoader) resolveVirtualPath(requested string) (string, bool) {
	if strings.HasPrefix(requested, "/") {
		if requested != virtualRoot && !strings.HasPrefix(requested, virtualRoot+"/") {
			return "", false
		}
		candidate, ok := cleanVirtual(requested)
		if !ok {
			return "", true
		}
		return candidate, true
	}
	if l.cwd == "" {
		return "", false
	}
	candidate, ok := cleanVirtual(path.Join(l.cwd, requested))
	if !ok {
		return "", true
	}
	return candidate, true
}

func (l *embeddedLoader) ReadSource(name string) ([]byte, error) {
	canonical, ok := cleanVirtual(name)
	if !ok {
		if strings.IndexByte(name, 0) < 0 &&
			strings.HasPrefix(name, "/") &&
			name != virtualRoot &&
			!strings.HasPrefix(name, virtualRoot+"/") {
			return nil, fs.ErrNotExist
		}
		return nil, fmt.Errorf("bundle: invalid source path %q", name)
	}
	file, ok := l.files[canonical]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return readChecked(l.fsys, file.Source, file.SourceSHA256, file.Size)
}

func (l *embeddedLoader) CompileFile(name string) (*pb.Unit, error) {
	canonical, ok := cleanVirtual(name)
	if !ok {
		return nil, fmt.Errorf("bundle: invalid PHP path %q", name)
	}
	if cached := l.units[canonical]; cached != nil {
		return cached, nil
	}
	file, ok := l.files[canonical]
	if !ok {
		return nil, fs.ErrNotExist
	}
	if file.Unit == "" {
		return nil, fmt.Errorf("bundle: %s was not harvested as PHP", canonical)
	}
	data, err := readChecked(l.fsys, file.Unit, file.UnitSHA256, -1)
	if err != nil {
		return nil, err
	}
	unit := &pb.Unit{}
	if err := proto.Unmarshal(data, unit); err != nil {
		return nil, fmt.Errorf("bundle: decode unit for %s: %w", canonical, err)
	}
	l.units[canonical] = unit
	return unit, nil
}

func (l *embeddedLoader) CompileString(code string) (*pb.Unit, error) {
	if l.evalCompiler == nil {
		return nil, &interp.UnsupportedError{Opcode: "eval in a static bundle"}
	}
	return l.evalCompiler.CompileString(code)
}

func readChecked(fsys fs.FS, name, wantHash string, wantSize int64) ([]byte, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("bundle: read %s: %w", name, err)
	}
	if wantSize >= 0 && int64(len(data)) != wantSize {
		return nil, fmt.Errorf("bundle: %s is %d bytes, want %d", name, len(data), wantSize)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantHash {
		return nil, fmt.Errorf("bundle: checksum mismatch for %s", name)
	}
	return data, nil
}

func cleanVirtual(name string) (string, bool) {
	if name == "" || strings.IndexByte(name, 0) >= 0 {
		return "", false
	}
	cleaned := path.Clean(name)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = path.Join(virtualRoot, cleaned)
	}
	if cleaned != virtualRoot && !strings.HasPrefix(cleaned, virtualRoot+"/") {
		return "", false
	}
	return cleaned, true
}
