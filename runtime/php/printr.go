package php

// What is left of `print_r` here after the renderer moved to
// `internal/userland`: the visibility annotation, which is a fact about a
// property's declaration rather than a piece of formatting, and which the
// `print_r_object` leaf applies before handing the entries to PHP.

// PrintRPropertyLabel is var_dump's visibility annotation in print_r's
// punctuation: `name:protected`, `name:Class:private`.
func PrintRPropertyLabel(o *Object, name string) string {
	if plain, owner, mangled := UnmangleProperty(name); mangled {
		if owner == "*" {
			return plain + ":protected"
		}
		return plain + ":" + owner + ":private"
	}
	switch declared := o.Declared[name]; {
	case o.Dynamic[name]:
	case declared.Visibility == "protected":
		return name + ":protected"
	case declared.Visibility == "private":
		return name + ":" + DisplayName(declared.Class) + ":private"
	}
	return name
}
