package script

import (
	"github.com/dop251/goja"
)

// lazyProp is one property of a lazyObject: its name and how a VU builds
// its value.
type lazyProp struct {
	name  string
	build func(vu *VU) goja.Value
}

// lazyObject is a JavaScript object with a fixed set of properties that are
// built on first access. A function object costs about 750 bytes in every
// VU, native or JavaScript alike, so building console methods and built-in
// modules lazily means a VU only pays for what its script uses. Properties
// can be replaced (console.log = ...) but not added or deleted.
type lazyObject struct {
	vu    *VU
	props []lazyProp   // shared by all VUs, never modified
	vals  []goja.Value // this VU's values, by index into props; nil until first use
}

var _ goja.DynamicObject = (*lazyObject)(nil)

func (vu *VU) newLazyObject(props []lazyProp) *goja.Object {
	return vu.rt.NewDynamicObject(&lazyObject{vu: vu, props: props})
}

// index runs on every property access, such as each console.log call; a
// linear scan of a few names is cheaper than a map and allocates nothing.
func (o *lazyObject) index(key string) int {
	for i := range o.props {
		if o.props[i].name == key {
			return i
		}
	}
	return -1
}

func (o *lazyObject) Get(key string) goja.Value {
	i := o.index(key)
	if i < 0 {
		return nil
	}
	if o.vals == nil {
		o.vals = make([]goja.Value, len(o.props))
	}
	if o.vals[i] == nil {
		o.vals[i] = o.props[i].build(o.vu)
	}
	return o.vals[i]
}

func (o *lazyObject) Set(key string, val goja.Value) bool {
	i := o.index(key)
	if i < 0 {
		return false
	}
	if o.vals == nil {
		o.vals = make([]goja.Value, len(o.props))
	}
	o.vals[i] = val
	return true
}

func (o *lazyObject) Has(key string) bool { return o.index(key) >= 0 }

func (o *lazyObject) Delete(string) bool { return false }

func (o *lazyObject) Keys() []string {
	keys := make([]string, len(o.props))
	for i, p := range o.props {
		keys[i] = p.name
	}
	return keys
}
