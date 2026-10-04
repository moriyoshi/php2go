package php

// SplHeap, SplMinHeap, SplMaxHeap and SplPriorityQueue, natively: spl_heap.c's
// array heap, sift for sift.
//
// **The order of the backing array is observable** -- `var_dump` lists the
// heap in storage order, and a user compare() that echoes sees every
// comparison -- so insertion and deletion are the engine's loops exactly,
// including the deletion loop's `limit`, which is taken before the count
// drops, and its comparison against the slot the bottom element still sits
// in.
//
// A comparison that throws does not stop the loop: the engine's comparator
// answers 0 while an exception is pending, the sift finishes on those zeros,
// and the heap is then marked corrupted. While a sift runs the heap is write
// locked, which is what a compare() that calls insert() runs into.

type splHeapElem struct {
	data, priority Value
}

// SplHeapStore is a heap's or priority queue's storage.
type SplHeapStore struct {
	elems     []splHeapElem
	pq        bool
	min       bool
	flags     int
	corrupted bool
	locked    bool
}

// The SplPriorityQueue extraction flags.
const (
	PQExtrData     = 1
	PQExtrPriority = 2
	PQExtrBoth     = 3
)

func init() {
	heap := map[string]string{
		"extract": "extract", "insert": "insert", "top": "top", "count": "count",
		"isempty": "isEmpty", "rewind": "rewind", "current": "current", "key": "key",
		"next": "next", "valid": "valid", "recoverfromcorruption": "recoverFromCorruption",
		"compare": "compare", "iscorrupted": "isCorrupted", "__debuginfo": "__debugInfo",
	}
	splDeclared["SplHeap"] = heap
	splDeclared["SplMinHeap"] = map[string]string{"compare": "compare"}
	splDeclared["SplMaxHeap"] = map[string]string{"compare": "compare"}
	pq := map[string]string{"setextractflags": "setExtractFlags", "getextractflags": "getExtractFlags"}
	for k, v := range heap {
		pq[k] = v
	}
	splDeclared["SplPriorityQueue"] = pq
	splParent["SplMinHeap"] = "SplHeap"
	splParent["SplMaxHeap"] = "SplHeap"
	SplClasses = append(SplClasses, "SplHeap", "SplMinHeap", "SplMaxHeap", "SplPriorityQueue")
}

// SplHeapOf is a heap's storage, made as the engine's create handler makes
// it when the object has none yet.
func SplHeapOf(o *Object) *SplHeapStore {
	if s, ok := o.Internal.(*SplHeapStore); ok && s != nil {
		return s
	}
	s := &SplHeapStore{}
	switch SplKindOf(o.ClassName) {
	case "SplPriorityQueue":
		s.pq, s.flags = true, PQExtrData
	case "SplMinHeap":
		s.min = true
	}
	o.Internal = s
	return s
}

// Clone copies the elements, the flags and the corruption.
func (s *SplHeapStore) Clone() *SplHeapStore {
	c := *s
	c.elems = append([]splHeapElem(nil), s.elems...)
	return &c
}

// splHeapCmp is one comparison, with the exception the engine keeps pending
// across a sift.
type splHeapCmp struct {
	ctx  *Ctx
	o    *Object
	s    *SplHeapStore
	h    SplHooks
	user bool
	err  error
}

func (c *splHeapCmp) cmp(x, y splHeapElem) int {
	if c.err != nil {
		return 0
	}
	a, b := x.data, y.data
	if c.s.pq {
		a, b = x.priority, y.priority
	}
	if c.user {
		v, err := c.h.Call(c.ctx, Obj(c.o), "compare", []Value{a, b})
		if err != nil {
			c.err = err
			return 0
		}
		n := ToInt(c.ctx, v.Deref()).Int()
		if err := c.ctx.TakePendingError(); err != nil {
			c.err = err
			return 0
		}
		switch {
		case n > 0:
			return 1
		case n < 0:
			return -1
		}
		return 0
	}
	if c.s.min {
		a, b = b, a
	}
	n, err := CompareValues(c.ctx, a, b)
	if err == nil {
		err = c.ctx.TakePendingError()
	}
	if err != nil {
		c.err = err
		return 0
	}
	return n
}

func (c *splHeapCmp) insert(e splHeapElem) error {
	s := c.s
	s.elems = append(s.elems, splHeapElem{})
	s.locked = true
	i := len(s.elems) - 1
	for ; i > 0 && c.cmp(s.elems[(i-1)/2], e) < 0; i = (i - 1) / 2 {
		s.elems[i] = s.elems[(i-1)/2]
	}
	s.locked = false
	if c.err != nil {
		s.corrupted = true
	}
	s.elems[i] = e
	return c.err
}

func (c *splHeapCmp) deleteTop() (splHeapElem, bool, error) {
	s := c.s
	count := len(s.elems)
	if count == 0 {
		return splHeapElem{}, false, nil
	}
	limit := (count - 1) / 2
	s.locked = true
	top := s.elems[0]
	count--
	bottom := s.elems[count]
	i := 0
	for j := 0; i < limit; i = j {
		j = i*2 + 1
		if j != count && c.cmp(s.elems[j+1], s.elems[j]) > 0 {
			j++
		}
		if c.cmp(bottom, s.elems[j]) < 0 {
			s.elems[i] = s.elems[j]
		} else {
			break
		}
	}
	s.locked = false
	if c.err != nil {
		s.corrupted = true
	}
	if i != count {
		s.elems[i] = bottom
	}
	s.elems = s.elems[:count]
	return top, true, c.err
}

// topGap is splDiscardGap for the top a step drops without returning it.
func (s *SplHeapStore) topGap(ctx *Ctx, what string) error {
	if len(s.elems) == 0 {
		return nil
	}
	if err := splDiscardGap(ctx, s.elems[0].data, what); err != nil {
		return err
	}
	return splDiscardGap(ctx, s.elems[0].priority, what)
}

func (s *SplHeapStore) validate(ctx *Ctx, write bool) error {
	if s.corrupted {
		return RaiseAt(ctx, "RuntimeException", "Heap is corrupted, heap properties are no longer ensured.")
	}
	if write && s.locked {
		return RaiseAt(ctx, "RuntimeException", "Heap cannot be changed when it is already being modified.")
	}
	return nil
}

func (s *SplHeapStore) extracted(e splHeapElem) Value {
	if !s.pq {
		return e.data
	}
	switch {
	case s.flags&PQExtrBoth == PQExtrBoth:
		out := NewArray()
		out.Set(StringKey("data"), e.data)
		out.Set(StringKey("priority"), e.priority)
		return Arr(out)
	case s.flags&PQExtrData != 0:
		return e.data
	}
	return e.priority
}

func heapComparer(ctx *Ctx, o *Object, s *SplHeapStore, h SplHooks) *splHeapCmp {
	// fptr_cmp: a compare() the program declares answers instead of the
	// engine's, which the create handler looked up once.
	user := h.Compiled != nil && h.Compiled(o.ClassName, "compare")
	return &splHeapCmp{ctx: ctx, o: o, s: s, h: h, user: user}
}

func heapMethod(ctx *Ctx, o *Object, owner, folded, spelled string, args []Value, h SplHooks) (Value, error) {
	least, most := 0, 0
	switch folded {
	case "insert":
		least, most = 1, 1
		if owner == "SplPriorityQueue" {
			least, most = 2, 2
		}
	case "compare":
		least, most = 2, 2
	case "setextractflags":
		least, most = 1, 1
	}
	if folded == "compare" && owner == "SplHeap" {
		return Value{}, RaiseAt(ctx, "Error", "Cannot call abstract method SplHeap::compare()")
	}
	if err := splArity(ctx, owner, spelled, args, least, most); err != nil {
		return Value{}, err
	}
	if folded == "compare" {
		a, b := args[0].Deref(), args[1].Deref()
		if owner == "SplMinHeap" {
			a, b = b, a
		}
		n, err := CompareValues(ctx, a, b)
		if err == nil {
			err = ctx.TakePendingError()
		}
		return Int(int64(n)), err
	}
	s := SplHeapOf(o)
	switch folded {
	case "count":
		return Int(int64(len(s.elems))), nil
	case "isempty":
		return Bool(len(s.elems) == 0), nil
	case "insert":
		if err := s.validate(ctx, true); err != nil {
			return Value{}, err
		}
		e := splHeapElem{data: args[0].Deref().Copy()}
		if s.pq {
			e.priority = args[1].Deref().Copy()
		}
		if err := heapComparer(ctx, o, s, h).insert(e); err != nil {
			return Value{}, err
		}
		return Bool(true), nil
	case "extract":
		if err := s.validate(ctx, true); err != nil {
			return Value{}, err
		}
		e, ok, err := heapComparer(ctx, o, s, h).deleteTop()
		if !ok {
			return Value{}, RaiseAt(ctx, "RuntimeException", "Can't extract from an empty heap")
		}
		if err != nil {
			return Value{}, err
		}
		return s.extracted(e), nil
	case "top":
		if err := s.validate(ctx, false); err != nil {
			return Value{}, err
		}
		if len(s.elems) == 0 {
			return Value{}, RaiseAt(ctx, "RuntimeException", "Can't peek at an empty heap")
		}
		return s.extracted(s.elems[0]), nil
	case "setextractflags":
		c, err := CoerceInternalArgument(ctx, args[0].Deref(), "int", owner+"::"+spelled, 1, "flags", false)
		if err != nil {
			return Value{}, err
		}
		v := int(c.Int()) & PQExtrBoth
		if v == 0 {
			return Value{}, RaiseAt(ctx, "RuntimeException", "Must specify at least one extract flag")
		}
		s.flags = v
		return Int(int64(v)), nil
	case "getextractflags":
		return Int(int64(s.flags)), nil
	case "recoverfromcorruption":
		s.corrupted = false
		return Bool(true), nil
	case "iscorrupted":
		return Bool(s.corrupted), nil
	case "key":
		return Int(int64(len(s.elems) - 1)), nil
	case "valid":
		return Bool(len(s.elems) != 0), nil
	case "rewind":
		return Null(), nil
	case "current":
		if len(s.elems) == 0 {
			return Null(), nil
		}
		return s.extracted(s.elems[0]), nil
	case "next":
		if err := s.validate(ctx, true); err != nil {
			return Value{}, err
		}
		if err := s.topGap(ctx, owner+"::next()"); err != nil {
			return Value{}, err
		}
		_, _, err := heapComparer(ctx, o, s, h).deleteTop()
		return Null(), err
	case "__debuginfo":
		return Arr(heapDebugInfo(o, s, owner)), nil
	}
	return Value{}, ThrowUndefinedMethod(o.ClassName, spelled)
}

// heapDebugInfo is spl_heap_object_get_debug_info: the properties, then the
// three private entries mangled with the class whose __debugInfo answers --
// owner, SplHeap for every heap and SplPriorityQueue for a queue -- and a
// queue's elements each shown with both data and priority.
func heapDebugInfo(o *Object, s *SplHeapStore, owner string) *Array {
	info := splPropsTable(o)
	info.Set(StringKey(MangleProperty(owner, "flags")), Int(int64(s.flags)))
	info.Set(StringKey(MangleProperty(owner, "isCorrupted")), Bool(s.corrupted))
	list := NewArray()
	for _, e := range s.elems {
		if owner == "SplPriorityQueue" {
			pair := NewArray()
			pair.Set(StringKey("data"), e.data)
			pair.Set(StringKey("priority"), e.priority)
			list.Append(Arr(pair))
		} else {
			list.Append(e.data)
		}
	}
	info.Set(StringKey(MangleProperty(owner, "heap")), Arr(list))
	return info
}

// SplHeapTraversal is the iterator `foreach` takes over a heap: it always
// stands on the top, and each step extracts it.
type SplHeapTraversal struct {
	o    *Object
	h    SplHooks
	held Value
	have bool
}

// NewSplHeapTraversal walks one heap or priority queue (spl_heap_get_iterator).
func NewSplHeapTraversal(o *Object, h SplHooks) *SplHeapTraversal {
	return &SplHeapTraversal{o: o, h: h}
}

func (t *SplHeapTraversal) Rewind(ctx *Ctx) error { return nil }
func (t *SplHeapTraversal) Valid(ctx *Ctx) (bool, error) {
	return len(SplHeapOf(t.o).elems) != 0, nil
}
func (t *SplHeapTraversal) Current(ctx *Ctx) (Value, error) {
	s := SplHeapOf(t.o)
	if err := s.validate(ctx, false); err != nil {
		return Value{}, err
	}
	if len(s.elems) == 0 {
		return Null(), nil
	}
	if !s.pq {
		return s.elems[0].data, nil
	}
	// A queue's value is extracted once per position and kept until the
	// step (spl_pqueue_it_get_current_data).
	if !t.have {
		t.held, t.have = s.extracted(s.elems[0]), true
	}
	return t.held, nil
}
func (t *SplHeapTraversal) Key(ctx *Ctx) (Value, error) {
	return Int(int64(len(SplHeapOf(t.o).elems) - 1)), nil
}
func (t *SplHeapTraversal) Next(ctx *Ctx) error {
	s := SplHeapOf(t.o)
	if err := s.validate(ctx, true); err != nil {
		return err
	}
	if err := s.topGap(ctx, "foreach over "+DisplayName(t.o.ClassName)); err != nil {
		return err
	}
	_, _, err := heapComparer(ctx, t.o, s, t.h).deleteTop()
	t.held, t.have = Value{}, false
	return err
}
