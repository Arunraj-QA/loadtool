package script

import (
	"maps"
	"slices"

	"github.com/dop251/goja"
)

// envObject is a VU's __ENV. It reads from one map shared by all VUs, which
// is never written after the run starts and so is safe to read
// concurrently; copying every variable into each of 1,000 VUs would cost
// memory. Writes and deletes by the script go into a per-VU overlay,
// allocated only when first needed, so VUs cannot see each other's changes.
type envObject struct {
	rt      *goja.Runtime
	base    map[string]string     // shared, read-only
	overlay map[string]goja.Value // this VU's writes
	deleted map[string]bool       // base keys this VU deleted
}

var _ goja.DynamicObject = (*envObject)(nil)

func (e *envObject) Get(key string) goja.Value {
	if v, ok := e.overlay[key]; ok {
		return v
	}
	if e.deleted[key] {
		return nil
	}
	if v, ok := e.base[key]; ok {
		return e.rt.ToValue(v)
	}
	return nil
}

func (e *envObject) Set(key string, val goja.Value) bool {
	if e.overlay == nil {
		e.overlay = make(map[string]goja.Value)
	}
	e.overlay[key] = val
	delete(e.deleted, key)
	return true
}

func (e *envObject) Has(key string) bool {
	if _, ok := e.overlay[key]; ok {
		return true
	}
	_, ok := e.base[key]
	return ok && !e.deleted[key]
}

func (e *envObject) Delete(key string) bool {
	delete(e.overlay, key)
	if _, ok := e.base[key]; ok {
		if e.deleted == nil {
			e.deleted = make(map[string]bool)
		}
		e.deleted[key] = true
	}
	return true
}

// Keys returns the visible keys in sorted order, so iteration and
// JSON.stringify(__ENV) are deterministic.
func (e *envObject) Keys() []string {
	keys := make([]string, 0, len(e.base)+len(e.overlay))
	for k := range e.base {
		if !e.deleted[k] {
			if _, shadowed := e.overlay[k]; !shadowed {
				keys = append(keys, k)
			}
		}
	}
	keys = slices.AppendSeq(keys, maps.Keys(e.overlay))
	slices.Sort(keys)
	return keys
}
