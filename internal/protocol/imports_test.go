package protocol_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Protocol packages may not import scenario, runner, script, report,
// threshold or command-line code, nor each other (ADR-018 §1). This
// keeps protocol code isolated and independently testable.
func TestProtocolPackagesImportOnlyAllowedPackages(t *testing.T) {
	const internal = "github.com/Arunraj-QA/loadtool/internal/"
	allowed := map[string]bool{
		internal + "protocol":              true,
		internal + "protocol/protocoltest": true, // tests only
		internal + "metrics":               true,
		internal + "httpclient":            true, // protocols carried over HTTP
	}
	dirs, err := filepath.Glob(filepath.Join("..", "protocols", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no protocol packages found: %v", err)
	}
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, file := range files {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				// The package itself (for its external tests) and its own
				// sub-packages, such as a test service.
				own := internal + "protocols/" + filepath.Base(dir)
				if strings.HasPrefix(path, internal) && !allowed[path] && path != own && !strings.HasPrefix(path, own+"/") {
					t.Errorf("%s imports %s; protocol packages may only use %v", file, path, keys(allowed))
				}
			}
		}
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, strings.TrimPrefix(k, "github.com/Arunraj-QA/loadtool/"))
	}
	return out
}
