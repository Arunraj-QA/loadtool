package script

import (
	"errors"
	"math"
	"time"

	"github.com/dop251/goja"
)

var errInitSleep = errors.New("sleep is not allowed in the script's top-level code; call it inside the default function")

// newCoreModule builds the Go side of the "loadtool" module.
func (vu *VU) newCoreModule() *goja.Object {
	o := vu.rt.NewObject()
	_ = o.Set("sleep", vu.sleep)
	return o
}

// sleep(seconds) pauses this VU. It returns early when the run ends or is
// cancelled, so a sleeping VU never delays shutdown; the interrupt that
// follows then ends the iteration.
func (vu *VU) sleep(call goja.FunctionCall) goja.Value {
	if vu.ctx == nil {
		panic(vu.rt.NewGoError(errInitSleep))
	}
	arg := call.Argument(0)
	secs := arg.ToFloat()
	if !isSet(arg) || math.IsNaN(secs) || math.IsInf(secs, 0) || secs < 0 {
		panic(vu.rt.NewTypeError("sleep: seconds must be a non-negative number, got %s", arg.String()))
	}
	t := time.NewTimer(time.Duration(secs * float64(time.Second)))
	defer t.Stop()
	select {
	case <-t.C:
	case <-vu.ctx.Done():
	}
	return goja.Undefined()
}
