package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinUpdateArchiveRejectsUnsafeBundles(t *testing.T) {
	required := []string{"TailSplit.app/Contents/Info.plist", "TailSplit.app/Contents/MacOS/tailsplit-tray", "TailSplit.app/Contents/MacOS/tailsplit", "TailSplit.app/Contents/Resources/TailSplit Network Service.pkg"}
	for _, test := range []struct {
		name, extra string
		mode        os.FileMode
		omit        bool
		valid       bool
	}{
		{name: "complete", valid: true},
		{name: "resource forks", extra: "__MACOSX/._TailSplit.app", valid: true},
		{name: "traversal", extra: "TailSplit.app/../../outside"},
		{name: "backslash", extra: `TailSplit.app/Contents\outside`},
		{name: "outside bundle", extra: "Other.app/Contents/Info.plist"},
		{name: "duplicate", extra: required[0]},
		{name: "symlink", extra: "TailSplit.app/Contents/link", mode: os.ModeSymlink | 0o777},
		{name: "device", extra: "TailSplit.app/Contents/device", mode: os.ModeDevice | 0o600},
		{name: "missing service", omit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "update.zip")
			file, err := os.Create(filename)
			if err != nil {
				t.Fatal(err)
			}
			archive := zip.NewWriter(file)
			add := func(name string, mode os.FileMode) {
				header := &zip.FileHeader{Name: name, Method: zip.Store}
				header.SetMode(mode)
				entry, err := archive.CreateHeader(header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte("fixture")); err != nil {
					t.Fatal(err)
				}
			}
			for i, name := range required {
				if !test.omit || i != len(required)-1 {
					add(name, 0o644)
				}
			}
			if test.extra != "" {
				add(test.extra, test.mode)
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			if err := validateDarwinUpdateArchive(filename); (err == nil) != test.valid {
				t.Fatalf("valid=%v, error=%v", test.valid, err)
			}
		})
	}
}
