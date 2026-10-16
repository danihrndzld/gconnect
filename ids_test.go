package gconnect_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// testID matches a technique ID at the start of a quoted test or row name: "EP-301 ...".
var testID = regexp.MustCompile(`"((?:EP|BVA|CL|EG|AC)-\d+|DT-R\d+|ST-[TI]\d+) `)

// TestTestIDsAreUnique keeps the promise in CONTRIBUTING.md: every technique ID names exactly
// one test case in the repository, so a report can trace each ID to one line.
func TestTestIDsAreUnique(t *testing.T) {
	t.Run("CL-500 every technique ID names exactly one test", checkUniqueIDs)
}

func checkUniqueIDs(t *testing.T) {
	seen := map[string]string{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range testID.FindAllSubmatch(src, -1) {
			id := string(m[1])
			if prev, dup := seen[id]; dup {
				t.Errorf("%s is used in %s and in %s", id, prev, path)
			}
			seen[id] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("no test IDs found; the pattern no longer matches the test names")
	}
}
