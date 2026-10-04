package ir_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/moriyoshi/php2go/internal/ir"
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/zend"
)

var engine *zend.Engine

func TestMain(m *testing.M) {
	started, err := zend.Start()
	if err != nil {
		panic(err)
	}
	engine = started
	code := m.Run()
	engine.Close()
	os.Exit(code)
}

func harvest(t *testing.T) *zend.Unit {
	t.Helper()
	unit, err := engine.CompileUnit(filepath.Join("testdata", "sample.php"))
	if err != nil {
		t.Fatal(err)
	}
	return unit
}

func TestUnitRoundTrip(t *testing.T) {
	unit := harvest(t)
	encoded := ir.EncodeUnit(unit)

	data, err := ir.MarshalUnit(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ir.UnmarshalUnit(data)
	if err != nil {
		t.Fatal(err)
	}

	// Re-marshalling the decoded message must reproduce the same bytes; that is
	// what makes the format usable for golden tests and build caching.
	again, err := ir.MarshalUnit(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again) {
		t.Fatalf("round trip changed the encoding: %d vs %d bytes", len(data), len(again))
	}

	// Spot-check that the content survived rather than trusting byte equality
	// of an empty message.
	if decoded.GetFile().GetFilename() != unit.File.Filename {
		t.Errorf("filename = %q, want %q", decoded.GetFile().GetFilename(), unit.File.Filename)
	}
	if len(decoded.GetFile().GetOplines()) != len(unit.File.Oplines) {
		t.Errorf("oplines = %d, want %d", len(decoded.GetFile().GetOplines()), len(unit.File.Oplines))
	}
	if len(decoded.GetFunctions()) != len(unit.Functions) {
		t.Errorf("functions = %d, want %d", len(decoded.GetFunctions()), len(unit.Functions))
	}
	if len(decoded.GetClasses()) != len(unit.Classes) {
		t.Errorf("classes = %d, want %d", len(decoded.GetClasses()), len(unit.Classes))
	}
}

// Encoding must be stable across runs, otherwise golden comparisons would churn
// and nothing downstream could be cached.
func TestEncodingIsDeterministic(t *testing.T) {
	first, err := ir.MarshalUnit(ir.EncodeUnit(harvest(t)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := ir.MarshalUnit(ir.EncodeUnit(harvest(t)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("two harvests of the same file encoded differently (%d vs %d bytes)", len(first), len(second))
	}
}

// The golden file pins the encoding. A diff here means either a real IR change
// or an accidental one; both deserve review. Refresh with -update.
var update = os.Getenv("UPDATE_GOLDEN") != ""

func TestUnitGolden(t *testing.T) {
	data, err := ir.MarshalUnit(ir.EncodeUnit(harvest(t)))
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "sample.unit.pb")
	if update {
		if err := os.WriteFile(golden, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s (%d bytes)", golden, len(data))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (regenerate with UPDATE_GOLDEN=1)", err)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("IR encoding changed: %d bytes now, %d in golden. "+
			"If intended, regenerate with UPDATE_GOLDEN=1.", len(data), len(want))
	}
}

// The manifest pins IR to the build that produced it: the opcode set and
// operand encoding are version-specific, so a decoder has to be able to check.
func TestManifestRoundTrip(t *testing.T) {
	builtins, err := engine.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	constants, err := engine.Constants()
	if err != nil {
		t.Fatal(err)
	}
	framelessFunctions, err := engine.FramelessFunctions()
	if err != nil {
		t.Fatal(err)
	}
	builtinClasses, err := engine.InternalClasses()
	if err != nil {
		t.Fatal(err)
	}
	iniEntries, err := engine.IniEntries()
	if err != nil {
		t.Fatal(err)
	}
	extensions, err := engine.Extensions()
	if err != nil {
		t.Fatal(err)
	}
	manifest := ir.EncodeManifest(ir.Harvest{
		Version: "8.4.24", Layout: zend.DetectLayout(), Opcodes: zend.OpcodeNames(),
		Builtins: builtins, Constants: constants, BuiltinClasses: builtinClasses,
		FramelessFunctions:    framelessFunctions,
		IniEntries:            iniEntries,
		Extensions:            extensions,
		DefaultErrorReporting: engine.DefaultErrorReporting(), Assertions: engine.Assertions(),
	})

	data, err := ir.MarshalManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ir.UnmarshalManifest(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.GetProvenance().GetPhpVersion() != "8.4.24" {
		t.Errorf("php version = %q", decoded.GetProvenance().GetPhpVersion())
	}
	if got := decoded.GetProvenance().GetSizeofZnodeOp(); got != uint32(zend.DetectLayout().SizeofZnodeOp) {
		t.Errorf("znode_op size = %d", got)
	}
	if len(decoded.GetOpcodeNames()) != 256 {
		t.Errorf("opcode table = %d entries, want 256", len(decoded.GetOpcodeNames()))
	}
	if decoded.GetOpcodeNames()[1] != "ZEND_ADD" {
		t.Errorf("opcode 1 = %q", decoded.GetOpcodeNames()[1])
	}
	if len(decoded.GetBuiltins()) != len(builtins) {
		t.Errorf("builtins = %d, want %d", len(decoded.GetBuiltins()), len(builtins))
	}
	if len(decoded.GetConstants()) != len(constants) {
		t.Errorf("constants = %d, want %d", len(decoded.GetConstants()), len(constants))
	}
	if len(decoded.GetIniEntries()) != len(iniEntries) {
		t.Errorf("INI entries = %d, want %d", len(decoded.GetIniEntries()), len(iniEntries))
	}
	if len(decoded.GetExtensions()) != len(extensions) {
		t.Errorf("extensions = %d, want %d", len(decoded.GetExtensions()), len(extensions))
	}
	var foundNullable bool
	for _, entry := range decoded.GetIniEntries() {
		if entry.GetName() == "fiber.stack_size" {
			foundNullable = true
			if entry.LocalValue != nil {
				t.Errorf("fiber.stack_size local value = %q, want null", entry.GetLocalValue())
			}
		}
	}
	if !foundNullable {
		t.Error("fiber.stack_size missing from harvested INI entries")
	}
	// A build-specific value is the point of harvesting rather than
	// transcribing: E_ALL has changed across releases.
	var foundEAll bool
	for _, constant := range decoded.GetConstants() {
		if constant.GetName() == "E_ALL" {
			foundEAll = true
			if constant.GetValue().GetKind() != pb.Literal_KIND_INT {
				t.Errorf("E_ALL kind = %v, want int", constant.GetValue().GetKind())
			}
		}
	}
	if !foundEAll {
		t.Error("E_ALL missing from the harvested constants")
	}
}
