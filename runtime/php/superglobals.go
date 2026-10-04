package php

// Superglobals are defined by **userland code**, not here. The bundled library's
// initializer (`register_superglobals_impl` in internal/userland/ext/standard.php)
// creates the engine's own -- `$_GET` and the rest -- and a host or application
// initializer that runs first can define any of them; a definition never
// replaces one already made. Most modern programs read the request through a
// framework's object rather than the superglobals, so what they hold is the
// embedding's business rather than the runtime's.
//
// What stays in Go are the two facts PHP cannot compute: which just-in-time
// superglobals the program names (harvested from the compile, see
// SetAutoGlobals), and where a back-end keeps a global variable (the definer
// each back-end installs).

// engineSuperglobals are the names the engine itself defines. Anything else a
// library registers is php2go's own request state -- `$_RESP_HEADERS` -- which
// a PHP program running on the engine could never see in `$GLOBALS`.
var engineSuperglobals = map[string]bool{
	"_GET": true, "_POST": true, "_COOKIE": true, "_FILES": true,
	"_SERVER": true, "_ENV": true, "_REQUEST": true,
}

// IsEngineSuperglobal reports whether name is one of the engine's own
// superglobals. A registered name that is not stays out of `$GLOBALS` and
// `get_defined_vars()`.
func IsEngineSuperglobal(name string) bool { return engineSuperglobals[name] }

// SuperglobalDefiner is how a back-end stores a superglobal a library defines.
// It reports whether it defined the name, which it does not when the name is
// already defined: the first definition wins.
type SuperglobalDefiner func(ctx *Ctx, name string, initial Value) bool

// SetSuperglobalDefiner installs the back-end's definer. Without one,
// DefineSuperglobal registers into Globals, which is the interpreter's table.
func (c *Ctx) SetSuperglobalDefiner(define SuperglobalDefiner) { c.defineSuperglobal = define }

// DefineSuperglobal is `PHP2Go\Internal\register_superglobal`: it defines name
// with initial unless something defined it already.
func (c *Ctx) DefineSuperglobal(name string, initial Value) bool {
	if c.defineSuperglobal != nil {
		return c.defineSuperglobal(c, name, initial)
	}
	if c.SuperglobalRegistered(name) {
		return false
	}
	c.RegisterSuperglobal(name, initial)
	return true
}

// SuperglobalRegistered reports whether name has been defined as a superglobal
// in this request. A name is defined **once**: a program that unsets `$_SERVER`
// does not get it back from a later include naming it.
func (c *Ctx) SuperglobalRegistered(name string) bool {
	_, ok := c.superglobals[name]
	return ok
}

// SetAutoGlobals records the just-in-time superglobals -- `_SERVER`, `_ENV`,
// `_REQUEST` -- the program names, in the order the engine's compile created
// them (`Unit.auto_globals`). The engine creates those only for a script that
// spells one, and `$GLOBALS["_SERVER"]` does not count, so only the compile can
// say; `PHP2Go\Internal\auto_globals()` hands the list to the library.
func (c *Ctx) SetAutoGlobals(names []string) { c.autoGlobals = append([]string(nil), names...) }

// AutoGlobals is the list SetAutoGlobals recorded.
func (c *Ctx) AutoGlobals() []string { return c.autoGlobals }
