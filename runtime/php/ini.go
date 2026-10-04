package php

// INI settings as **request state**. A script may write one and read it back,
// and the value has to survive across the call -- which is the same reason the
// constant table and the error-handler stack live on the Ctx rather than in a
// package-level map.
//
// The *storage* is a hook because the interpreter already has one: a Unit
// carries the harvested registry, so a setting the build declares has a value
// there that nothing here could know. A caller with no registry falls back to
// the overlay below, which is empty until the script writes.

// IniStore is somewhere to keep the settings a request has. Read answers false
// for a setting nothing has stored, which is not the same as one whose value is
// empty.
type IniStore struct {
	Read  func(name string) (string, bool)
	Write func(name, value string)
}

// SetIniStore installs it.
func (c *Ctx) SetIniStore(store IniStore) { c.iniStore = store }

// IniRead answers what is stored for a setting, or false when nothing is.
func (c *Ctx) IniRead(name string) (string, bool) {
	if c.iniStore.Read != nil {
		return c.iniStore.Read(name)
	}
	value, held := c.ini[name]
	return value, held
}

// IniWrite records a setting for the rest of the request.
func (c *Ctx) IniWrite(name, value string) {
	if c.iniStore.Write != nil {
		c.iniStore.Write(name, value)
		return
	}
	if c.ini == nil {
		c.ini = map[string]string{}
	}
	c.ini[name] = value
}

// SourceReader hands back a file's bytes from wherever the program's sources
// really live. A bundle serves its PHP from `go:embed`, where `os.ReadFile`
// would find nothing; an ordinary program has no reader and the host answers.
//
// The second result says whether a reader was consulted at all, so "there is no
// loader" and "the loader says it is not there" stay different answers.
type SourceReader func(path string) ([]byte, error)

// SetSourceReader installs it.
func (c *Ctx) SetSourceReader(fn SourceReader) { c.readSource = fn }

// ReadSource asks it. `handled` is false when nothing is installed.
func (c *Ctx) ReadSource(path string) (data []byte, handled bool, err error) {
	if c.readSource == nil {
		return nil, false, nil
	}
	data, err = c.readSource(path)
	return data, true, err
}
