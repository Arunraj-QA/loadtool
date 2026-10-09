package script

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	greeterproto "github.com/Arunraj-QA/loadtool/examples/proto"
)

// Every complete script in the documentation (a TypeScript block with
// imports and a default function) loads with the current implementation,
// so the docs cannot show an API that does not exist. Fragments, such as
// an options object on its own, are skipped.
func TestDocSnippetsLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "docs")
	if d := os.Getenv("LOADTOOL_DOCS_DIR"); d != "" {
		dir = d // lets a test run over a CRLF copy of the docs
	}
	docs, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Windows checkouts may have CRLF line endings.
	block := regexp.MustCompile("(?s)```typescript\r?\n(.*?)```")
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
				dir := t.TempDir()
				path := filepath.Join(dir, "snippet.ts")
				if err := os.WriteFile(path, []byte(code), 0o644); err != nil {
					t.Fatal(err)
				}
				// Snippets load proto/greeter.proto, as the examples do.
				if err := os.MkdirAll(filepath.Join(dir, "proto"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "proto", "greeter.proto"), []byte(greeterproto.Greeter), 0o644); err != nil {
					t.Fatal(err)
				}
				p := loadWithModules(t, path)
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
