package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestStartupPreparesServiceBeforeRestoringSession(t *testing.T) {
	for _, noConnect := range []bool{false, true} {
		var calls []string
		var start func() error
		if !noConnect {
			start = func() error { calls = append(calls, "restore"); return nil }
		}
		err := prepareDesktopStartup(context.Background(), func(context.Context) error { calls = append(calls, "prepare"); return nil }, start)
		want := []string{"prepare"}
		if !noConnect {
			want = append(want, "restore")
		}
		if err != nil || !reflect.DeepEqual(calls, want) {
			t.Fatal(calls, err)
		}
	}
}

func TestStartupServiceFailureDoesNotStartNetworkOrLogin(t *testing.T) {
	for _, failure := range []error{context.Canceled, errors.New("installer failed")} {
		started := false
		err := prepareDesktopStartup(context.Background(), func(context.Context) error { return failure }, func() error { started = true; return nil })
		if !errors.Is(err, failure) || started {
			t.Fatal("startup continued after service preparation failed", err)
		}
	}
}
