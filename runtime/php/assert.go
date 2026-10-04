package php

// `assert()`'s options, which are request state: a script sets one with
// `assert_options()` and every later `assert()` reads it back. The defaults are
// the engine's for a build with `zend.assertions=1`, which is what php2go links
// against.
//
// Only the storage is here; which option number means what, and what `assert`
// does with them, is the library's.

// Assertions is the assertion configuration for one request.
type Assertions struct {
	// Active is ASSERT_ACTIVE: whether a failing assertion does anything.
	Active bool
	// Warning is ASSERT_WARNING: whether a failure also warns.
	Warning bool
	// Bail is ASSERT_BAIL: whether a failure ends the request.
	Bail bool
	// Exception is ASSERT_EXCEPTION: whether a failure throws AssertionError.
	Exception bool
	// Callback is ASSERT_CALLBACK: what a failure invokes, or null.
	Callback Value
}

// Assertions returns the request's configuration, created on first use with
// the engine's defaults.
func (c *Ctx) Assertions() *Assertions {
	if c.assertions == nil {
		c.assertions = &Assertions{
			Active: true, Warning: true, Bail: false, Exception: true,
			Callback: Null(),
		}
	}
	return c.assertions
}
