package php

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"math"
	"time"
)

const (
	mtStateSize = 624
	mtPeriod    = 397
)

// MT19937 is the engine's Mt19937 generator, the default engine behind
// mt_rand, rand, shuffle, array_rand and str_shuffle: seeded and stepped as
// ext/random does, so a program that calls mt_srand() draws the engine's
// own sequence.
type MT19937 struct {
	state  [mtStateSize]uint32
	count  int
	legacy bool
}

func (state *MT19937) seed(seed uint32, legacy bool) {
	state.legacy = legacy
	state.state[0] = seed
	for i := 1; i < mtStateSize; i++ {
		previous := state.state[i-1]
		state.state[i] = 1812433253*(previous^(previous>>30)) + uint32(i)
	}
	state.reload()
}

func (state *MT19937) reload() {
	twist := func(m, u, v uint32) uint32 {
		lowBit := v & 1
		if state.legacy {
			lowBit = u & 1
		}
		mixed := (u & 0x80000000) | (v & 0x7fffffff)
		return m ^ (mixed >> 1) ^ (uint32(0)-lowBit)&0x9908b0df
	}
	for i := 0; i < mtStateSize-mtPeriod; i++ {
		state.state[i] = twist(state.state[i+mtPeriod], state.state[i], state.state[i+1])
	}
	for i := mtStateSize - mtPeriod; i < mtStateSize-1; i++ {
		state.state[i] = twist(state.state[i+mtPeriod-mtStateSize], state.state[i], state.state[i+1])
	}
	state.state[mtStateSize-1] = twist(
		state.state[mtPeriod-1], state.state[mtStateSize-1], state.state[0],
	)
	state.count = 0
}

func (state *MT19937) generate() uint32 {
	if state.count >= mtStateSize {
		state.reload()
	}
	value := state.state[state.count]
	state.count++
	value ^= value >> 11
	value ^= (value << 7) & 0x9d2c5680
	value ^= (value << 15) & 0xefc60000
	return value ^ (value >> 18)
}

func (state *MT19937) range32(max uint32) uint32 {
	value := state.generate()
	if max == math.MaxUint32 {
		return value
	}
	size := max + 1
	if size&(size-1) == 0 {
		return value & (size - 1)
	}
	limit := uint32(math.MaxUint32 - math.MaxUint32%uint64(size) - 1)
	for value > limit {
		value = state.generate()
	}
	return value % size
}

func (state *MT19937) range64(max uint64) uint64 {
	value := uint64(state.generate()) | uint64(state.generate())<<32
	if max == math.MaxUint64 {
		return value
	}
	size := max + 1
	if size&(size-1) == 0 {
		return value & (size - 1)
	}
	limit := uint64(math.MaxUint64 - math.MaxUint64%size - 1)
	for value > limit {
		value = uint64(state.generate()) | uint64(state.generate())<<32
	}
	return value % size
}

func (state *MT19937) rangeBetween(minimum, maximum int64) int64 {
	width := uint64(maximum) - uint64(minimum)
	var offset uint64
	if width > math.MaxUint32 {
		offset = state.range64(width)
	} else {
		offset = uint64(state.range32(uint32(width)))
	}
	return int64(offset + uint64(minimum))
}

func NewMT19937(legacy bool) *MT19937 {
	var seedBytes [4]byte
	var seed uint32
	if _, err := cryptorand.Read(seedBytes[:]); err == nil {
		seed = binary.NativeEndian.Uint32(seedBytes[:])
	} else {
		seed = uint32(time.Now().UnixNano())
	}
	state := &MT19937{}
	state.seed(seed, legacy)
	return state
}

// RandomCall answers the functions that draw from the request's default
// Mt19937 engine -- mt_srand/srand, mt_rand/rand and the library's
// `php2go\internal` shuffling leaves -- over *state, made and seeded the way
// the engine makes it on first use.
func RandomCall(ctx *Ctx, state **MT19937, name string, args []Value) (Value, bool, error) {
	randomState := func() *MT19937 {
		if *state == nil {
			*state = NewMT19937(false)
		}
		return *state
	}
	argument := func(index int) Value {
		if index < len(args) {
			return args[index].Deref()
		}
		return Null()
	}
	switch name {
	case "mt_srand", "srand":
		legacy := len(args) > 1 && argument(1).Int() == 1
		if legacy {
			ctx.Deprecated("The MT_RAND_PHP variant of Mt19937 is deprecated")
		}
		if len(args) == 0 || argument(0).Kind() == KindNull {
			(*state) = NewMT19937(legacy)
			return Null(), true, nil
		}
		(*state) = &MT19937{}
		(*state).seed(uint32(argument(0).Int()), legacy)
		return Null(), true, nil

	case "mt_rand", "rand":
		state := randomState()
		if len(args) == 0 {
			return Int(int64(state.generate() >> 1)), true, nil
		}
		minimum, maximum := argument(0).Int(), argument(1).Int()
		if maximum < minimum {
			if name == "mt_rand" {
				return Undef(), true, Raise(
					"ValueError",
					"mt_rand(): Argument #2 ($max) must be greater than or equal to argument #1 ($min)",
				)
			}
			minimum, maximum = maximum, minimum
		}
		if state.legacy {
			generated := uint64(state.generate() >> 1)
			offset := uint64(
				(float64(maximum) - float64(minimum) + 1.0) *
					(float64(generated) / 2147483648.0),
			)
			return Int(int64(offset + uint64(minimum))), true, nil
		}
		return Int(state.rangeBetween(minimum, maximum)), true, nil

	case "php2go\\internal\\array_random":
		source := argument(1).Array()
		switch argument(0).String() {
		case "key":
			used, available := source.Used(), source.Len()
			if available < used-(used>>1) {
				keys := source.Keys()
				position := randomState().rangeBetween(0, int64(available-1))
				return keys[position].Value(), true, nil
			}
			for {
				position := int(randomState().rangeBetween(0, int64(used-1)))
				if key, live := source.BucketKey(position); live {
					return key.Value(), true, nil
				}
			}
		case "permute":
			keys := source.Keys()
			result := NewArray()
			argument(2).Array().Each(func(_ Key, position Value) bool {
				at := int(position.Int())
				if at >= 0 && at < len(keys) {
					if raw, present := source.Raw(keys[at]); present {
						result.Append(CopyElement(raw))
					}
				}
				return true
			})
			return Arr(result), true, nil
		default:
			return Undef(), true, Raise(
				"Error", "php2go internal array_random received an unknown operation",
			)
		}

	case "php2go\\internal\\mt_range":
		return Int(randomState().rangeBetween(
			argument(0).Int(), argument(1).Int(),
		)), true, nil
	}
	return Undef(), false, nil
}
