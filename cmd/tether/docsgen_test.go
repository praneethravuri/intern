package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGoldenDocs = flag.Bool("update", false, "write regenerated docs instead of checking them")

const referenceDocPath = "../../docs/reference.md"

// TestGeneratedDocsMatchCheckedIn is docs/reference.md's drift test: the
// CLI Reference and Flags tables are generated from the live cobra command
// tree (docsgen.go), not hand-maintained, so this fails the instant a
// command or flag changes without the checked-in file being regenerated.
func TestGeneratedDocsMatchCheckedIn(t *testing.T) {
	got, err := buildReferenceDoc(newRootCmd())
	if err != nil {
		t.Fatalf("buildReferenceDoc: %v", err)
	}

	if *updateGoldenDocs {
		if err := os.WriteFile(referenceDocPath, []byte(got), 0o600); err != nil {
			t.Fatalf("write %s: %v", referenceDocPath, err)
		}
		return
	}

	want, err := os.ReadFile(filepath.Clean(referenceDocPath))
	if err != nil {
		t.Fatalf("read %s: %v (run with -update to create it)", referenceDocPath, err)
	}
	if got != string(want) {
		t.Errorf("docs/reference.md is stale -- run `go test ./cmd/tether -run TestGeneratedDocsMatchCheckedIn -update` to regenerate it")
	}
}

// TestDocsCommandCoverage catches a new top-level command that was added to
// newRootCmd but never added to docsCommandOrder, which would otherwise
// silently leave it out of docs/reference.md.
func TestDocsCommandCoverage(t *testing.T) {
	root := newRootCmd()

	documented := map[string]bool{}
	for _, path := range docsCommandOrder {
		if len(path) == 0 {
			continue
		}
		documented[path[0]] = true
	}

	for _, cmd := range root.Commands() {
		if !documented[cmd.Name()] {
			t.Errorf("newRootCmd registers %q but docsCommandOrder does not document it", cmd.Name())
			continue
		}
		if cmd.Name() != "hooks" {
			continue
		}
		for _, child := range cmd.Commands() {
			if docsOmittedHooksChildren[child.Name()] {
				continue
			}
			found := false
			for _, path := range docsCommandOrder {
				if len(path) == 2 && path[0] == "hooks" && path[1] == child.Name() {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("hooks %s is neither documented nor in docsOmittedHooksChildren", child.Name())
			}
		}
	}
}
