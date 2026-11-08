package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/idy/tailsplit/internal/tray"
)

const desktopLogLimit = 128 << 10

// Helper stderr may bypass event redaction; apply the shared policy before
// exposing a log tail.
func desktopLogTail(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	truncated := info.Size() > desktopLogLimit
	if truncated {
		if _, err := file.Seek(-desktopLogLimit, io.SeekEnd); err != nil {
			return "", err
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, desktopLogLimit))
	if err != nil {
		return "", err
	}
	if truncated {
		// A seek can land inside a secret, past its recognizable URL/key
		// prefix. Omit the partial entry rather than expose that suffix.
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			data = data[newline+1:]
		} else {
			data = nil
		}
	}
	text := tray.RedactLog(strings.ToValidUTF8(string(data), "�"))
	// UTF-8 replacement and redaction of short keys can expand the result.
	if len(text) > desktopLogLimit {
		text = text[len(text)-desktopLogLimit:]
		for len(text) > 0 && !utf8.RuneStart(text[0]) {
			text = text[1:]
		}
	}
	return text, nil
}
