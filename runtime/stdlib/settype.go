package stdlib

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `settype`, which is the casts written as a function -- and the one library
// function that writes **through a reference** the caller owns. PHP requires a
// variable in that position and the compiler enforces it, so a value arriving
// here is a gap rather than a program error.

func init() {
	php.RegisterBuiltin("settype", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Bool(false), nil
		}
		ref, isRef := args[0].RefOf()
		if !isRef {
			return php.Undef(), php.Unsupported("settype of a value")
		}
		value := ref.Get()
		var converted php.Value
		switch strings.ToLower(args[1].Deref().String()) {
		case "bool", "boolean":
			converted = php.ToBool(value)
		case "int", "integer":
			converted = php.ToInt(ctx, value)
		case "float", "double":
			converted = php.ToFloat(ctx, value)
		case "string":
			// The same conversion the language uses everywhere else: an array
			// warns, and an object goes through `__toString` -- a class
			// without one is an Error.
			rendered, err := php.ToString(ctx, value)
			if err != nil {
				return php.Undef(), err
			}
			converted = rendered
		case "array":
			converted = php.ToArray(value)
		case "object":
			if value.Deref().Kind() == php.KindObject {
				converted = value.Deref()
				break
			}
			made, ok := ctx.NewObjectOf("stdClass")
			if !ok {
				return php.Undef(), php.Unsupported("settype to object without an object maker")
			}
			made.Props = php.ObjectCastProps(value)
			converted = php.Obj(made)
		case "null":
			converted = php.Null()
		case "resource":
			// There is no conversion *to* a resource: the handle would have to
			// come from somewhere, and nothing can make one up.
			return php.Undef(), php.Raise("ValueError", "Cannot convert to resource type")
		default:
			return php.Undef(), php.Raise("ValueError",
				"settype(): Argument #2 ($type) must be a valid type")
		}
		ref.Set(converted)
		return php.Bool(true), nil
	})
}
