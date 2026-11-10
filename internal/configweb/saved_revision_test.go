package configweb

import (
	"context"
	"os"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestSaveCannotAcknowledgeAnExternalEditAsItsOwnRevision(t *testing.T) {
	server, path, _ := testServer(t)
	_, revision, _ := tray.ReadConfigDocument(path)
	apply := server.options.Apply
	external := []byte(fixtureConfig + "# external policy after the save\n")
	server.options.Apply = func(ctx context.Context, check func(tray.Snapshot) error, update func() error) error {
		if err := apply(ctx, check, update); err != nil {
			return err
		}
		return os.WriteFile(path, external, 0600)
	}
	status, body := request(t, server, "POST", "/api/config", map[string]any{"yaml": fixtureConfig, "revision": revision, "changes": map[string]any{"log_level": "debug"}}, nil)
	if status != 409 {
		t.Fatalf("external policy was acknowledged as a successful save: %d %s", status, body)
	}
	data, _, _ := tray.ReadConfigDocument(path)
	if string(data) != string(external) {
		t.Fatal("external edit was overwritten")
	}
}
