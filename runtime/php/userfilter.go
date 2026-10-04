package php

// php_user_filter and StreamBucket, the two classes the user stream filter
// API is made of. runtime/stdlib userfilter.go drives a program's subclass;
// this is what `new` lays out and what the engine's own methods answer when a
// subclass leaves them alone (probed).

// NewUserFilterObject lays out one of the two as `new` does: their declared
// properties exist from allocation, since neither has a constructor.
// php_user_filter's are a string filtername and a mixed params, both the
// empty string, and
// an untyped `$stream = NULL`; StreamBucket's `$bucket` is NULL and its typed
// `data`, `datalen` and `dataLength` start uninitialised.
func NewUserFilterObject(class string, id int) *Object {
	o := NewObject(class, id)
	switch class {
	case "php_user_filter":
		o.Declare("filtername", Declaration{Class: class, Type: "string"})
		o.Declare("params", Declaration{Class: class, Type: "mixed"})
		o.Declare("stream", Declaration{Class: class})
		o.Set("filtername", String(""))
		o.Set("params", String(""))
		o.Set("stream", Null())
	case "StreamBucket":
		o.Declare("bucket", Declaration{Class: class})
		o.Set("bucket", Null())
		o.Declare("data", Declaration{Class: class, Type: "string"})
		o.Declare("datalen", Declaration{Class: class, Type: "int"})
		o.Declare("dataLength", Declaration{Class: class, Type: "int"})
	}
	return o
}

// UserFilterDefault is php_user_filter's own method, folded name in lower:
// filter answers PSFS_ERR_FATAL (0) having taken nothing from $in, onCreate
// true and onClose nothing. filter's parameters are checked as the engine's
// arginfo has them: two resources, a by-reference int and a bool.
func UserFilterDefault(ctx *Ctx, lower string, args []Value) (Value, error) {
	switch lower {
	case "filter":
		if msg, bad := BuiltinArityError("php_user_filter::filter", len(args), 4, 4); bad {
			return Value{}, RaiseAt(ctx, "ArgumentCountError", "%s", msg)
		}
		for i, name := range []string{"in", "out"} {
			if v := args[i].Deref(); v.Kind() != KindResource {
				return Value{}, RaiseAt(ctx, "TypeError",
					"php_user_filter::filter(): Argument #%d ($%s) must be of type resource, %s given",
					i+1, name, TypeName(v))
			}
		}
		return Int(0), nil
	case "oncreate":
		if msg, bad := BuiltinArityError("php_user_filter::onCreate", len(args), 0, 0); bad {
			return Value{}, RaiseAt(ctx, "ArgumentCountError", "%s", msg)
		}
		return Bool(true), nil
	case "onclose":
		if msg, bad := BuiltinArityError("php_user_filter::onClose", len(args), 0, 0); bad {
			return Value{}, RaiseAt(ctx, "ArgumentCountError", "%s", msg)
		}
		return Null(), nil
	}
	return Value{}, Unsupported("internal method php_user_filter::%s", lower)
}

// requestEndCloser is a resource whose closing runs user code -- a stream
// carrying a user filter, whose closing call and onClose the engine makes
// when the request ends and its resource list is freed.
type requestEndCloser struct {
	resource *Resource
	close    func() error
}

// CloseAtRequestEnd registers r to be closed by CloseRequestResources unless
// the program closed it first. Registering it again keeps the first.
func (c *Ctx) CloseAtRequestEnd(r *Resource, close func() error) {
	if r == nil {
		return
	}
	for _, held := range c.requestEndClosers {
		if held.resource == r {
			return
		}
	}
	c.requestEndClosers = append(c.requestEndClosers, requestEndCloser{resource: r, close: close})
}

// CloseRequestResources closes what CloseAtRequestEnd registered and is still
// open, highest handle first, which is the order the engine frees its
// resource list in (probed: two filtered streams left open close the later
// one's filter first). The first error stops the walk and is returned.
func (c *Ctx) CloseRequestResources() error {
	held := c.requestEndClosers
	c.requestEndClosers = nil
	for i := 1; i < len(held); i++ {
		for j := i; j > 0 && held[j].resource.ID > held[j-1].resource.ID; j-- {
			held[j], held[j-1] = held[j-1], held[j]
		}
	}
	for _, h := range held {
		if h.resource.Closed() {
			continue
		}
		if err := h.close(); err != nil {
			return err
		}
	}
	return nil
}
