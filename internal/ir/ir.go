// Package ir converts harvested Zend structures to the serialized IR and back.
//
// The IR is deliberately a separate representation from the zend package's
// types: those mirror engine memory and are only valid while the engine holds
// it, whereas this one is written to disk and read by later stages.
package ir

import (
	"fmt"
	"google.golang.org/protobuf/proto"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/zend"
)

// Harvest is everything one run collected from the engine. It is a struct
// rather than a parameter list because the list grew past the point where
// positional arguments are safe: two int64 settings side by side can be
// swapped without the compiler noticing.
type Harvest struct {
	Version string
	Layout  zend.Layout
	Opcodes []string

	Builtins           []*zend.Builtin
	Constants          []zend.Constant
	BuiltinClasses     []*zend.Class
	FramelessFunctions []string
	IniEntries         []zend.IniEntry
	Extensions         []string

	// DefaultErrorReporting is the mask a script starts with, and Assertions is
	// zend.assertions. Both are build settings the IR is only meaningful next
	// to, which is why they travel in the manifest.
	DefaultErrorReporting int64
	Assertions            int64
}

// EncodeManifest records what produced a harvest. The opcode set and operand
// encoding are version-specific, so IR is only meaningful alongside this.
func EncodeManifest(h Harvest) *pb.Manifest {
	manifest := &pb.Manifest{
		Provenance: &pb.Provenance{
			PhpVersion:                h.Version,
			SizeofZnodeOp:             uint32(h.Layout.SizeofZnodeOp),
			AbsoluteJumpAddresses:     h.Layout.AbsoluteJumpAddresses,
			AbsoluteConstantAddresses: h.Layout.AbsoluteConstantAddresses,
		},
		OpcodeNames:           h.Opcodes,
		FramelessFunctions:    h.FramelessFunctions,
		DefaultErrorReporting: h.DefaultErrorReporting,
		Assertions:            h.Assertions,
		Extensions:            h.Extensions,
	}
	for _, builtin := range h.Builtins {
		manifest.Builtins = append(manifest.Builtins, &pb.Builtin{
			Name:         builtin.Name,
			Args:         encodeArgs(builtin.Args),
			ReturnType:   builtin.ReturnType,
			RequiredArgs: builtin.RequiredArgs,
			Variadic:     builtin.Variadic,
		})
	}
	for _, constant := range h.Constants {
		manifest.Constants = append(manifest.Constants, &pb.Constant{
			Deprecated: constant.Deprecated,
			Name:       constant.Name,
			Value:      encodeLiteral(constant.Value),
		})
	}
	for _, entry := range h.IniEntries {
		manifest.IniEntries = append(manifest.IniEntries, &pb.IniEntry{
			Name:        entry.Name,
			GlobalValue: entry.GlobalValue,
			LocalValue:  entry.LocalValue,
			Access:      entry.Access,
			Extension:   entry.Extension,
		})
	}
	for _, class := range h.BuiltinClasses {
		manifest.BuiltinClasses = append(manifest.BuiltinClasses, encodeClass(class))
	}
	return manifest
}

// EncodeUnit converts one harvested source file.
func EncodeUnit(unit *zend.Unit) *pb.Unit {
	encoded := &pb.Unit{File: encodeOpArray(unit.File), CompileDiagnostic: unit.Diagnostic, AutoGlobals: unit.AutoGlobals}
	for _, fn := range unit.Functions {
		encoded.Functions = append(encoded.Functions, encodeOpArray(fn))
	}
	for _, class := range unit.Classes {
		encoded.Classes = append(encoded.Classes, encodeClass(class))
	}
	return encoded
}

func encodeOpArray(op *zend.OpArray) *pb.OpArray {
	if op == nil {
		return nil
	}
	encoded := &pb.OpArray{
		Name:         op.Name,
		Filename:     op.Filename,
		LineStart:    op.LineStart,
		LineEnd:      op.LineEnd,
		DocComment:   op.DocComment,
		NumTemps:     op.NumTemps,
		VarNames:     op.VarNames,
		Args:         encodeArgs(op.Args),
		ReturnType:   op.ReturnType,
		RequiredArgs: op.RequiredArgs,
		Visibility:   op.Visibility,
		Static:       op.Static,
		Abstract:     op.Abstract,
		Final:        op.Final,
		StrictTypes:  op.StrictTypes,
		UsesThis:     op.UsesThis,
		Deprecated:   op.Deprecated,
		ReturnsRef:   op.ReturnsRef,
		Attributes:   encodeAttributes(op.Attributes),
		// The two #[\Deprecated] arguments are optional and may be deferred
		// expressions, so they ride as literals like any other initializer.
		DeprecatedMessage: encodeOptionalLiteral(op.DeprecatedMessage),
		DeprecatedSince:   encodeOptionalLiteral(op.DeprecatedSince),
	}
	for _, lit := range op.Literals {
		encoded.Literals = append(encoded.Literals, encodeLiteral(lit))
	}
	for _, line := range op.Oplines {
		// The opcode name is not stored per opline: it is resolved through the
		// manifest's table, which keeps units small over millions of oplines.
		encoded.Oplines = append(encoded.Oplines, &pb.Opline{
			Opcode:        uint32(line.Opcode),
			Op1Type:       uint32(line.Op1Type),
			Op2Type:       uint32(line.Op2Type),
			ResultType:    uint32(line.ResultType),
			Op1:           line.Op1,
			Op2:           line.Op2,
			Result:        line.Result,
			ExtendedValue: line.Extended,
			Line:          line.Line,
			SmartBranch:   line.SmartBranch,
		})
	}
	for _, tc := range op.TryCatch {
		encoded.TryCatch = append(encoded.TryCatch, &pb.TryCatch{
			TryOp: tc.Try, CatchOp: tc.Catch, FinallyOp: tc.Finally, FinallyEnd: tc.FinallyEnd,
		})
	}
	for _, lr := range op.LiveRanges {
		encoded.LiveRanges = append(encoded.LiveRanges, &pb.LiveRange{
			Var: lr.Var, Start: lr.Start, End: lr.End, Kind: lr.Kind,
		})
	}
	for _, entry := range op.StaticVars {
		encodedVar := &pb.StaticVar{Name: entry.Name, HasDefault: entry.HasDefault}
		if entry.HasDefault {
			encodedVar.DefaultValue = encodeLiteral(entry.Default)
		}
		encoded.StaticVars = append(encoded.StaticVars, encodedVar)
	}
	for _, closure := range op.Closures {
		encoded.Closures = append(encoded.Closures, encodeOpArray(closure))
	}
	return encoded
}

func encodeLiteral(lit zend.Literal) *pb.Literal {
	encoded := &pb.Literal{
		Kind:        literalKind(lit.Kind),
		IntValue:    lit.Int,
		FloatValue:  lit.Float,
		StringValue: lit.Str,
		Expression:  encodeExpr(lit.Expr),
		ExtraValue:  lit.ExtraValue,
	}
	for _, entry := range lit.Entries {
		encoded.Entries = append(encoded.Entries, &pb.LiteralEntry{
			IntKey:      entry.IntKey,
			IntKeyValue: entry.IntKeyVal,
			StringKey:   entry.StringKey,
			Value:       encodeLiteral(entry.Value),
		})
	}
	return encoded
}

// encodeExpr converts an unevaluated initializer's shape. A nil child is a hole
// the source left -- the middle of `a ?: b` -- and becomes a "none" node so the
// positions of the remaining children are preserved.
func encodeExpr(expr *zend.Expr) *pb.Expr {
	if expr == nil {
		return &pb.Expr{Kind: "none"}
	}
	encoded := &pb.Expr{
		Kind:   expr.Kind,
		Name:   expr.Name,
		Member: expr.Member,
		Op:     expr.Op,
		Line:   expr.Line,
	}
	if expr.Kind == "literal" {
		encoded.Value = encodeLiteral(expr.Value)
	}
	for _, child := range expr.Children {
		encoded.Children = append(encoded.Children, encodeExpr(child))
	}
	return encoded
}

func encodeArgs(args []zend.ArgInfo) []*pb.ArgInfo {
	if len(args) == 0 {
		return nil
	}
	encoded := make([]*pb.ArgInfo, 0, len(args))
	for _, arg := range args {
		encoded = append(encoded, &pb.ArgInfo{
			Name: arg.Name, Type: arg.Type, ByRef: arg.ByRef, Variadic: arg.Variadic,
			Attributes: encodeAttributes(arg.Attributes), DefaultValue: arg.Default,
		})
	}
	return encoded
}

func encodeClass(class *zend.Class) *pb.Class {
	encoded := &pb.Class{
		Name:            class.Name,
		Kind:            classKind(class.Kind),
		Parent:          class.Parent,
		Interfaces:      class.Interfaces,
		Traits:          class.Traits,
		Abstract:        class.Abstract,
		Final:           class.Final,
		EnumBackingType: class.EnumBackingType,
		HasGet:          class.HasGet,
		HasSet:          class.HasSet,
		HasCall:         class.HasCall,
		HasCallStatic:   class.HasCallStatic,

		AllowDynamicProperties: class.AllowDynamicProperties,
		NoDynamicProperties:    class.NoDynamicProperties,
		Readonly:               class.Readonly,
		NotSerializable:        class.NotSerializable,
		CustomCreate:           class.CustomCreate,
		NoClone:                class.NoClone,
		Filename:               class.Filename,
		LineStart:              class.LineStart,
		LineEnd:                class.LineEnd,
		DocComment:             class.DocComment,
		Attributes:             encodeAttributes(class.Attributes),
	}
	for _, alias := range class.TraitAliases {
		encoded.TraitAliases = append(encoded.TraitAliases, &pb.TraitAlias{
			Class: alias.Class, Method: alias.Method,
			Alias: alias.Alias, Visibility: alias.Visibility,
			Final: alias.Final,
		})
	}
	for _, rule := range class.TraitPrecedences {
		encoded.TraitPrecedences = append(encoded.TraitPrecedences, &pb.TraitPrecedence{
			Class: rule.Class, Method: rule.Method, Excludes: rule.Excludes,
		})
	}
	for _, method := range class.EngineMethods {
		encoded.EngineMethods = append(encoded.EngineMethods, &pb.EngineMethod{
			Name: method.Name, Visibility: method.Visibility,
			Static: method.Static, Abstract: method.Abstract,
			Args: encodeArgs(method.Args), ReturnType: method.ReturnType,
			RequiredArgs: method.RequiredArgs, Variadic: method.Variadic,
			TentativeReturn: method.TentativeReturn, Final: method.Final,
		})
	}
	for _, prop := range class.Properties {
		encodedProp := &pb.Property{
			Name: prop.Name, Type: prop.Type, Visibility: prop.Visibility,
			DeclaringClass: prop.DeclaringClass,
			Static:         prop.Static, Readonly: prop.Readonly,
			HasDefault: prop.HasDefault,
		}
		encodedProp.Virtual = prop.Virtual
		encodedProp.Order = prop.Order
		encodedProp.SetVisibility = prop.SetVisibility
		encodedProp.Final = prop.Final
		encodedProp.DocComment = prop.DocComment
		encodedProp.Attributes = encodeAttributes(prop.Attributes)
		if prop.GetHook != nil {
			encodedProp.GetHook = encodeOpArray(prop.GetHook)
		}
		if prop.SetHook != nil {
			encodedProp.SetHook = encodeOpArray(prop.SetHook)
		}
		if prop.HasDefault {
			encodedProp.DefaultValue = encodeLiteral(prop.Default)
		}
		encoded.Properties = append(encoded.Properties, encodedProp)
	}
	for _, method := range class.Methods {
		encoded.Methods = append(encoded.Methods, encodeOpArray(method))
	}
	for _, constant := range class.Constants {
		encodedConst := &pb.ClassConstant{
			Name: constant.Name, DeclaringClass: constant.DeclaringClass,
			Visibility: constant.Visibility,
			Final:      constant.Final, HasValue: constant.HasValue,
			IsCase:     constant.Case,
			DocComment: constant.DocComment,
		}
		if constant.HasValue {
			encodedConst.Value = encodeLiteral(constant.Value)
		}
		encodedConst.Attributes = encodeAttributes(constant.Attributes)
		encodedConst.Type = constant.Type
		encodedConst.Deprecated = constant.Deprecated
		encodedConst.DeprecatedMessage = encodeOptionalLiteral(constant.DeprecatedMessage)
		encodedConst.DeprecatedSince = encodeOptionalLiteral(constant.DeprecatedSince)
		encoded.Constants = append(encoded.Constants, encodedConst)
	}
	return encoded
}

// encodeAttributes carries a declaration's `#[...]` list across. The arguments
// stay unevaluated: an attribute is only instantiated when something reflects
// on it, and its arguments may name constants the program has not defined yet.
func encodeAttributes(attributes []zend.Attribute) []*pb.Attribute {
	if len(attributes) == 0 {
		return nil
	}
	encoded := make([]*pb.Attribute, 0, len(attributes))
	for _, attribute := range attributes {
		out := &pb.Attribute{Name: attribute.Name, Line: attribute.Line}
		for _, arg := range attribute.Args {
			out.Args = append(out.Args, &pb.AttributeArg{
				Name: arg.Name, Value: encodeLiteral(arg.Value),
			})
		}
		encoded = append(encoded, out)
	}
	return encoded
}

// EncodeLiteral converts a harvested literal to its IR form. Exported because a
// consumer meeting a literal outside a unit -- the engine's constant table, say
// -- must render it through the same path rather than inventing a second one.
func EncodeLiteral(lit zend.Literal) *pb.Literal { return encodeLiteral(lit) }

// encodeOptionalLiteral encodes an attribute argument that may be absent.
func encodeOptionalLiteral(lit *zend.Literal) *pb.Literal {
	if lit == nil {
		return nil
	}
	return encodeLiteral(*lit)
}

func literalKind(kind string) pb.Literal_Kind {
	switch kind {
	case "null":
		return pb.Literal_KIND_NULL
	case "bool":
		return pb.Literal_KIND_BOOL
	case "int":
		return pb.Literal_KIND_INT
	case "float":
		return pb.Literal_KIND_FLOAT
	case "string":
		return pb.Literal_KIND_STRING
	case "array":
		return pb.Literal_KIND_ARRAY
	case "other":
		return pb.Literal_KIND_OTHER
	case "expr":
		return pb.Literal_KIND_EXPR
	}
	return pb.Literal_KIND_UNSPECIFIED
}

func classKind(kind string) pb.Class_Kind {
	switch kind {
	case "class":
		return pb.Class_KIND_CLASS
	case "interface":
		return pb.Class_KIND_INTERFACE
	case "trait":
		return pb.Class_KIND_TRAIT
	case "enum":
		return pb.Class_KIND_ENUM
	}
	return pb.Class_KIND_UNSPECIFIED
}

// MarshalUnit serializes a unit. Deterministic so that identical input yields
// byte-identical output, which golden tests and build caching both rely on.
func MarshalUnit(unit *pb.Unit) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(unit)
}

// UnmarshalUnit is the inverse of MarshalUnit.
func UnmarshalUnit(data []byte) (*pb.Unit, error) {
	unit := &pb.Unit{}
	if err := proto.Unmarshal(data, unit); err != nil {
		return nil, fmt.Errorf("ir: decoding unit: %w", err)
	}
	return unit, nil
}

// MarshalManifest serializes a manifest.
func MarshalManifest(manifest *pb.Manifest) ([]byte, error) {
	return proto.MarshalOptions{Deterministic: true}.Marshal(manifest)
}

// UnmarshalManifest is the inverse of MarshalManifest.
func UnmarshalManifest(data []byte) (*pb.Manifest, error) {
	manifest := &pb.Manifest{}
	if err := proto.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("ir: decoding manifest: %w", err)
	}
	return manifest, nil
}
