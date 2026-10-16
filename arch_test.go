package gconnect_test

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/danihrndzld/gconnect"

// diskImports are the packages the library must not import outside its tests. Without them it
// cannot open, write or delete a file, or read the environment.
var diskImports = map[string]bool{
	"os": true, "os/exec": true, "os/signal": true, "path/filepath": true, "io/fs": true,
	"io/ioutil": true, "syscall": true, "unsafe": true,
}

// checkModule returns every architecture rule that the module at root breaks: an import
// outside the standard library (test files included), a disk or process import in a non-test
// file, or a require or replace line in go.mod.
func checkModule(root, modPath string) ([]string, error) {
	var bad []string
	fh, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) > 0 && (f[0] == "require" || f[0] == "replace") {
			bad = append(bad, "go.mod: "+sc.Text()+"; the library has no dependencies")
		}
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		test := strings.HasSuffix(p, "_test.go")
		for _, imp := range f.Imports {
			ip := strings.Trim(imp.Path.Value, `"`)
			first, _, _ := strings.Cut(ip, "/")
			switch {
			case strings.Contains(first, ".") && ip != modPath && !strings.HasPrefix(ip, modPath+"/"):
				bad = append(bad, fmt.Sprintf("%s imports %s; the library is standard library only", rel, ip))
			case ip == "C":
				bad = append(bad, fmt.Sprintf("%s uses cgo", rel))
			case !test && diskImports[ip]:
				bad = append(bad, fmt.Sprintf("%s imports %s; the library must not touch the disk or the process", rel, ip))
			}
		}
		return nil
	})
	return bad, err
}

func TestArchitecture(t *testing.T) {
	t.Parallel()
	t.Run("CL-491 this module: standard library only, no disk or process import, no require in go.mod", func(t *testing.T) {
		t.Parallel()
		bad, err := checkModule(".", module)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bad {
			t.Error(b)
		}
	})

	write := func(t *testing.T, files map[string]string) string {
		root := t.TempDir()
		if _, ok := files["go.mod"]; !ok {
			files["go.mod"] = "module example.com/lib\n\ngo 1.24\n"
		}
		for name, body := range files {
			p := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	src := func(imports ...string) string {
		s := "package lib\n\nimport (\n"
		for _, i := range imports {
			s += "\t" + i + "\n"
		}
		return s + ")\n"
	}
	cases := []struct {
		id, want string // want: part of the reported line; "" means the fixture is clean
		files    map[string]string
	}{
		{"CL-492 a third party import in a library file: reported", "standard library only", map[string]string{"a.go": src(`"golang.org/x/net/html"`)}},
		{"CL-493 a third party import in a test file: reported", "standard library only", map[string]string{"a_test.go": src(`"github.com/stretchr/testify/assert"`)}},
		{"CL-494 os under an alias in a library file: reported", "must not touch the disk", map[string]string{"sub/a.go": src(`xos "os"`)}},
		{"CL-495 path/filepath, io/fs, syscall, unsafe, os/exec, io/ioutil: each reported", "must not touch the disk", map[string]string{"a.go": src(`_ "path/filepath"`, `_ "io/fs"`, `_ "syscall"`, `_ "unsafe"`, `_ "os/exec"`, `_ "io/ioutil"`)}},
		{"CL-496 a require line in go.mod: reported", "no dependencies", map[string]string{"go.mod": "module example.com/lib\n\ngo 1.24\n\nrequire example.com/x v1.0.0\n"}},
		{"CL-497 a replace line in go.mod: reported", "no dependencies", map[string]string{"go.mod": "module example.com/lib\n\ngo 1.24\n\nreplace example.com/x => ../x\n"}},
		{"CL-498 cgo: reported", "cgo", map[string]string{"a.go": src(`"C"`)}},
		{"CL-499 os in a test file, the module's own subpackage, net/http: clean", "", map[string]string{"a_test.go": src(`"os"`), "a.go": src(`"net/http"`, `"example.com/lib/sub"`)}},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			bad, err := checkModule(write(t, c.files), "example.com/lib")
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(bad, "\n")
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("reported %q, want %q", got, c.want)
			}
			if c.id[:6] == "CL-495" && len(bad) != 6 {
				t.Fatalf("%d of 6 imports reported: %v", len(bad), bad)
			}
		})
	}
}
