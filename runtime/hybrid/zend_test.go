package hybrid

import (
	"strings"
	"testing"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/zend"
)

func TestCompilerRejectsMismatchedManifestAndReleasesEngine(t *testing.T) {
	_, err := newCompiler(&pb.Manifest{})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched manifest = %v; want incompatibility", err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatalf("rejected compiler left Zend active: %v", err)
	}
	engine.Close()
}
