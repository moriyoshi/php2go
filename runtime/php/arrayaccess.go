package php

// The ArrayAccess methods, which are ordinary methods the engine calls on an
// object's behalf whenever it is indexed. Spelled as the declarations spell
// them; the hop lowercases if its dispatcher needs that.
const (
	OffsetGet    = "offsetGet"
	OffsetSet    = "offsetSet"
	OffsetExists = "offsetExists"
	OffsetUnset  = "offsetUnset"
)

// ArrayAccessCall dispatches one of the ArrayAccess methods on an object.
//
// It is a hop for the same reason `traversable` and `objectProps` are: indexing
// an object is core runtime behaviour, but deciding whether a class implements
// `ArrayAccess` and then *calling a method* needs the consumer's class table,
// which this package does not have.
//
// The second result is **handled**, and false means the object is not an
// ArrayAccess at all -- the caller's cue to raise `Cannot use object of type X
// as array` rather than inventing a value. The interface is what decides, not
// the presence of the methods: a class can declare `offsetGet` without
// implementing `ArrayAccess`, and PHP still refuses to index it.
type ArrayAccessCall func(ctx *Ctx, o *Object, method string, args []Value) (Value, bool, error)

// SetArrayAccess installs the hop.
func (c *Ctx) SetArrayAccess(fn ArrayAccessCall) { c.arrayAccess = fn }

// offsetCall runs one ArrayAccess method, reporting handled=false when there is
// no hop installed as well as when the object is not an ArrayAccess.
//
// With no hop the capability is honestly absent and the caller keeps whatever
// it did before, rather than every indexed object suddenly becoming an Error in
// a consumer that answers for them itself.
func offsetCall(ctx *Ctx, o *Object, method string, args []Value) (Value, bool, error) {
	if ctx == nil || ctx.arrayAccess == nil || o == nil {
		return Undef(), false, nil
	}
	return ctx.arrayAccess(ctx, o, method, args)
}

// readOffset is `$o[$k]`.
//
// The **quiet** form asks `offsetExists` first and only then `offsetGet`, so a
// missing offset answers without `offsetGet` ever seeing it. Read back from the
// engine: `isset($c["zz"])` prints one `exists` and no `get`, while
// `empty($c["a"])` prints both.
func readOffset(ctx *Ctx, o *Object, key Value, silent bool) (Value, bool, error) {
	if silent {
		exists, handled, err := offsetCall(ctx, o, OffsetExists, []Value{key})
		if err != nil || !handled {
			return Undef(), handled, err
		}
		if !exists.Deref().Bool() {
			return Null(), true, nil
		}
	}
	return offsetCall(ctx, o, OffsetGet, []Value{key})
}
