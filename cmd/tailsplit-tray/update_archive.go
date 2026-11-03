package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
)

// Inspect the complete archive before ditto can write files or resource forks.
// Current release bundles contain no links or special files.
func validateDarwinUpdateArchive(filename string) error {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer archive.Close()
	if len(archive.File) > 10000 {
		return errors.New("update archive contains too many files")
	}
	seen := make(map[string]bool)
	var total uint64
	for _, file := range archive.File {
		name := file.Name
		clean := strings.TrimSuffix(name, "/")
		if (!strings.HasPrefix(name, "TailSplit.app/") && !strings.HasPrefix(name, "__MACOSX/")) ||
			path.Clean(clean) != clean || strings.Contains(name, "\\") ||
			strings.IndexFunc(name, unicode.IsControl) >= 0 || seen[clean] ||
			(!file.Mode().IsRegular() && !file.Mode().IsDir()) {
			return fmt.Errorf("unsafe update archive entry %q", name)
		}
		seen[clean] = true
		if file.UncompressedSize64 > 1<<30 {
			return errors.New("update archive entry exceeds 1 GiB")
		}
		total += file.UncompressedSize64
		if total > 2<<30 {
			return errors.New("expanded update exceeds 2 GiB")
		}
	}
	for _, name := range []string{"TailSplit.app/Contents/Info.plist", "TailSplit.app/Contents/MacOS/tailsplit-tray", "TailSplit.app/Contents/MacOS/tailsplit", "TailSplit.app/Contents/Resources/TailSplit Network Service.pkg"} {
		if !seen[name] {
			return fmt.Errorf("update archive is missing %s", name)
		}
	}
	return nil
}
