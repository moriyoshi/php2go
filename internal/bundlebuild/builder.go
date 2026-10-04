// Package bundlebuild creates an inspectable Go module containing an embedded
// PHP project and the IR needed to run it without Zend.
package bundlebuild

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/internal/ir"
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/userland"
	"github.com/moriyoshi/php2go/internal/zend"
	runtimebundle "github.com/moriyoshi/php2go/runtime/bundle"
)

// Options control one bundle generation.
type Options struct {
	Root        string
	Entry       string
	Output      string
	RuntimeRoot string
}

// Report summarizes a generated bundle.
type Report struct {
	Files     int
	PHPFiles  int
	Bytes     int64
	Output    string
	Entry     string
	HasVendor bool
}

type inputFile struct {
	absolute string
	relative string
	data     []byte
	php      bool
	internal bool
}

// Build harvests a project and writes a Go module at opts.Output. Output must
// not already exist; refusing avoids replacing an unrelated directory.
func Build(opts Options) (Report, error) {
	root, entry, output, err := validate(opts)
	if err != nil {
		return Report{}, err
	}
	inputs, err := inventory(root, entry, output)
	if err != nil {
		return Report{}, err
	}
	libraries := userland.Libraries()
	for _, library := range libraries {
		inputs = append(inputs, inputFile{
			relative: strings.TrimPrefix(library.VirtualPath, "/app/"),
			data:     library.Source, php: true, internal: true,
		})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].relative < inputs[j].relative })

	engine, err := zend.Start()
	if err != nil {
		return Report{}, err
	}
	defer engine.Close()

	manifest, err := harvestManifest(engine)
	if err != nil {
		return Report{}, err
	}
	manifestData, err := ir.MarshalManifest(manifest)
	if err != nil {
		return Report{}, err
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return Report{}, err
	}
	if err := os.Mkdir(output, 0o755); err != nil {
		if os.IsExist(err) {
			return Report{}, fmt.Errorf("bundle: output %s already exists", output)
		}
		return Report{}, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(output)
		}
	}()

	bundleDir := filepath.Join(output, "bundle")
	for _, dir := range []string{
		bundleDir,
		filepath.Join(bundleDir, "source"),
		filepath.Join(bundleDir, "units"),
	} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			return Report{}, err
		}
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.pb"), manifestData, 0o644); err != nil {
		return Report{}, err
	}

	index := runtimebundle.Index{
		Format: runtimebundle.FormatVersion, Entry: virtualPath(mustRelative(root, entry)),
		Manifest: "manifest.pb",
	}
	for _, library := range libraries {
		descriptor := runtimebundle.UserlandLibrary{
			File:         library.VirtualPath,
			Initializers: append([]string(nil), library.Initializers...),
		}
		for _, function := range library.Functions {
			descriptor.Functions = append(descriptor.Functions, runtimebundle.UserlandFunction{
				Name: function.Name, Implementation: function.Implementation,
			})
		}
		index.Userland = append(index.Userland, descriptor)
	}
	report := Report{Output: output, Entry: index.Entry}
	writtenSources := map[string]bool{}
	writtenUnits := map[string]bool{}
	for _, input := range inputs {
		sourceHash := digest(input.data)
		sourceName := path.Join("source", sourceHash)
		if !writtenSources[sourceHash] {
			if err := os.WriteFile(filepath.Join(bundleDir, filepath.FromSlash(sourceName)), input.data, 0o644); err != nil {
				return Report{}, err
			}
			writtenSources[sourceHash] = true
		}
		file := runtimebundle.File{
			Path: virtualPath(input.relative), Source: sourceName,
			SourceSHA256: sourceHash, Size: int64(len(input.data)),
		}
		if !input.internal {
			report.Files++
			report.Bytes += int64(len(input.data))
		}
		if input.relative == "vendor" || strings.HasPrefix(input.relative, "vendor/") {
			report.HasVendor = true
		}
		if input.php {
			if err := engine.Reset(); err != nil {
				return Report{}, err
			}
			var harvested *zend.Unit
			if input.internal {
				harvested, err = engine.CompileEvalString(strings.TrimPrefix(string(input.data), "<?php"), file.Path)
			} else if input.absolute == entry {
				harvested, err = engine.CompileMainUnit(input.absolute)
			} else {
				harvested, err = engine.CompileUnit(input.absolute)
			}
			if err != nil {
				return Report{}, fmt.Errorf("bundle: compile %s: %w", input.relative, err)
			}
			unit := ir.EncodeUnit(harvested)
			physical := input.absolute
			if input.internal {
				physical = file.Path
			}
			relocateUnit(unit, physical, file.Path)
			unitData, err := ir.MarshalUnit(unit)
			if err != nil {
				return Report{}, fmt.Errorf("bundle: encode %s: %w", input.relative, err)
			}
			unitHash := digest(unitData)
			unitName := path.Join("units", unitHash+".pb")
			if !writtenUnits[unitHash] {
				if err := os.WriteFile(filepath.Join(bundleDir, filepath.FromSlash(unitName)), unitData, 0o644); err != nil {
					return Report{}, err
				}
				writtenUnits[unitHash] = true
			}
			file.Unit, file.UnitSHA256 = unitName, unitHash
			if !input.internal {
				report.PHPFiles++
			}
		}
		index.Files = append(index.Files, file)
	}

	indexData, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return Report{}, err
	}
	indexData = append(indexData, '\n')
	if err := os.WriteFile(filepath.Join(bundleDir, runtimebundle.IndexFile), indexData, 0o644); err != nil {
		return Report{}, err
	}
	if err := os.WriteFile(filepath.Join(output, "main.go"), []byte(launcherSource), 0o644); err != nil {
		return Report{}, err
	}
	if err := os.WriteFile(filepath.Join(output, "hybrid_disabled.go"), []byte(hybridDisabledSource), 0o644); err != nil {
		return Report{}, err
	}
	if err := os.WriteFile(filepath.Join(output, "hybrid_zend.go"), []byte(hybridZendSource), 0o644); err != nil {
		return Report{}, err
	}
	if err := os.WriteFile(filepath.Join(output, "go.mod"), []byte(moduleFile(opts.RuntimeRoot)), 0o644); err != nil {
		return Report{}, err
	}
	if err := os.WriteFile(filepath.Join(output, "go.sum"), []byte(moduleSums), 0o644); err != nil {
		return Report{}, err
	}
	complete = true
	return report, nil
}

func validate(opts Options) (root, entry, output string, err error) {
	if opts.Entry == "" {
		return "", "", "", fmt.Errorf("bundle: entry is required")
	}
	if opts.Output == "" {
		return "", "", "", fmt.Errorf("bundle: output is required")
	}
	entry = opts.Entry
	if opts.Root == "" {
		entry, err = filepath.Abs(entry)
		if err != nil {
			return "", "", "", err
		}
		opts.Root = filepath.Dir(entry)
	}
	root, err = filepath.Abs(opts.Root)
	if err != nil {
		return "", "", "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", "", fmt.Errorf("bundle: root: %w", err)
	}
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(root, entry)
	}
	entry, err = filepath.Abs(entry)
	if err != nil {
		return "", "", "", err
	}
	entry, err = filepath.EvalSymlinks(entry)
	if err != nil {
		return "", "", "", fmt.Errorf("bundle: entry: %w", err)
	}
	if _, err := filepath.Rel(root, entry); err != nil || !within(root, entry) {
		return "", "", "", fmt.Errorf("bundle: entry %s is outside root %s", entry, root)
	}
	info, err := os.Stat(entry)
	if err != nil {
		return "", "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", "", fmt.Errorf("bundle: entry %s is not a regular file", entry)
	}
	output, err = filepath.Abs(opts.Output)
	if err != nil {
		return "", "", "", err
	}
	if output == root {
		return "", "", "", fmt.Errorf("bundle: output cannot replace the project root")
	}
	if _, statErr := os.Lstat(output); statErr == nil {
		return "", "", "", fmt.Errorf("bundle: output %s already exists", output)
	} else if !os.IsNotExist(statErr) {
		return "", "", "", fmt.Errorf("bundle: inspect output: %w", statErr)
	}
	return root, entry, output, nil
}

func inventory(root, entry, output string) ([]inputFile, error) {
	var files []inputFile
	err := filepath.WalkDir(root, func(name string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == output {
			if item.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if item.IsDir() {
			if name != root && item.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if item.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle: symlink %s is not supported", name)
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative := mustRelative(root, name)
		if relative == ".php2go" || strings.HasPrefix(relative, ".php2go/") {
			return fmt.Errorf("bundle: project path %s uses reserved .php2go directory", relative)
		}
		files = append(files, inputFile{
			absolute: name, relative: relative, data: data,
			php: name == entry || looksLikePHP(relative, data),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })
	return files, nil
}

func looksLikePHP(name string, data []byte) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".php", ".inc", ".phtml", ".phps":
		return true
	}
	return bytes.Contains(data, []byte("<?php")) || bytes.Contains(data, []byte("<?="))
}

func harvestManifest(engine *zend.Engine) (*pb.Manifest, error) {
	builtins, err := engine.Builtins()
	if err != nil {
		return nil, err
	}
	constants, err := engine.Constants()
	if err != nil {
		return nil, err
	}
	frameless, err := engine.FramelessFunctions()
	if err != nil {
		return nil, err
	}
	classes, err := engine.InternalClasses()
	if err != nil {
		return nil, err
	}
	iniEntries, err := engine.IniEntries()
	if err != nil {
		return nil, err
	}
	extensions, err := engine.Extensions()
	if err != nil {
		return nil, err
	}
	return ir.EncodeManifest(ir.Harvest{
		Version: zend.Version(), Layout: zend.DetectLayout(), Opcodes: zend.OpcodeNames(),
		Builtins: builtins, Constants: constants, BuiltinClasses: classes,
		FramelessFunctions: frameless, DefaultErrorReporting: engine.DefaultErrorReporting(),
		Assertions: engine.Assertions(), IniEntries: iniEntries, Extensions: extensions,
	}), nil
}

func relocateUnit(unit *pb.Unit, physical, virtual string) {
	physicalDir := filepath.Dir(physical)
	virtualDir := path.Dir(virtual)
	unit.CompileDiagnostic = relocateText(unit.GetCompileDiagnostic(), physical, physicalDir, virtual, virtualDir)

	var relocateOp func(*pb.OpArray)
	relocateOp = func(op *pb.OpArray) {
		if op == nil {
			return
		}
		op.Filename = virtual
		for _, literal := range op.GetLiterals() {
			if literal.GetKind() == pb.Literal_KIND_STRING {
				literal.StringValue = relocateText(
					literal.GetStringValue(), physical, physicalDir, virtual, virtualDir)
			}
		}
		for _, closure := range op.GetClosures() {
			relocateOp(closure)
		}
	}
	relocateOp(unit.GetFile())
	for _, function := range unit.GetFunctions() {
		relocateOp(function)
	}
	for _, class := range unit.GetClasses() {
		class.Filename = virtual
		class.Name = relocateText(class.GetName(), physical, physicalDir, virtual, virtualDir)
		for _, method := range class.GetMethods() {
			relocateOp(method)
		}
	}
}

func relocateText(text, physical, physicalDir, virtual, virtualDir string) string {
	text = strings.ReplaceAll(text, physical, virtual)
	if physicalDir != "" {
		text = strings.ReplaceAll(text, physicalDir, virtualDir)
	}
	return text
}

func virtualPath(relative string) string {
	return path.Join("/app", filepath.ToSlash(relative))
}

func mustRelative(root, name string) string {
	relative, err := filepath.Rel(root, name)
	if err != nil {
		panic(err)
	}
	return filepath.ToSlash(relative)
}

func within(root, name string) bool {
	relative, err := filepath.Rel(root, name)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func moduleFile(runtimeRoot string) string {
	var out strings.Builder
	out.WriteString("module php2go.local/bundle\n\ngo 1.26.5\n\n")
	out.WriteString("require (\n")
	out.WriteString("\tgithub.com/moriyoshi/php2go v0.0.0\n")
	out.WriteString("\tgoogle.golang.org/protobuf v1.36.12\n")
	out.WriteString(")\n")
	if runtimeRoot != "" {
		out.WriteString("\nreplace github.com/moriyoshi/php2go => ")
		out.WriteString(strconv.Quote(runtimeRoot))
		out.WriteByte('\n')
	}
	return out.String()
}

const moduleSums = "google.golang.org/protobuf v1.36.12 h1:pJOKDDOyeXErUroCihFAd5LQuwXBSpVnKGrj5o/fwxc=\n" +
	"google.golang.org/protobuf v1.36.12/go.mod h1:HTf+CrKn2C3g5S8VImy6tdcUvCska2kB7j23XfzDpco=\n"

const launcherSource = `// Code generated by php2go bundle. DO NOT EDIT.
package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/moriyoshi/php2go/runtime/bundle"
	"github.com/moriyoshi/php2go/runtime/php"
)

//go:embed all:bundle
var embedded embed.FS

func main() {
	files, err := fs.Sub(embedded, "bundle")
	if err != nil {
		fail(err)
	}
	app, err := bundle.Open(files)
	if err != nil {
		fail(err)
	}
	enableHybrid(app)
	if _, err := app.Run(php.NewCtx(os.Stdout)); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "php2go bundle:", err)
	os.Exit(255)
}
`

const hybridDisabledSource = `//go:build !php2go_hybrid

// Code generated by php2go bundle. DO NOT EDIT.
package main

import "github.com/moriyoshi/php2go/runtime/bundle"

func enableHybrid(*bundle.App) {}
`

const hybridZendSource = `//go:build php2go_hybrid

// Code generated by php2go bundle. DO NOT EDIT.
package main

import (
	"github.com/moriyoshi/php2go/runtime/bundle"
	"github.com/moriyoshi/php2go/runtime/hybrid"
)

func enableHybrid(app *bundle.App) { hybrid.EnableZend(app) }
`
