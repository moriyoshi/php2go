package php

// Producing a lazy object's state.
//
// A lazy object is one with a class and a handle but no state, whose state
// appears the first time anything looks at it. A **ghost** is filled in place by
// its initializer; a **proxy** hands off to a real instance the initializer
// returns. The rules are the engine's and most of them were wrong here once --
// see `.agents/docs/LTM/lazy-objects.md`.
//
// This lives in `runtime/php` because both back-ends need it and the algorithm
// is too subtle to keep two copies of. What it cannot know is anything about a
// class table, so the three questions it has to ask go out through hooks.

// MagicAvailable reports whether a magic accessor should answer for a name --
// the class has one, and it is not already running for that same name on that
// same object.
type MagicAvailable func(object *Object, method, prop string) bool

// ProxyCompatible reports whether a lazy proxy of one class may hand off to an
// instance of another.
type ProxyCompatible func(c *Ctx, proxyClass, instanceClass string) bool

// LazyInitCall runs a lazy object's initializer.
//
// A hook rather than `Ctx.Call` because the engine runs the initializer **at
// the access that produced the state**, so its stack frame names the line that
// touched the object rather than `[internal function]`, and only the back-end
// knows how to make a call say that.
type LazyInitCall func(ctx *Ctx, initializer Value, object *Object) (Value, error)

// SetMagicAvailable installs the accessor hook.
func (c *Ctx) SetMagicAvailable(fn MagicAvailable) { c.magicAvailable = fn }

// SetProxyCompatible installs the proxy-compatibility hook.
func (c *Ctx) SetProxyCompatible(fn ProxyCompatible) { c.proxyCompatible = fn }

// SetLazyInitCall installs the initializer-call hook.
func (c *Ctx) SetLazyInitCall(fn LazyInitCall) { c.lazyInitCall = fn }

// LazySnapshot is an object's whole property state, kept so that an initializer
// that throws can be undone. The engine restores the properties table it
// swapped aside; here the table is copied, which comes to the same thing because
// nothing outside the object holds those slots.
type LazySnapshot struct {
	props   []Key
	values  []Value
	dynamic map[string]bool
	cleared map[string]bool
}

// TakeLazySnapshot records an object's state.
func TakeLazySnapshot(object *Object) *LazySnapshot {
	taken := &LazySnapshot{
		dynamic: copyFlagSet(object.Dynamic),
		cleared: copyFlagSet(object.Cleared),
	}
	object.PropEach(func(k Key, v Value) bool {
		taken.props = append(taken.props, k)
		taken.values = append(taken.values, v)
		return true
	})
	return taken
}

// Restore puts it back.
func (s *LazySnapshot) Restore(object *Object) {
	if s == nil {
		return
	}
	// Back in table mode: the snapshot holds every property by key.
	object.Props, object.Slots, object.Layout = NewArray(), nil, nil
	for i, key := range s.props {
		object.Props.Set(key, s.values[i])
	}
	object.Dynamic = s.dynamic
	object.Cleared = s.cleared
}

func copyFlagSet(flags map[string]bool) map[string]bool {
	if flags == nil {
		return nil
	}
	copied := make(map[string]bool, len(flags))
	for k, v := range flags {
		copied[k] = v
	}
	return copied
}

// declaredVirtual reports whether the class declares this property as virtual --
// a hooked property with no backing slot, which defers nothing because there is
// no state behind it to produce.
func (c *Ctx) declaredVirtual(class, name string) bool {
	for _, p := range c.DeclaredPropertiesOf(class) {
		if p.Name == name {
			return p.Virtual
		}
	}
	return false
}

// ResolveLazy produces a lazy object's state and answers the object that holds
// it -- which for a proxy is the instance behind it, not the proxy.
//
// `property` is the name being reached, empty for an access that is about the
// object as a whole; `magic` is the accessor that would answer for an undeclared
// name, empty when none applies.
// releaseInitializer hands the initializer's object number back when the lazy
// state was its only holder.
//
// Initialisation is where the engine drops it, and the number becomes reusable
// at once: a ghost made with an inline closure gives that closure's handle to
// the **next** object created after it initialises. With the same closure also
// kept in a variable it does not, which is why this is gated on a proof the
// caller made rather than done unconditionally.
func releaseInitializer(c *Ctx, lazy *Lazy) {
	if lazy == nil || !lazy.OwnsInitializer {
		return
	}
	// An initialized proxy keeps its lazy state (the instance lives there),
	// so a later drop of it reaches here again; the release itself is
	// once-only (Object.TakeIDRelease), which is what keeps the number from
	// being handed back twice.
	if o := lazy.Initializer.Deref().Object(); o != nil && o.SoleOwned {
		c.ReleaseHandle(o)
	}
}

// DropLazy ends an object's laziness without running its initializer -- the
// last deferred property was skipped or given a raw value, the object was
// marked initialized, or it was made lazy with nothing to defer. The engine
// drops the initializer there as on initialisation, so a closure the lazy
// state solely owned gives its number back at once.
func DropLazy(c *Ctx, object *Object) {
	if object == nil || object.Lazy == nil {
		return
	}
	releaseInitializer(c, object.Lazy)
	object.Lazy = nil
}

func ResolveLazy(c *Ctx, object *Object, property, magic string) (*Object, error) {
	if object == nil || object.Lazy == nil {
		return object, nil
	}
	// An incomplete object is not lazy; reaching here means no handler
	// answered for this access (incomplete.go).
	if object.Lazy.Incomplete {
		return object, Unsupported("this operation on an incomplete object (%s)", IncompleteName(object))
	}
	lazy := object.Lazy
	if property != "" {
		if lazy.Skipped[property] {
			// This property has a value already; nothing about it is deferred,
			// so it answers from the object's own slot even on a proxy.
			return object, nil
		}
		if c.declaredVirtual(object.ClassName, property) {
			// A virtual hooked property has no state behind it: its value comes
			// from the hook. Reaching it therefore produces nothing, and
			// whatever the hook body touches produces the state itself if it
			// needs to.
			return object, nil
		}
		if magic != "" && !c.DeclaresProperty(object.ClassName, property) &&
			c.magicAvailable != nil && c.magicAvailable(object, magic, property) {
			// A name the class does not declare is the magic accessor's
			// business, and it is asked before anything is initialized -- the
			// accessor may not touch the object's state at all. But once the
			// accessor is *running* for that name, the access inside it is the
			// real one: `$this->$name` in __set reaches the state, and reaching
			// it is what produces it.
			return object, nil
		}
	}
	if lazy.Instance != nil {
		// The instance a proxy forwards to may itself be a proxy: the engine
		// re-enters the property handler on it, so the answer is the end of the
		// chain and each link answers about its own deferred set.
		return ResolveLazy(c, lazy.Instance, property, magic)
	}
	if lazy.Running {
		// The initializer is looking at the object it is initializing. For a
		// ghost that is the normal case -- it is filling the properties in -- so
		// it sees the object as it stands.
		return object, nil
	}
	if c.lazyInitCall == nil {
		return object, Unsupported("lazy object initialization")
	}
	lazy.Running = true
	defer func() { lazy.Running = false }()
	var before *LazySnapshot
	if !lazy.Proxy {
		// A ghost is initialized in place, and it starts from the state an
		// ordinary new object would have: the declared defaults, which its
		// laziness had been hiding. What it had *before* that is what an
		// initializer that throws is rolled back to.
		before = TakeLazySnapshot(object)
		RestoreDeclaredDefaults(c, object)
	}
	produced, err := c.lazyInitCall(c, lazy.Initializer, object)
	if err != nil {
		// An initializer that fails leaves nothing behind: the object is lazy
		// again, and a later access will call it again. A ghost is rolled back
		// to the state it was in before -- which is not the same as empty, since
		// a raw write may already have given some properties values that were
		// never deferred.
		if !lazy.Proxy {
			before.Restore(object)
		}
		return object, err
	}
	if !lazy.Proxy {
		if held := produced.Deref(); !held.IsUndef() && held.Kind() != KindNull {
			// A ghost fills itself in, so there is nothing for its initializer
			// to hand back. The engine reverts the initialization first -- the
			// object is lazy again and the initializer will run again.
			before.Restore(object)
			return object, RaiseAt(c, "TypeError",
				"%s", "Lazy object initializer must return NULL or no value")
		}
		// A ghost is done being lazy: whatever the initializer wrote is the
		// object's state, and nothing about it is deferred any more.
		releaseInitializer(c, lazy)
		object.Lazy = nil
		return object, nil
	}
	instance := produced.Deref().Object()
	if instance == nil {
		return object, RaiseAt(c, "TypeError",
			"Lazy proxy factory must return an instance of a class compatible with %s, %s returned",
			DisplayName(object.ClassName), ValueName(produced.Deref()))
	}
	if c.proxyCompatible == nil || !c.proxyCompatible(c, object.ClassName, instance.ClassName) {
		return object, RaiseAt(c, "TypeError",
			"The real instance class %s is not compatible with the proxy class %s. "+
				"The proxy must be a instance of the same class as the real instance, "+
				"or a sub-class with no additional properties, and no overrides of the "+
				"__destructor or __clone methods.",
			DisplayName(instance.ClassName), DisplayName(object.ClassName))
	}
	if instance == object || instance.Lazy != nil {
		// Checked after compatibility, since a proxy handed back its own self is
		// trivially compatible with itself. An Error rather than a TypeError:
		// nothing about a type is wrong.
		return object, RaiseAt(c, "Error", "%s", "Lazy proxy factory must return a non-lazy object")
	}
	// The instance answers for the proxy from here on, including for the
	// accessors already running on it.
	instance.Guard = GuardOf(object)
	lazy.Instance = instance
	lazy.InstanceOwned = lazy.FreshFactory && c.instanceConfined != nil && c.instanceConfined(instance.ClassName)
	releaseInitializer(c, lazy)
	return instance, nil
}

// GuardOf is the handle an object's in-flight accessors are recorded under. A
// lazy proxy lends its own to the instance behind it, since the two are one
// object as far as a property access is concerned.
func GuardOf(object *Object) int {
	if object.Guard != 0 {
		return object.Guard
	}
	return object.ID
}
