package php_test

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// model is the ordered map a PHP array is, kept the slow obvious way.
type model struct {
	keys []php.Key
	vals map[php.Key]int64
}

func (m *model) set(k php.Key, v int64) {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

func (m *model) unset(k php.Key) {
	if _, ok := m.vals[k]; !ok {
		return
	}
	delete(m.vals, k)
	for i, x := range m.keys {
		if x == k {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			return
		}
	}
}

func (m *model) rekey(from, to php.Key) bool {
	v, ok := m.vals[from]
	if !ok || from == to {
		return false
	}
	if _, taken := m.vals[to]; taken {
		return false
	}
	delete(m.vals, from)
	m.vals[to] = v
	for i, x := range m.keys {
		if x == from {
			m.keys[i] = to
		}
	}
	return true
}

func agree(t *testing.T, step int, a *php.Array, m *model) {
	t.Helper()
	if a.Len() != len(m.keys) {
		t.Fatalf("step %d: len %d, model %d", step, a.Len(), len(m.keys))
	}
	i := 0
	a.Each(func(k php.Key, v php.Value) bool {
		if k != m.keys[i] || v.RawInt() != m.vals[k] {
			t.Fatalf("step %d: position %d is %v=%d, model %v=%d", step, i, k, v.RawInt(), m.keys[i], m.vals[m.keys[i]])
		}
		i++
		return true
	})
	for _, k := range m.keys {
		if v, ok := a.Get(k); !ok || v.RawInt() != m.vals[k] {
			t.Fatalf("step %d: lookup of %v gave %v %v, model %d", step, k, v, ok, m.vals[k])
		}
	}
}

// Random operations against the model: the index has two modes and a table
// that keeps stale slots, and each operation below moves between them.
func TestArrayIndexAgreesWithAnOrderedMap(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	key := func() php.Key {
		switch rng.Intn(4) {
		case 0:
			return php.StringKey("s" + strconv.Itoa(rng.Intn(40)))
		case 1:
			return php.IntKey(int64(rng.Intn(40)) - 5)
		default:
			return php.IntKey(int64(rng.Intn(60)))
		}
	}
	for round := 0; round < 40; round++ {
		a := php.NewArray()
		m := &model{vals: map[php.Key]int64{}}
		// Some rounds start as a list, which begins packed.
		if round%2 == 0 {
			for i := 0; i < 30; i++ {
				a.Append(php.Int(int64(i)))
				m.set(php.IntKey(int64(i)), int64(i))
			}
		}
		for step := 0; step < 400; step++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3:
				k, v := key(), rng.Int63n(1000)
				a.Set(k, php.Int(v))
				m.set(k, v)
			case 4, 5:
				k := key()
				a.Unset(k)
				m.unset(k)
			case 6:
				from, to := key(), key()
				if got, want := a.Rekey(from, to), m.rekey(from, to); got != want {
					t.Fatalf("round %d step %d: Rekey(%v, %v) = %v, model %v", round, step, from, to, got, want)
				}
			case 7:
				// A clone compacts and rebuilds; it must hold the same thing.
				c := a.Clone()
				agree(t, step, c, m)
			case 8:
				// A share, separated by a write on this side.
				b := a.Share()
				k, v := key(), rng.Int63n(1000)
				a.Set(k, php.Int(v))
				before := sharedModel(m)
				m.set(k, v)
				agree(t, step, b, before)
			default:
				if _, ok := a.Get(php.StringKey("absent")); ok {
					t.Fatalf("an absent key was found")
				}
			}
			agree(t, step, a, m)
		}
	}
}

func sharedModel(m *model) *model {
	c := &model{keys: append([]php.Key(nil), m.keys...), vals: map[php.Key]int64{}}
	for k, v := range m.vals {
		c.vals[k] = v
	}
	return c
}
