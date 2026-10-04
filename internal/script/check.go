package script

import (
	"errors"

	"github.com/dop251/goja"
)

var errInitCheck = errors.New("check is not allowed in the script's top-level code; call it inside the default function")

// check(value, { name: (v) => boolean }) runs each condition on value,
// records a pass or fail per name, and returns true if all passed
// (ADR-008). A condition that throws counts as failed and its message is
// kept for the summary; it does not end the iteration. A condition that is
// not a function is a script bug and throws a TypeError.
func (vu *VU) check(call goja.FunctionCall) goja.Value {
	rt := vu.rt
	if vu.rec == nil {
		panic(rt.NewGoError(errInitCheck))
	}
	value, sets := call.Argument(0), call.Argument(1)
	if !isSet(sets) {
		panic(rt.NewTypeError("check: the second argument must be an object of { name: condition }"))
	}
	obj := sets.ToObject(rt)
	all := true
	for _, name := range obj.Keys() {
		cond, ok := goja.AssertFunction(obj.Get(name))
		if !ok {
			panic(rt.NewTypeError("check: condition %q must be a function", name))
		}
		result, err := cond(goja.Undefined(), value)
		if err != nil {
			var interrupted *goja.InterruptedError
			if errors.As(err, &interrupted) {
				panic(err) // the run is stopping: end the iteration
			}
			vu.rec.RecordCheck(name, false, scriptErrorMessage(err))
			all = false
			continue
		}
		passed := result.ToBoolean()
		vu.rec.RecordCheck(name, passed, "")
		all = all && passed
	}
	return rt.ToValue(all)
}
