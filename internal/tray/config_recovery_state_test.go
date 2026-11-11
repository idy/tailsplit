package tray

import (
	"errors"
	"testing"
)

func TestSuccessfulIdleReconfigureClearsPriorFailure(t *testing.T) {
	for _, state := range []string{"failed", "stopped"} {
		for _, fail := range []bool{false, true} {
			controller := testController(t, "")
			controller.snapshot = Snapshot{State: state, Error: "old configuration failure"}
			failure := errors.New("backup failed")
			err := controller.Reconfigure(func() error {
				if fail {
					return failure
				}
				return nil
			})
			snapshot := controller.Snapshot()
			if fail {
				if !errors.Is(err, failure) || snapshot.Error == "" || snapshot.State != state {
					t.Fatalf("failed save cleared the diagnostic: %+v %v", snapshot, err)
				}
			} else if err != nil || snapshot.State != "stopped" || snapshot.Error != "" || snapshot.Active {
				t.Fatalf("successful recovery retained failure: %+v %v", snapshot, err)
			}
		}
	}
}
