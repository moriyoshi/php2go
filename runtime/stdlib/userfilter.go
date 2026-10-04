package stdlib

import (
	"errors"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// User stream filters for compiled code: `stream_filter_register` and a
// `php_user_filter` subclass the program declares, driven entirely through
// the Ctx's hooks -- NewInstance lays the filter object out, HasMethod and
// Call reach its methods (a method the subclass does not declare is the
// engine class's own, which the generated dispatcher answers), and the
// bucket objects are numbered by the ObjectMaker. The interpreter keeps its
// own copy of this glue (runtime/interp streams.go), which dispatches these
// names before the registry is asked.
//
// Probed against the engine: onCreate runs at stream_filter_append with
// `filtername` and `params` already set (a false answer is "Unable to create
// or locate filter"); filter runs once per write or read with $closing false
// and once more with true when the filter is removed or the stream closed,
// `stream` holding the stream resource only while it runs; onClose follows
// the closing call; buckets left on the input brigade warn "Unprocessed
// filter buckets remaining on input brigade".
func init() {
	php.RegisterBuiltin("stream_filter_register", streamFilterRegister)
	php.RegisterBuiltin("stream_filter_append", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return attachUserStreamFilter(ctx, "stream_filter_append", args, true)
	})
	php.RegisterBuiltin("stream_filter_prepend", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return attachUserStreamFilter(ctx, "stream_filter_prepend", args, false)
	})
	php.RegisterBuiltin("stream_filter_remove", streamFilterRemove)
	php.RegisterBuiltin("stream_bucket_make_writeable", streamBucketMakeWriteable)
	php.RegisterBuiltin("stream_bucket_append", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return streamBucketAttach(ctx, "stream_bucket_append", args, true)
	})
	php.RegisterBuiltin("stream_bucket_prepend", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return streamBucketAttach(ctx, "stream_bucket_prepend", args, false)
	})
	php.RegisterBuiltin("stream_bucket_new", streamBucketNew)
	php.RegisterBuiltin("stream_get_filters", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		result := php.NewArray()
		for _, name := range BuiltinStreamFilterNames {
			result.Append(php.String(name))
		}
		for _, name := range ctx.StreamFilterNames() {
			result.Append(php.String(name))
		}
		return php.Arr(result), nil
	})
}

// resourceParam is the engine's parameter check for a `resource` parameter:
// anything that is not a resource at all is a TypeError naming the parameter,
// before the payload is asked about (probed: `stream_filter_append(false,
// ...)` is "must be of type resource, false given").
func resourceParam(args []php.Value, i int, function, name string) error {
	if v := userFilterArg(args, i); v.Kind() != php.KindResource {
		return php.Raise("TypeError", "%s(): Argument #%d ($%s) must be of type resource, %s given",
			function, i+1, name, php.ValueName(v))
	}
	return nil
}

func userFilterArg(args []php.Value, i int) php.Value {
	if i < len(args) {
		return args[i].Deref()
	}
	return php.Undef()
}

func streamFilterRegister(ctx *php.Ctx, args []php.Value) (php.Value, error) {
	filterName := userFilterArg(args, 0).String()
	if filterName == "" {
		return php.Undef(), php.Raise("ValueError",
			"stream_filter_register(): Argument #1 ($filter_name) must be a non-empty string")
	}
	className := userFilterArg(args, 1).String()
	if className == "" {
		return php.Undef(), php.Raise("ValueError",
			"stream_filter_register(): Argument #2 ($class) must be a non-empty string")
	}
	return php.Bool(ctx.RegisterStreamFilter(filterName, className)), nil
}

// NewStreamBucketObject is the StreamBucket object over a bucket: the
// resource the brigade functions take back, and the data the filter reads
// and writes (probed: `datalen` and `dataLength` both carry the length).
func NewStreamBucketObject(ctx *php.Ctx, bucket *StreamBucket) (php.Value, error) {
	object, ok := ctx.NewObjectOf("StreamBucket")
	if !ok {
		return php.Undef(), php.Unsupported("StreamBucket without an object maker")
	}
	resource := ctx.NewResource("userfilter.bucket", bucket)
	object.Declare("bucket", php.Declaration{Class: "StreamBucket"})
	object.Declare("data", php.Declaration{Class: "StreamBucket", Type: "string"})
	object.Declare("datalen", php.Declaration{Class: "StreamBucket", Type: "int"})
	object.Declare("dataLength", php.Declaration{Class: "StreamBucket", Type: "int"})
	object.Set("bucket", php.Res(resource))
	object.Set("data", php.String(bucket.Data))
	object.Set("datalen", php.Int(int64(len(bucket.Data))))
	object.Set("dataLength", php.Int(int64(len(bucket.Data))))
	return php.Obj(object), nil
}

func streamBucketMakeWriteable(ctx *php.Ctx, args []php.Value) (php.Value, error) {
	if err := resourceParam(args, 0, "stream_bucket_make_writeable", "brigade"); err != nil {
		return php.Undef(), err
	}
	brigade, err := BrigadeResource(userFilterArg(args, 0), "stream_bucket_make_writeable")
	if err != nil {
		return php.Undef(), err
	}
	bucket := brigade.Pop()
	if bucket == nil {
		return php.Null(), nil
	}
	return NewStreamBucketObject(ctx, bucket)
}

func streamBucketAttach(_ *php.Ctx, function string, args []php.Value, appendBucket bool) (php.Value, error) {
	if err := resourceParam(args, 0, function, "brigade"); err != nil {
		return php.Undef(), err
	}
	brigade, err := BrigadeResource(userFilterArg(args, 0), function)
	if err != nil {
		return php.Undef(), err
	}
	bucket, err := BucketFromObject(userFilterArg(args, 1).Object(), function)
	if err != nil {
		return php.Undef(), err
	}
	brigade.Attach(bucket, appendBucket)
	return php.Null(), nil
}

func streamBucketNew(ctx *php.Ctx, args []php.Value) (php.Value, error) {
	// Not the generic parameter check: stream_bucket_new fetches its
	// resource itself and says "supplied argument" for a non-resource
	// (probed with false).
	if v := userFilterArg(args, 0); v.Kind() != php.KindResource {
		return php.Undef(), php.Raise("TypeError",
			"stream_bucket_new(): supplied argument is not a valid stream resource")
	}
	if _, _, ok := AsStream(userFilterArg(args, 0)); !ok {
		return php.Undef(), php.Raise("TypeError",
			"stream_bucket_new(): supplied resource is not a valid stream resource")
	}
	return NewStreamBucketObject(ctx, &StreamBucket{Data: userFilterArg(args, 1).String()})
}

// callUserFilterMethod calls a method of the filter object, keeping the
// caller's position: a warning raised after it belongs to the caller's line.
func callUserFilterMethod(ctx *php.Ctx, object *php.Object, name string, args []php.Value) (php.Value, error) {
	callable := php.NewArray()
	callable.Append(php.Obj(object))
	callable.Append(php.String(name))
	file, line := ctx.Location()
	value, err := ctx.Call(php.Arr(callable), args)
	ctx.SetLocation(file, line)
	return value, err
}

func newHookedUserStreamFilter(ctx *php.Ctx, name, className string, params php.Value,
	held *Stream, handle *php.Resource, operation string,
) (*StreamFilter, error) {
	object, made, err := ctx.NewInstance(className)
	if err != nil {
		return nil, err
	}
	if !made {
		ctx.Warn("%s(): User-filter %q requires class %q, but that class is not defined", operation, name, className)
		return nil, nil
	}
	object.Set("filtername", php.String(name))
	if params.Kind() == php.KindUndef {
		object.Set("params", php.Null())
	} else {
		object.Set("params", params.Copy())
	}
	object.Set("stream", php.Null())
	created, err := callUserFilterMethod(ctx, object, "onCreate", nil)
	if err != nil {
		return nil, err
	}
	if created.Deref().Kind() == php.KindFalse {
		return nil, nil
	}
	filter := NewUserStreamFilter(name, held, object, className)
	filter.SetRun(func(input string, closing bool, operation string) (string, int, int, error) {
		inputBrigade := &StreamBrigade{}
		if input != "" {
			inputBrigade.Attach(&StreamBucket{Data: input}, true)
		}
		outputBrigade := &StreamBrigade{}
		inputResource := ctx.NewResource("userfilter.bucket brigade", inputBrigade)
		outputResource := ctx.NewResource("userfilter.bucket brigade", outputBrigade)
		defer inputResource.Close()
		defer outputResource.Close()
		consumed := php.NewRef(php.Int(0))
		object.Set("stream", php.Res(handle))
		status, err := callUserFilterMethod(ctx, object, "filter", []php.Value{
			php.Res(inputResource),
			php.Res(outputResource),
			php.MakeRef(consumed),
			php.Bool(closing),
		})
		object.Set("stream", php.Null())
		if err != nil {
			return "", int(consumed.Get().Int()), StreamFilterErrFatal, err
		}
		if len(inputBrigade.Buckets) != 0 {
			ctx.Warn("%s(): Unprocessed filter buckets remaining on input brigade", operation)
		}
		return outputBrigade.Text(), int(consumed.Get().Int()), int(status.Deref().Int()), nil
	})
	filter.SetClose(func(_ string) error {
		_, err := callUserFilterMethod(ctx, object, "onClose", nil)
		return err
	})
	return filter, nil
}

func attachUserStreamFilter(ctx *php.Ctx, function string, args []php.Value, appendFilter bool) (php.Value, error) {
	if err := resourceParam(args, 0, function, "stream"); err != nil {
		return php.Undef(), err
	}
	held, handle, ok := AsStream(userFilterArg(args, 0))
	if !ok {
		return php.Undef(), php.Raise("TypeError", "%s(): supplied resource is not a valid stream resource", function)
	}
	name := userFilterArg(args, 1).String()
	mode := int64(0)
	if m := userFilterArg(args, 2); m.Kind() != php.KindUndef {
		mode = m.Int()
	}
	params := userFilterArg(args, 3)
	if mode&3 == 0 {
		if strings.ContainsAny(held.Mode(), "r+") {
			mode |= StreamFilterRead
		}
		if strings.ContainsAny(held.Mode(), "wa+") {
			mode |= StreamFilterWrite
		}
	}
	var last *StreamFilter
	for _, direction := range []int64{StreamFilterRead, StreamFilterWrite} {
		if mode&direction == 0 {
			continue
		}
		var filter *StreamFilter
		located := false
		if builtin, found := NewBuiltinStreamFilter(ctx, name, params, function); found {
			located = true
			if builtin != nil {
				builtin.Stream = held
				filter = builtin
			}
		} else if className, found := ctx.StreamFilterClass(name); found {
			located = true
			made, err := newHookedUserStreamFilter(ctx, name, className, params, held, handle, function)
			if err != nil {
				return php.Undef(), err
			}
			filter = made
		}
		if !located {
			ctx.Warn("%s(): Unable to locate filter %q", function, name)
			return php.Bool(false), nil
		}
		if filter == nil {
			ctx.Warn("%s(): Unable to create or locate filter %q", function, name)
			return php.Bool(false), nil
		}
		filter.SetRead(direction == StreamFilterRead)
		held.AttachFilter(filter, appendFilter)
		last = filter
		if filter.Object != nil {
			// A stream the program leaves open is closed when the request
			// ends, and a user filter on it gets its closing call and
			// onClose then (probed).
			ctx.CloseAtRequestEnd(handle, func() error {
				err := held.Close()
				handle.Close()
				// A filter that fails its closing call is silent here and
				// the walk goes on (probed: a PSFS_ERR_FATAL filter left
				// open prints nothing and the next stream still closes).
				if errors.Is(err, ErrStreamFilterFatal) || errors.Is(err, ErrUserStreamOperation) {
					return nil
				}
				return err
			})
		}
	}
	if last == nil {
		return php.Bool(false), nil
	}
	resource := ctx.NewResource("stream filter", last)
	last.Handle = resource
	return php.Res(resource), nil
}

func streamFilterRemove(ctx *php.Ctx, args []php.Value) (php.Value, error) {
	if err := resourceParam(args, 0, "stream_filter_remove", "stream_filter"); err != nil {
		return php.Undef(), err
	}
	handle := userFilterArg(args, 0).Resource()
	if handle == nil || handle.Closed() {
		return php.Undef(), php.Raise("TypeError",
			"stream_filter_remove(): supplied resource is not a valid stream filter resource")
	}
	filter, ok := handle.Payload.(*StreamFilter)
	if !ok || filter == nil || filter.Removed() || filter.Stream == nil {
		return php.Undef(), php.Raise("TypeError",
			"stream_filter_remove(): supplied resource is not a valid stream filter resource")
	}
	if err := filter.Stream.RemoveFilter(filter, "stream_filter_remove"); err != nil {
		if errors.Is(err, ErrStreamFilterFatal) {
			ctx.Warn("stream_filter_remove(): Unable to flush filter, not removing")
			return php.Bool(false), nil
		}
		return php.Undef(), err
	}
	handle.Close()
	return php.Bool(true), nil
}
