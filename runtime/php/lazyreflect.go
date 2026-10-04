package php

// The ReflectionClass options a lazy object is made or reset with. They are
// engine constants rather than anything derived, so they are written down.
const (
	LazySkipInitializationOnSerialize = 8
	LazySkipDestructor                = 16
)

// CheckLazyOptions refuses an options mask with a bit the method does not
// accept, in the engine's order: a bit outside the two known ones first
// ("contains invalid flags", which is what makes -1 an error rather than
// "everything"), then SKIP_DESTRUCTOR on newLazyGhost/newLazyProxy -- it only
// means something when there is state to destroy, so only the reset forms
// take it. `position` is the argument the diagnostic names.
func CheckLazyOptions(c *Ctx, spelled string, position int, options Value, resetting bool) error {
	mask := options.Deref().Int()
	if mask&^(LazySkipInitializationOnSerialize|LazySkipDestructor) != 0 {
		return RaiseAt(c, "ReflectionException",
			"ReflectionClass::%s(): Argument #%d ($options) contains invalid flags",
			spelled, position)
	}
	if !resetting && mask&LazySkipDestructor != 0 {
		return RaiseAt(c, "ReflectionException",
			"ReflectionClass::%s(): Argument #%d ($options) does not accept "+
				"ReflectionClass::SKIP_DESTRUCTOR", spelled, position)
	}
	return nil
}

// ApplyLazyFlags records what the caller asked for beyond deferring the state.
func ApplyLazyFlags(object *Object, options Value) {
	if object == nil || object.Lazy == nil {
		return
	}
	mask := options.Deref().Int()
	object.Lazy.SkipSerialize = mask&LazySkipInitializationOnSerialize != 0
	object.Lazy.SkipDestructor = mask&LazySkipDestructor != 0
}

// CheckLazyReset is the state half of resetAsLazyGhost/resetAsLazyProxy's
// refusals, asked after the options. An object whose initializer is running
// cannot be reset from inside it; one still waiting for its initializer has
// no state to throw away. An *initialized* one, proxy or ghost, may be reset.
func CheckLazyReset(c *Ctx, object *Object) error {
	if object == nil || object.Lazy == nil {
		return nil
	}
	if object.Lazy.Running {
		return RaiseAt(c, "Error", "%s", "Can not reset an object while it is being initialized")
	}
	if object.Lazy.Instance == nil {
		return RaiseAt(c, "ReflectionException", "%s", "Object is already lazy")
	}
	return nil
}

// DestructForReset runs an ordinary object's destructor because a reset is
// throwing its state away, which is what a destructor is for. The object lives
// on, so after a destructor that returned it is due one again when it really
// does go away. A throw is the reset's and the reset does not happen -- and,
// probed, that destructor then counts as called: unsetting the object later
// runs nothing.
func (c *Ctx) DestructForReset(o *Object) error {
	if o == nil || o.Lazy != nil || o.NoDestructor || c.destructorRun == nil || c.Destructed(o) {
		return nil
	}
	err := c.destructorRun(c, o)
	if err != nil {
		c.MarkDestructed(o)
	}
	return err
}

// LazyTarget follows a chain of *initialized proxies* to the object that holds
// the state. ReflectionProperty's lazy methods open with the engine's loop
//
//	while (zend_object_is_lazy_proxy(object) && zend_lazy_object_initialized(object)) {
//	    object = zend_lazy_object_get_instance(object);
//	}
//
// so they act on the real instance rather than the stand-in. An uninitialized
// proxy, and a ghost at any stage, is its own target.
func LazyTarget(object *Object) *Object {
	for object != nil && object.Lazy != nil && object.Lazy.Proxy && object.Lazy.Instance != nil {
		object = object.Lazy.Instance
	}
	return object
}
