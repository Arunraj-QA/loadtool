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

// consoleProps are the console methods. Each writes one line:
//
//	INFO  [VU 3] message
var consoleProps = []lazyProp{
	{"log", consoleMethod("INFO ")},
	{"info", consoleMethod("INFO ")},
	{"warn", consoleMethod("WARN ")},
	{"error", consoleMethod("ERROR")},
	{"debug", consoleMethod("DEBUG")},
}

func consoleMethod(level string) func(vu *VU) goja.Value {
	return func(vu *VU) goja.Value {
		return vu.rt.ToValue(func(call goja.FunctionCall) goja.Value {
			vu.consoleWrite(level, call.Arguments)
			return goja.Undefined()
		})
	}
}

func (vu *VU) consoleWrite(level string, args []goja.Value) {
	if vu.console == nil {
		return
	}
	var b strings.Builder
	b.WriteString(level)
	b.WriteString(" [VU ")
	b.WriteString(strconv.FormatInt(vu.id, 10))
	b.WriteString("]")
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(vu.formatConsoleArg(arg))
	}
	b.WriteByte('\n')
	_, _ = io.WriteString(vu.console, b.String())
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
