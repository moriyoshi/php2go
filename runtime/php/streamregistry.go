package php

// The stream wrapper and filter registries.
//
// They are here rather than on the interpreter's Unit because `fopen` has to
// consult them and an emitted program has to be able to `fopen`. They are
// per-Ctx for the reason the constant table and the error-handler stack are:
// `stream_wrapper_register` and `stream_filter_register` are things a *request*
// does, and PHP is shared-nothing per request while a long-lived Go process is
// not.
//
// What they hold is deliberately thin. A wrapper is a class name, some flags,
// and whether the engine supplied it; the resolved **class** is `any`, because
// what a class is belongs to the layer with a class table -- the same reason
// `Resource.Payload` is `any` and `UserStream` is an interface.

// BuiltinStreamWrapperSchemes are the wrappers the engine provides, in the
// order `stream_get_wrappers` reports them.
var BuiltinStreamWrapperSchemes = []string{"php", "file", "glob", "data", "http", "ftp"}

// StreamWrapper is one registered wrapper.
type StreamWrapper struct {
	// ClassName is the PHP class that implements it, empty for a builtin.
	ClassName string
	Flags     int64
	// Builtin marks a wrapper the engine supplied rather than the program.
	Builtin bool
	// Class is the resolved class, opaque here. Whoever registered the wrapper
	// put it there and is the only one that can read it back.
	Class any
	// Factory is the `stream factory` resource the engine holds for a wrapper
	// the program registered, released when it is unregistered. It takes a
	// resource number, which is how it is observed (NewStreamFactory).
	Factory *Resource
}

// NewStreamFactory takes the resource stream_wrapper_register holds. The
// engine allocates it once the class is found and *before* checking the
// protocol, so a duplicate or invalid scheme still uses a number: the caller
// closes it on failure and keeps it in the StreamWrapper on success.
func (c *Ctx) NewStreamFactory() *Resource { return c.NewResource("stream factory", nil) }

func (c *Ctx) ensureStreamWrappers() {
	if c.streamWrappers != nil {
		return
	}
	c.streamWrappers = map[string]*StreamWrapper{}
	for _, scheme := range BuiltinStreamWrapperSchemes {
		c.streamWrappers[scheme] = &StreamWrapper{Builtin: true}
		c.streamWrapperOrder = append(c.streamWrapperOrder, scheme)
	}
}

// StreamWrapperFor answers the wrapper registered for a scheme.
func (c *Ctx) StreamWrapperFor(scheme string) (*StreamWrapper, bool) {
	c.ensureStreamWrappers()
	wrapper, found := c.streamWrappers[scheme]
	return wrapper, found
}

// StreamWrapperSchemes is every registered scheme, in registration order --
// the builtins first, then whatever the program added.
func (c *Ctx) StreamWrapperSchemes() []string {
	c.ensureStreamWrappers()
	return append([]string(nil), c.streamWrapperOrder...)
}

// RegisterStreamWrapper adds one, reporting false when the scheme is taken.
// PHP refuses rather than replacing, which is why this answers rather than
// overwriting.
func (c *Ctx) RegisterStreamWrapper(scheme string, wrapper *StreamWrapper) bool {
	c.ensureStreamWrappers()
	if _, taken := c.streamWrappers[scheme]; taken {
		return false
	}
	c.streamWrappers[scheme] = wrapper
	c.streamWrapperOrder = append(c.streamWrapperOrder, scheme)
	return true
}

// UnregisterStreamWrapper removes one, reporting whether there was one.
func (c *Ctx) UnregisterStreamWrapper(scheme string) bool {
	c.ensureStreamWrappers()
	held, found := c.streamWrappers[scheme]
	if !found {
		return false
	}
	held.Factory.Close()
	delete(c.streamWrappers, scheme)
	for index, name := range c.streamWrapperOrder {
		if name == scheme {
			c.streamWrapperOrder = append(
				c.streamWrapperOrder[:index], c.streamWrapperOrder[index+1:]...)
			break
		}
	}
	return true
}

// RegisterStreamFilter records a user filter class under a name, which may end
// in `.*` to claim a whole family. False when the name is taken.
func (c *Ctx) RegisterStreamFilter(name, className string) bool {
	if c.streamFilters == nil {
		c.streamFilters = map[string]string{}
	}
	if _, taken := c.streamFilters[name]; taken {
		return false
	}
	c.streamFilters[name] = className
	c.streamFilterOrder = append(c.streamFilterOrder, name)
	return true
}

// StreamFilterClass resolves a filter name to its class, trying the exact name
// and then each `prefix.*` the name falls under -- `convert.iconv.utf-8` is
// answered by `convert.iconv.*` and then by `convert.*`.
func (c *Ctx) StreamFilterClass(name string) (string, bool) {
	if className, found := c.streamFilters[name]; found {
		return className, true
	}
	for at := len(name); at > 0; at-- {
		if name[at-1] != '.' {
			continue
		}
		if className, found := c.streamFilters[name[:at]+"*"]; found {
			return className, true
		}
	}
	return "", false
}

// StreamFilterNames is every registered user filter name, **in registration
// order** -- `stream_get_filters` reports them that way, so a map's iteration
// order will not do.
func (c *Ctx) StreamFilterNames() []string {
	return append([]string(nil), c.streamFilterOrder...)
}

// UserStreamOpener opens a stream through a wrapper the program registered.
// Supplied from above for the reason CallValue is: the wrapper is a **class**,
// and instantiating one and calling `stream_open` on it needs a class table.
//
// The registry itself is here because `fopen` has to *find* the wrapper before
// it knows whether this is needed at all -- most schemes are builtin and never
// reach the hook.
type UserStreamOpener func(ctx *Ctx, wrapper *StreamWrapper,
	path, mode string, options int64, context Value) (Value, error)

// SetUserStreamOpener installs the hook.
func (c *Ctx) SetUserStreamOpener(fn UserStreamOpener) { c.openUserStream = fn }

// OpenUserStream opens through a registered wrapper class, and refuses honestly
// when nothing here can. A program that reached this registered the wrapper, so
// answering false would be a wrong answer rather than a missing feature.
func (c *Ctx) OpenUserStream(wrapper *StreamWrapper,
	path, mode string, options int64, context Value) (Value, error) {
	if c.openUserStream == nil {
		return Undef(), Unsupported("fopen through the user-registered wrapper %q", wrapper.ClassName)
	}
	return c.openUserStream(c, wrapper, path, mode, options, context)
}
