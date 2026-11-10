package tray

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func preparationController(t *testing.T) *Controller {
	t.Helper()
	directory := t.TempDir()
	c, err := New(Options{Helper: "unused", ConfigPath: filepath.Join(directory, "config.yaml"), LogPath: filepath.Join(directory, "app.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestServicePreparationPublishesCancelableState(t *testing.T) {
	c := preparationController(t)
	ready, result := make(chan struct{}), make(chan error, 1)
	go func() {
		result <- c.PrepareService(context.Background(), func(ctx context.Context) error {
			close(ready)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	<-ready
	snapshot := c.Snapshot()
	if !snapshot.PreparingService() || snapshot.State != "authorizing" || snapshot.LoginURL != "" {
		t.Fatal(snapshot)
	}
	if err := c.Start(); err == nil {
		t.Fatal("network start bypassed preparation")
	}
	if err := c.beginReconfigure(); err == nil {
		t.Fatal("settings bypassed preparation")
	}
	if !c.CancelServicePreparation() {
		t.Fatal("preparation could not be canceled")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	snapshot = c.Snapshot()
	if snapshot.Active || snapshot.State != "stopped" || snapshot.Error != "" || c.CancelServicePreparation() {
		t.Fatal(snapshot)
	}
}

func TestStopWaitsForServicePreparationCleanup(t *testing.T) {
	c := preparationController(t)
	ready, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result, stopped := make(chan error, 1), make(chan error, 1)
	go func() {
		result <- c.PrepareService(context.Background(), func(ctx context.Context) error {
			close(ready)
			<-ctx.Done()
			close(canceled)
			<-release
			return ctx.Err()
		})
	}()
	<-ready
	go func() { stopped <- c.Stop() }()
	<-canceled
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before cleanup: %v", err)
	default:
	}
	if snapshot := c.Snapshot(); !snapshot.PreparingService() || snapshot.State != "stopping" {
		t.Fatal(snapshot)
	}
	close(release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if c.Snapshot().Active {
		t.Fatal("preparation remains active after cleanup")
	}
}

func TestServicePreparationFailureReleasesGate(t *testing.T) {
	c := preparationController(t)
	failure := errors.New("fixture installer failure")
	if err := c.PrepareService(context.Background(), func(context.Context) error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if snapshot := c.Snapshot(); snapshot.Active || snapshot.State != "failed" || snapshot.Error == "" {
		t.Fatal(snapshot)
	}
	if err := c.PrepareService(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal("retry stayed busy:", err)
	}
	if snapshot := c.Snapshot(); snapshot.State != "stopped" || snapshot.Error != "" || snapshot.Active {
		t.Fatal("successful repair retained its old error:", snapshot)
	}
}
