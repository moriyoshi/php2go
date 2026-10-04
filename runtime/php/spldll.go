package php

import "strings"

// SplDoublyLinkedList, SplQueue and SplStack, natively: spl_dllist.c's list
// with its traverse pointer, step for step.
//
// **The traverse pointer is a node, not an index.** The engine keeps a
// counted reference to the element it stands on, so popping or shifting that
// element leaves the pointer on a detached node whose value is gone:
// `current()` answers null, `valid()` still true, and `next()` walks off the
// detached node's own (cleared) links. offsetUnset of that element clears the
// pointer instead. Each of those is mirrored here rather than recomputed from
// a position, because a test that mixes iteration with mutation sees the
// difference.
//
// `foreach` takes its own copy of the pointer (spl_dllist_get_iterator), so
// a loop never moves the object's own position -- unless a subclass overrides
// one of the five Iterator methods, which hands the loop to the methods
// (zend_implement_iterator); the layer above decides that.

// The iteration mode bits, as spl_dllist.h numbers them.
const (
	DllistLIFO   = 2
	DllistDelete = 1
	dllistMask   = 3
	dllistFix    = 4
)

type splNode struct {
	prev, next *splNode
	data       Value
	// gone is the engine's ZVAL_UNDEF on a node taken out of the list
	// while the traverse pointer still held it.
	gone bool
}

// SplDLL is an SplDoublyLinkedList's storage.
type SplDLL struct {
	head, tail *splNode
	count      int
	trav       *splNode
	pos        int
	flags      int
}

// SplKindOf names the SPL class a class is or derives from -- the nearest
// one up its chain. The generated program installs its own answer; a class
// this package cannot place is taken as itself.
var SplKindOf = func(class string) string { return class }

// SplCompiled reports that the program declares a body for class::name
// (folded); the generated program installs it beside SplKindOf.
var SplCompiled = func(class, name string) bool { return false }

// SplNativeHook reports that a container's own method answers
// class::method -- a list's, SplObjectStorage's, ArrayObject's or
// ArrayIterator's, none of which keeps the object anywhere -- rather than a
// body the program declared, which might.
func SplNativeHook(ctx *Ctx, class, method string) bool {
	folded := strings.ToLower(method)
	if SplCompiled(class, folded) {
		return false
	}
	switch folded {
	case "__unserialize", "unserialize":
	default:
		return false
	}
	if ctx != nil && ctx.IsArrayBackedClass(class) {
		return true
	}
	owner, _ := splOwner(SplKindOf(class), folded)
	return owner == "SplObjectStorage" || owner != "" && splContainerOf(owner) == "SplDoublyLinkedList"
}

// splDLLKinds are the classes answered here, with the flags the engine's
// create handler gives an instance (spl_dllist_object_new_ex).
var splDLLKinds = map[string]int{
	"SplDoublyLinkedList": 0,
	"SplQueue":            dllistFix,
	"SplStack":            dllistFix | DllistLIFO,
}

func init() {
	dll := map[string]string{
		"add": "add", "pop": "pop", "shift": "shift", "push": "push", "unshift": "unshift",
		"top": "top", "bottom": "bottom", "__debuginfo": "__debugInfo", "count": "count",
		"isempty": "isEmpty", "setiteratormode": "setIteratorMode", "getiteratormode": "getIteratorMode",
		"offsetexists": "offsetExists", "offsetget": "offsetGet", "offsetset": "offsetSet",
		"offsetunset": "offsetUnset", "rewind": "rewind", "current": "current", "key": "key",
		"prev": "prev", "next": "next", "valid": "valid", "unserialize": "unserialize",
		"serialize": "serialize", "__serialize": "__serialize", "__unserialize": "__unserialize",
	}
	splDeclared["SplDoublyLinkedList"] = dll
	splDeclared["SplQueue"] = map[string]string{"enqueue": "enqueue", "dequeue": "dequeue"}
	splDeclared["SplStack"] = map[string]string{}
	splParent["SplQueue"] = "SplDoublyLinkedList"
	splParent["SplStack"] = "SplDoublyLinkedList"
	SplClasses = append(SplClasses, "SplDoublyLinkedList", "SplQueue", "SplStack")
}

// SplDLLOf is a list's storage, made as the engine's create handler makes it
// when the object has none yet.
func SplDLLOf(o *Object) *SplDLL {
	if d, ok := o.Internal.(*SplDLL); ok && d != nil {
		return d
	}
	d := &SplDLL{flags: splDLLKinds[SplKindOf(o.ClassName)]}
	o.Internal = d
	return d
}

func (d *SplDLL) push(v Value) {
	n := &splNode{prev: d.tail, data: v}
	if d.tail != nil {
		d.tail.next = n
	} else {
		d.head = n
	}
	d.tail = n
	d.count++
}

func (d *SplDLL) unshift(v Value) {
	n := &splNode{next: d.head, data: v}
	if d.head != nil {
		d.head.prev = n
	} else {
		d.tail = n
	}
	d.head = n
	d.count++
}

func (d *SplDLL) pop() (Value, bool) {
	t := d.tail
	if t == nil {
		return Value{}, false
	}
	if t.prev != nil {
		t.prev.next = nil
	} else {
		d.head = nil
	}
	d.tail = t.prev
	d.count--
	v := t.data
	t.data, t.gone, t.prev = Undef(), true, nil
	return v, true
}

func (d *SplDLL) shift() (Value, bool) {
	h := d.head
	if h == nil {
		return Value{}, false
	}
	if h.next != nil {
		h.next.prev = nil
	} else {
		d.tail = nil
	}
	d.head = h.next
	d.count--
	v := h.data
	h.data, h.gone, h.next = Undef(), true, nil
	return v, true
}

func (d *SplDLL) offset(i int, backward bool) *splNode {
	cur := d.head
	if backward {
		cur = d.tail
	}
	for pos := 0; cur != nil && pos < i; pos++ {
		if backward {
			cur = cur.prev
		} else {
			cur = cur.next
		}
	}
	return cur
}

func (d *SplDLL) rewindPtr(trav **splNode, pos *int, flags int) {
	if flags&DllistLIFO != 0 {
		*pos = d.count - 1
		*trav = d.tail
	} else {
		*pos = 0
		*trav = d.head
	}
}

// forward is one step, answering the element a delete-mode step dropped.
func (d *SplDLL) forward(trav **splNode, pos *int, flags int) (Value, bool) {
	old := *trav
	if old == nil {
		return Value{}, false
	}
	if flags&DllistLIFO != 0 {
		*trav = old.prev
		*pos--
		if flags&DllistDelete != 0 {
			return d.pop()
		}
	} else {
		*trav = old.next
		if flags&DllistDelete != 0 {
			return d.shift()
		}
		*pos++
	}
	return Value{}, false
}

// splDiscardGap refuses dropping a value that holds an object with a
// destructor. The engine releases what a container lets go of on the spot,
// and the destructor runs there if that was the last reference (gh16464);
// whether it was is a refcount this runtime does not keep.
func splDiscardGap(ctx *Ctx, v Value, what string) error {
	if ctx == nil || len(ctx.destructible) == 0 {
		return nil
	}
	seen := map[*Object]bool{}
	var holds func(v Value) bool
	holds = func(v Value) bool {
		v = v.Deref()
		switch v.Kind() {
		case KindArray:
			found := false
			v.Array().Each(func(_ Key, e Value) bool {
				found = holds(e)
				return !found
			})
			return found
		case KindObject:
			o := v.Object()
			if seen[o] {
				return false
			}
			seen[o] = true
			if !ctx.Destructed(o) {
				for _, d := range ctx.destructible {
					if d == o {
						return true
					}
				}
			}
			if o.Internal != nil {
				// What an engine object holds is not walked here.
				return true
			}
			found := false
			o.PropEach(func(_ Key, e Value) bool {
				found = holds(e)
				return !found
			})
			return found
		}
		return false
	}
	if holds(v) {
		return Unsupported("%s dropping an object that may run a destructor there", what)
	}
	return nil
}

// Clone is the engine's clone: the elements copied into a list of its own,
// the pointer back at the head, the flags kept.
func (d *SplDLL) Clone() *SplDLL {
	c := &SplDLL{flags: d.flags}
	for n := d.head; n != nil; n = n.next {
		c.push(n.data)
	}
	c.trav = c.head
	return c
}

func (d *SplDLL) values() *Array {
	out := NewArray()
	for n := d.head; n != nil; n = n.next {
		out.Append(n.data)
	}
	return out
}

func dllIndex(ctx *Ctx, function string, v Value, nullable bool) (int64, bool, error) {
	declared := "int"
	if nullable {
		declared = "?int"
	}
	c, err := CoerceInternalArgument(ctx, v.Deref(), declared, function, 1, "index", false)
	if err != nil {
		return 0, false, err
	}
	if c.Kind() == KindNull {
		return 0, true, nil
	}
	return c.Int(), false, nil
}

func dllOutOfRange(ctx *Ctx, function string) error {
	return RaiseAt(ctx, "OutOfRangeException", "%s(): Argument #1 ($index) is out of range", function)
}

// splPropsTable is zend_std_get_properties as a symbol table: the object's
// initialised properties under the names `(array)` gives them.
func splPropsTable(o *Object) *Array {
	out := NewArray()
	o.PropEach(func(k Key, e Value) bool {
		if !e.Deref().IsUndef() {
			out.Set(castKey(o, k), e.Deref().Copy())
		}
		return true
	})
	return out
}

func dllMethod(ctx *Ctx, o *Object, owner, folded, spelled string, args []Value, h SplHooks) (Value, error) {
	// An alias answers as the method it aliases, under its own name.
	switch folded {
	case "enqueue":
		folded = "push"
	case "dequeue":
		folded = "shift"
	}
	least, most := 0, 0
	switch folded {
	case "push", "unshift", "setiteratormode", "offsetexists", "offsetget", "offsetunset",
		"unserialize", "__unserialize":
		least, most = 1, 1
	case "add", "offsetset":
		least, most = 2, 2
	}
	if err := splArity(ctx, owner, spelled, args, least, most); err != nil {
		return Value{}, err
	}
	fn := owner + "::" + spelled
	d := SplDLLOf(o)
	switch folded {
	case "push":
		d.push(args[0].Deref().Copy())
		return Null(), nil
	case "unshift":
		d.unshift(args[0].Deref().Copy())
		return Null(), nil
	case "pop":
		v, ok := d.pop()
		if !ok {
			return Value{}, RaiseAt(ctx, "RuntimeException", "Can't pop from an empty datastructure")
		}
		return v, nil
	case "shift":
		v, ok := d.shift()
		if !ok {
			return Value{}, RaiseAt(ctx, "RuntimeException", "Can't shift from an empty datastructure")
		}
		return v, nil
	case "top", "bottom":
		n := d.tail
		if folded == "bottom" {
			n = d.head
		}
		if n == nil {
			return Value{}, RaiseAt(ctx, "RuntimeException", "Can't peek at an empty datastructure")
		}
		return n.data.Deref(), nil
	case "count":
		return Int(int64(d.count)), nil
	case "isempty":
		// count_elements: a subclass's count() answers (probed).
		if h.Compiled != nil && h.Compiled(o.ClassName, "count") {
			v, err := h.Call(ctx, Obj(o), "count", nil)
			if err != nil {
				return Value{}, err
			}
			n := ToInt(ctx, v.Deref())
			if err := ctx.TakePendingError(); err != nil {
				return Value{}, err
			}
			return Bool(n.Int() == 0), nil
		}
		return Bool(d.count == 0), nil
	case "setiteratormode":
		c, err := CoerceInternalArgument(ctx, args[0].Deref(), "int", fn, 1, "mode", false)
		if err != nil {
			return Value{}, err
		}
		mode := int(c.Int())
		if d.flags&dllistFix != 0 && d.flags&DllistLIFO != mode&DllistLIFO {
			return Value{}, RaiseAt(ctx, "RuntimeException", "Iterators' LIFO/FIFO modes for SplStack/SplQueue objects are frozen")
		}
		d.flags = mode&dllistMask | d.flags&dllistFix
		return Int(int64(d.flags)), nil
	case "getiteratormode":
		return Int(int64(d.flags)), nil
	case "offsetexists":
		i, _, err := dllIndex(ctx, fn, args[0], false)
		if err != nil {
			return Value{}, err
		}
		return Bool(i >= 0 && i < int64(d.count)), nil
	case "offsetget":
		i, _, err := dllIndex(ctx, fn, args[0], false)
		if err != nil {
			return Value{}, err
		}
		if i < 0 || i >= int64(d.count) {
			return Value{}, dllOutOfRange(ctx, fn)
		}
		return d.offset(int(i), d.flags&DllistLIFO != 0).data.Deref(), nil
	case "offsetset":
		i, isNull, err := dllIndex(ctx, fn, args[0], true)
		if err != nil {
			return Value{}, err
		}
		if isNull {
			d.push(args[1].Deref().Copy())
			return Null(), nil
		}
		if i < 0 || i >= int64(d.count) {
			return Value{}, dllOutOfRange(ctx, fn)
		}
		n := d.offset(int(i), d.flags&DllistLIFO != 0)
		if err := splDiscardGap(ctx, n.data, fn+"()"); err != nil {
			return Value{}, err
		}
		n.data = args[1].Deref().Copy()
		return Null(), nil
	case "offsetunset":
		i, _, err := dllIndex(ctx, fn, args[0], false)
		if err != nil {
			return Value{}, err
		}
		if i < 0 || i >= int64(d.count) {
			return Value{}, dllOutOfRange(ctx, fn)
		}
		n := d.offset(int(i), d.flags&DllistLIFO != 0)
		if err := splDiscardGap(ctx, n.data, fn+"()"); err != nil {
			return Value{}, err
		}
		if n.prev != nil {
			n.prev.next = n.next
		}
		if n.next != nil {
			n.next.prev = n.prev
		}
		if n == d.head {
			d.head = n.next
		}
		if n == d.tail {
			d.tail = n.prev
		}
		n.prev, n.next = nil, nil
		d.count--
		if d.trav == n {
			d.trav = nil
		}
		n.data, n.gone = Undef(), true
		return Null(), nil
	case "add":
		i, _, err := dllIndex(ctx, fn, args[0], false)
		if err != nil {
			return Value{}, err
		}
		if i < 0 || i > int64(d.count) {
			return Value{}, dllOutOfRange(ctx, fn)
		}
		v := args[1].Deref().Copy()
		if i == int64(d.count) {
			d.push(v)
			return Null(), nil
		}
		at := d.offset(int(i), d.flags&DllistLIFO != 0)
		n := &splNode{data: v, next: at, prev: at.prev}
		if n.prev == nil {
			d.head = n
		} else {
			at.prev.next = n
		}
		at.prev = n
		d.count++
		return Null(), nil
	case "rewind":
		d.rewindPtr(&d.trav, &d.pos, d.flags)
		return Null(), nil
	case "valid":
		return Bool(d.trav != nil), nil
	case "current":
		if d.trav == nil || d.trav.gone {
			return Null(), nil
		}
		return d.trav.data.Deref(), nil
	case "key":
		return Int(int64(d.pos)), nil
	case "next", "prev":
		flags := d.flags
		if folded == "prev" {
			flags ^= DllistLIFO
		}
		if err := dllStepGap(ctx, d, d.trav, flags, fn+"()"); err != nil {
			return Value{}, err
		}
		d.forward(&d.trav, &d.pos, flags)
		return Null(), nil
	case "__debuginfo":
		return Arr(dllDebugInfo(o, d)), nil
	case "__serialize":
		out := NewArray()
		out.Append(Int(int64(d.flags)))
		out.Append(Arr(d.values()))
		out.Append(Arr(splPropsTable(o)))
		return Arr(out), nil
	case "__unserialize":
		data := args[0].Deref()
		if data.Kind() != KindArray {
			return Value{}, RaiseAt(ctx, "TypeError", "%s(): Argument #1 ($data) must be of type array, %s given", fn, ValueName(data))
		}
		flags, ok0 := data.Array().Get(IntKey(0))
		storage, ok1 := data.Array().Get(IntKey(1))
		members, ok2 := data.Array().Get(IntKey(2))
		if !ok0 || !ok1 || !ok2 || flags.Deref().Kind() != KindInt || storage.Deref().Kind() != KindArray ||
			members.Deref().Kind() != KindArray {
			return Value{}, RaiseAt(ctx, "UnexpectedValueException", "Incomplete or ill-typed serialization data")
		}
		if members.Deref().Array().Len() > 0 {
			return Value{}, Unsupported("%s::__unserialize() with properties", DisplayName(o.ClassName))
		}
		d.flags = int(int32(flags.Deref().Int()))
		storage.Deref().Array().Each(func(_ Key, v Value) bool {
			d.push(v.Deref().Copy())
			return true
		})
		return Null(), nil
	case "serialize":
		return dllSerialize(ctx, o, d)
	case "unserialize":
		return dllUnserialize(ctx, d, args[0])
	}
	return Value{}, ThrowUndefinedMethod(o.ClassName, spelled)
}

func dllDebugInfo(o *Object, d *SplDLL) *Array {
	info := splPropsTable(o)
	info.Set(StringKey(MangleProperty("SplDoublyLinkedList", "flags")), Int(int64(d.flags)))
	info.Set(StringKey(MangleProperty("SplDoublyLinkedList", "dllist")), Arr(d.values()))
	return info
}

// splSerialError is the Serializable readers' refusal of their input.
func splSerialError(ctx *Ctx, at int, text string) error {
	return RaiseAt(ctx, "UnexpectedValueException", "Error at offset %d of %d bytes", at, len(text))
}

// splWriter and splReader open a container's own serialization session.
// None comes back where the engine would join a running serialize() or
// unserialize() whose table this runtime cannot reach, which is refused.
func splWriter(ctx *Ctx, class string) (SerialWriter, error) {
	var w SerialWriter
	if NewSerialWriter != nil {
		w = NewSerialWriter(ctx)
	}
	if w == nil {
		return nil, Unsupported("%s::serialize() inside serialize(), sharing its back-references", class)
	}
	return w, nil
}

func splReader(ctx *Ctx, class, text string) (SerialReader, error) {
	var r SerialReader
	if NewSerialReader != nil {
		r = NewSerialReader(ctx, text, class+"::unserialize")
	}
	if r == nil {
		return nil, Unsupported("%s::unserialize() inside unserialize(), sharing its back-references", class)
	}
	return r, nil
}

// dllSerialize is the Serializable format: the flags, then `:` and each
// element, written as one session. An element whose serialization takes the
// next element out of the list ends the walk there, as the engine's counted
// reference to the next node does (gh16589).
func dllSerialize(ctx *Ctx, o *Object, d *SplDLL) (Value, error) {
	w, err := splWriter(ctx, "SplDoublyLinkedList")
	if err != nil {
		return Value{}, err
	}
	var b strings.Builder
	piece, err := w.Write(Int(int64(d.flags)))
	if err != nil {
		return Value{}, err
	}
	b.WriteString(piece)
	for n := d.head; n != nil; {
		b.WriteString(":")
		next := n.next
		piece, err := w.Write(n.data)
		if err != nil {
			return Value{}, err
		}
		b.WriteString(piece)
		if next != nil && next.gone {
			if next == d.trav {
				// Still held by the pointer, the engine walks on into the
				// detached node.
				return Value{}, Unsupported("SplDoublyLinkedList::serialize() walking a node its pointer holds after removal")
			}
			break
		}
		n = next
	}
	return String(b.String()), nil
}

// dllUnserialize reads the Serializable format back as one session: the
// list emptied first, the flags, then each `:`-prefixed element pushed as it
// is read, so a failure leaves what was read so far.
func dllUnserialize(ctx *Ctx, d *SplDLL, arg Value) (Value, error) {
	c, err := CoerceInternalArgument(ctx, arg.Deref(), "string", "SplDoublyLinkedList::unserialize", 1, "data", false)
	if err != nil {
		return Value{}, err
	}
	buf := c.String()
	if buf == "" {
		return Null(), nil
	}
	r, err := splReader(ctx, "SplDoublyLinkedList", buf)
	if err != nil {
		return Value{}, err
	}
	for n := d.head; n != nil; n = n.next {
		if err := splDiscardGap(ctx, n.data, "SplDoublyLinkedList::unserialize()"); err != nil {
			return Value{}, err
		}
	}
	for d.count > 0 {
		d.pop()
	}
	flags, ok, err := r.Read()
	if err != nil {
		return Value{}, err
	}
	if !ok {
		return Value{}, splSerialError(ctx, r.FailOffset(), buf)
	}
	if flags.Deref().Kind() != KindInt {
		return Value{}, splSerialError(ctx, r.At(), buf)
	}
	d.flags = int(int32(flags.Deref().Int()))
	for r.At() < len(buf) && buf[r.At()] == ':' {
		r.Skip(1)
		v, ok, err := r.Read()
		if err != nil {
			return Value{}, err
		}
		if r.SharedRefs() {
			return Value{}, Unsupported("SplDoublyLinkedList::unserialize() of data holding a reference")
		}
		if !ok {
			return Value{}, splSerialError(ctx, r.FailOffset(), buf)
		}
		d.push(v.Deref().Copy())
	}
	if r.At() < len(buf) && buf[r.At()] != 0 {
		return Value{}, splSerialError(ctx, r.At(), buf)
	}
	return Null(), nil
}

// SplDLLTraversal is the iterator `foreach` takes over a list: the object's
// pointer, position and mode copied at the start, then moved on its own.
type SplDLLTraversal struct {
	o     *Object
	d     *SplDLL
	trav  *splNode
	pos   int
	flags int
}

// NewSplDLLTraversal walks one list (spl_dllist_get_iterator).
func NewSplDLLTraversal(o *Object) *SplDLLTraversal {
	d := SplDLLOf(o)
	return &SplDLLTraversal{o: o, d: d, trav: d.trav, pos: d.pos, flags: d.flags & dllistMask}
}

func (t *SplDLLTraversal) Rewind(ctx *Ctx) error {
	t.d.rewindPtr(&t.trav, &t.pos, t.flags)
	return nil
}
func (t *SplDLLTraversal) Valid(ctx *Ctx) (bool, error) { return t.trav != nil, nil }
func (t *SplDLLTraversal) Current(ctx *Ctx) (Value, error) {
	if t.trav == nil || t.trav.gone {
		return Null(), nil
	}
	return t.trav.data.Deref(), nil
}
func (t *SplDLLTraversal) Key(ctx *Ctx) (Value, error) { return Int(int64(t.pos)), nil }
func (t *SplDLLTraversal) Next(ctx *Ctx) error {
	if err := dllStepGap(ctx, t.d, t.trav, t.flags, "foreach over "+DisplayName(t.o.ClassName)); err != nil {
		return err
	}
	t.d.forward(&t.trav, &t.pos, t.flags)
	return nil
}

// dllStepGap is splDiscardGap for the element a delete-mode step would drop.
func dllStepGap(ctx *Ctx, d *SplDLL, trav *splNode, flags int, what string) error {
	if trav == nil || flags&DllistDelete == 0 {
		return nil
	}
	n := d.head
	if flags&DllistLIFO != 0 {
		n = d.tail
	}
	if n == nil {
		return nil
	}
	return splDiscardGap(ctx, n.data, what)
}
