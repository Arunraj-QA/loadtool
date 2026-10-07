package script

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every complete script in the documentation (a TypeScript block with
// imports and a default function) loads with the current implementation,
// so the docs cannot show an API that does not exist. Fragments, such as
// an options object on its own, are skipped.
func TestDocSnippetsLoad(t *testing.T) {
	docs, err := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile("(?s)```typescript\n(.*?)```")
	var loaded int
	for _, doc := range docs {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		for i, m := range block.FindAllStringSubmatch(string(b), -1) {
			code := m[1]
			if !strings.Contains(code, "import ") || !strings.Contains(code, "export default function") {
				continue
			}
			name := filepath.Base(doc) + "#" + string(rune('a'+i))
			t.Run(name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "snippet.ts")
				if err := os.WriteFile(path, []byte(code), 0o644); err != nil {
					t.Fatal(err)
				}
				p, err := Load(path)
				if err != nil {
					t.Fatalf("Load: %v\n%s", err, code)
				}
				l, err := p.NewLifecycle(context.Background())
				if err != nil {
					t.Fatalf("NewLifecycle: %v", err)
				}
				if _, err := l.Options(); err != nil {
					t.Fatalf("Options: %v", err)
				}
				if _, err := p.NewVU(context.Background(), 1, http.DefaultClient); err != nil {
					t.Fatalf("NewVU: %v", err)
				}
			})
			loaded++
		}
	}
	if loaded < 3 {
		t.Fatalf("only %d complete snippets found; the pattern may be wrong", loaded)
	}
}
