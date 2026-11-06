package configweb

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestEditorAsyncState(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is unavailable; skipping editor state regression tests")
	}
	version, err := exec.Command(node, "--version").Output()
	major, parseErr := strconv.Atoi(strings.Split(strings.TrimPrefix(strings.TrimSpace(string(version)), "v"), ".")[0])
	if err != nil || parseErr != nil || major < 18 {
		t.Skip("Node.js 18 or later is required for editor state regression tests")
	}
	if output, err := exec.Command(node, "--test", "testdata/app_test.cjs", "testdata/i18n_test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("editor state regression tests failed: %v\n%s", err, output)
	}
}
