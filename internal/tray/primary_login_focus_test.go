package tray

import (
	"slices"
	"testing"

	"github.com/idy/tailsplit"
)

func TestPrimaryLoginPassesExplicitMainDeviceFocus(t *testing.T) {
	controller := testController(t, "manual-events")
	if err := controller.StartLogin(); err != nil {
		t.Fatal(err)
	}
	controller.mu.Lock()
	args := slices.Clone(controller.command.Args)
	controller.mu.Unlock()
	index := slices.Index(args, "--exit")
	if index < 0 || index+1 >= len(args) || args[index+1] != tailsplit.MainExit || controller.Snapshot().Focus != tailsplit.MainExit {
		t.Fatalf("primary login did not focus its own device: %v", args)
	}
	waitState(t, controller, "connecting")
	controller.onEvent([]byte(`{"event":"authenticated","mode":"login"}`))
	snapshot := controller.Snapshot()
	if snapshot.BackendState != "Running" {
		t.Fatal("focused main login did not update the primary identity", snapshot)
	}
	if _, duplicate := snapshot.Exits[tailsplit.MainExit]; duplicate {
		t.Fatal("primary login created a phantom additional profile")
	}
	flow := LoginFlow{}
	flow.Request()
	if flow.Next(snapshot) != "" || flow.pending {
		t.Fatal("primary login flow ignored the authenticated primary device")
	}
	if err := controller.Stop(); err != nil {
		t.Fatal(err)
	}
}
