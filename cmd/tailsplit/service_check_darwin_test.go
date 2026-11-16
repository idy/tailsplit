//go:build darwin

package main

import (
	"bytes"
	"testing"
)

func TestInstallerServiceCheckRejectsInvalidCredentialsAndArguments(t *testing.T) {
	for _, args := range [][]string{
		{"service-check", "extra"},
		{"service-check", "--uid", "0", "--gid", "20"},
		{"service-check", "--uid", "501"},
		{"service-check", "--gid", "20"},
		{"service-check", "--uid", "4294967296", "--gid", "20"},
		{"service-check", "--uid", "501", "--gid", "4294967296"},
		{"service-check", "--uid", "-2", "--gid", "20"},
	} {
		var output bytes.Buffer
		if err := execute(args, &output, &output); err == nil {
			t.Fatalf("service-check accepted invalid installer arguments: %v", args)
		}
	}
}
