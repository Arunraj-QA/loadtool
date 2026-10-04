package script

import "sync"

// warner reports each distinct warning once per run. It is shared by all
// VUs, so it locks; warnings are rare, so the lock is not contended.
// The callback never runs concurrently with itself.
type warner struct {
	fn   func(msg string)
	mu   sync.Mutex
	seen map[string]bool
}

func newWarner(fn func(msg string)) *warner {
	if fn == nil {
		return nil
	}
	return &warner{fn: fn, seen: make(map[string]bool)}
}

// once reports msg unless it was reported before. A nil warner discards.
func (w *warner) once(msg string) {
	if w == nil {
		return
	}
	// fn is called under the lock, so callers need not make it safe for
	// concurrent use.
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.seen[msg] {
		w.seen[msg] = true
		w.fn(msg)
	}
}
