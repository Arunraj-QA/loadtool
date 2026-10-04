package script

import (
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/dop251/goja"
)

// lockedWriter serializes writes from many VUs, so each console line is
// written whole.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// WithConsole returns a copy of p whose VUs write console output to w. All
// VUs share w through one lock; nil discards console output.
func (p *Program) WithConsole(w io.Writer) *Program {
	c := *p
	c.console = nil
	if w != nil {
		c.console = &lockedWriter{w: w}
	}
	return &c
}

// newConsole builds the VU's console object. Each call writes one line:
//
//	INFO  [VU 3] message
func (vu *VU) newConsole(out io.Writer) *goja.Object {
	o := vu.rt.NewObject()
	for _, m := range []struct{ name, level string }{
		{"log", "INFO "}, {"info", "INFO "}, {"warn", "WARN "}, {"error", "ERROR"}, {"debug", "DEBUG"},
	} {
		level := m.level
		_ = o.Set(m.name, func(call goja.FunctionCall) goja.Value {
			if out == nil {
				return goja.Undefined()
			}
			var b strings.Builder
			b.WriteString(level)
			b.WriteString(" [VU ")
			b.WriteString(strconv.FormatInt(vu.id, 10))
			b.WriteString("]")
			for _, arg := range call.Arguments {
				b.WriteByte(' ')
				b.WriteString(vu.formatConsoleArg(arg))
			}
			b.WriteByte('\n')
			_, _ = io.WriteString(out, b.String())
			return goja.Undefined()
		})
	}
	return o
}

// formatConsoleArg renders strings as they are, objects and arrays as JSON
// (as text if JSON cannot represent them, such as circular values), and
// everything else as JavaScript's String() would.
func (vu *VU) formatConsoleArg(v goja.Value) string {
	if _, isObj := v.(*goja.Object); isObj {
		if _, isFn := goja.AssertFunction(v); !isFn {
			if b, err := v.(*goja.Object).MarshalJSON(); err == nil {
				return string(b)
			}
		}
	}
	return v.String()
}
