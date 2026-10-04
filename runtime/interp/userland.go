package interp

import (
	"fmt"
	"google.golang.org/protobuf/proto"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
)

// InstallUserlandLibrary makes harvested PHP functions available as private
// helpers and aliases selected implementations to names supplied by the linked
// PHP engine. Aliases intentionally retain the engine signature: callers and
// reflection continue to observe an internal function even though its body is
// implemented in PHP.
func (in *Interp) InstallUserlandLibrary(unit *Unit, library *pb.Unit, aliases map[string]string) error {
	if unit == nil {
		return fmt.Errorf("userland library: target unit is nil")
	}
	if library == nil {
		return fmt.Errorf("userland library: harvested unit is nil")
	}
	if unit.libraryFunctions == nil {
		unit.libraryFunctions = map[string]*pb.OpArray{}
	}
	if unit.userlandBuiltins == nil {
		unit.userlandBuiltins = map[string]*pb.OpArray{}
	}

	for _, function := range library.GetFunctions() {
		key := functionKey(function.GetName())
		if key == "" {
			return fmt.Errorf("userland library: function has no name")
		}
		if _, exists := unit.libraryFunctions[key]; exists {
			return fmt.Errorf("userland library: duplicate implementation %q", function.GetName())
		}
		unit.libraryFunctions[key] = function
	}

	// **The library's classes too** -- php://memory's wrapper -- installed
	// as declared but kept out of classOrder, which is what
	// get_declared_classes lists: they are php2go's, not the program's.
	for _, decl := range library.GetClasses() {
		key := strings.ToLower(decl.GetName())
		if _, exists := unit.classes[key]; !exists {
			unit.classes[key] = newClass(decl)
		}
	}

	for publicName, implementationName := range aliases {
		publicKey := functionKey(publicName)
		signature, exists := in.engineSignatures[publicKey]
		if !exists {
			return fmt.Errorf("userland library: %q is not supplied by the engine", publicName)
		}
		implementation, exists := unit.libraryFunctions[functionKey(implementationName)]
		if !exists {
			return fmt.Errorf("userland library: implementation %q for %q was not harvested", implementationName, publicName)
		}
		if _, exists := unit.userlandBuiltins[publicKey]; exists {
			return fmt.Errorf("userland library: duplicate alias %q", publicName)
		}
		body := proto.Clone(implementation).(*pb.OpArray)
		body.Name = signature.GetName()
		unit.userlandBuiltins[publicKey] = body
	}
	return nil
}

// InstallUserlandInitializer selects a private library function to run before
// the application body. Libraries use this for request-local extension setup;
// the function is not exposed under a public name.
func (in *Interp) InstallUserlandInitializer(unit *Unit, name string) error {
	if unit == nil {
		return fmt.Errorf("userland initializer: target unit is nil")
	}
	initializer, exists := unit.libraryFunctions[functionKey(name)]
	if !exists {
		return fmt.Errorf("userland initializer: implementation %q was not harvested", name)
	}
	unit.userlandInitializers = append(unit.userlandInitializers, initializer)
	return nil
}
