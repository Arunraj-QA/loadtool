package ui

import (
	"sort"
	"strings"
)

// minSecret is the shortest value redacted: shorter ones would mask
// ordinary text, and are too short to be credentials.
const minSecret = 3

// redactor replaces secret values with *** in text the controller shows,
// logs or returns.
type redactor struct{ secrets []string }

func newRedactor(secrets []string) redactor {
	var keep []string
	for _, s := range secrets {
		if len(s) >= minSecret {
			keep = append(keep, s)
		}
	}
	// Longest first, so a secret that contains another is masked whole.
	sort.Slice(keep, func(i, j int) bool { return len(keep[i]) > len(keep[j]) })
	return redactor{keep}
}

func (r redactor) text(s string) string {
	for _, v := range r.secrets {
		s = strings.ReplaceAll(s, v, "***")
	}
	return s
}

func (r redactor) bytes(b []byte) []byte {
	if len(r.secrets) == 0 {
		return b
	}
	return []byte(r.text(string(b)))
}
