package appupdate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/idy/tailsplit/internal/privatefs"
)

// Download fetches the checksums and desktop asset from the already offered
// release, verifies both size and SHA-256, and returns a private temporary file.
// The caller owns the returned Package and must call Cleanup after use.
func (u *Updater) Download(ctx context.Context) (Package, error) {
	if err := ctx.Err(); err != nil {
		return Package{}, err
	}
	u.mu.Lock()
	if u.state.Checking || u.state.Downloading {
		u.mu.Unlock()
		return Package{}, ErrBusy
	}
	if u.offer == nil {
		u.mu.Unlock()
		return Package{}, errors.New("no newer desktop release is available")
	}
	offer := *u.offer
	u.state.Downloading = true
	u.state.DownloadedBytes = 0
	u.state.TotalBytes = offer.asset.Size
	u.state.Error = ""
	u.mu.Unlock()

	pkg, err := u.download(ctx, offer)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.state.Downloading = false
	if err != nil {
		u.state.Error = err.Error()
	} else {
		u.state.Error = ""
	}
	return pkg, err
}

func (u *Updater) download(ctx context.Context, offer offeredRelease) (Package, error) {
	ctx, cancel := context.WithTimeout(ctx, u.downloadTimeout)
	defer cancel()
	expected, err := u.fetchChecksum(ctx, offer)
	if err != nil {
		return Package{}, err
	}
	if err := ctx.Err(); err != nil {
		return Package{}, err
	}
	directory, err := u.createTempDir()
	if err != nil {
		return Package{}, fmt.Errorf("create private update directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(directory)
		}
	}()
	token, err := randomToken()
	if err != nil {
		return Package{}, err
	}
	path := filepath.Join(directory, "package-"+token+filepath.Ext(offer.asset.Name))
	if err := u.writePackage(ctx, offer.asset, path, expected); err != nil {
		return Package{}, err
	}
	if err := ctx.Err(); err != nil {
		return Package{}, err
	}
	keep = true
	return Package{Path: path, Directory: directory, Version: offer.version, ownedDirectory: directory}, nil
}

func (u *Updater) fetchChecksum(ctx context.Context, offer offeredRelease) ([sha256.Size]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, u.requestTimeout)
	defer cancel()
	response, err := u.get(ctx, offer.sums.URL, true)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer response.Body.Close()
	data, err := readBounded(response, maxSumsBytes)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("read SHA256SUMS: %w", err)
	}
	if int64(len(data)) != offer.sums.Size {
		return [sha256.Size]byte{}, errors.New("SHA256SUMS size does not match release metadata")
	}
	return checksumFor(data, offer.asset.Name)
}

func checksumFor(data []byte, name string) ([sha256.Size]byte, error) {
	var expected [sha256.Size]byte
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 16<<10)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 67 || line[64:66] != "  " {
			return expected, errors.New("SHA256SUMS contains a malformed checksum line")
		}
		filename := line[66:]
		if strings.TrimSpace(filename) != filename || strings.ContainsAny(filename, "/\\") ||
			strings.IndexFunc(filename, unicode.IsControl) >= 0 {
			return expected, errors.New("SHA256SUMS contains an invalid filename")
		}
		digest, err := hex.DecodeString(line[:64])
		if err != nil || len(digest) != sha256.Size {
			return expected, errors.New("SHA256SUMS contains an invalid SHA-256 digest")
		}
		if seen[filename] {
			return expected, errors.New("SHA256SUMS contains a duplicate filename")
		}
		seen[filename] = true
		if filename == name {
			copy(expected[:], digest)
		}
	}
	if err := scanner.Err(); err != nil {
		return expected, fmt.Errorf("read SHA256SUMS line: %w", err)
	}
	if !seen[name] {
		return expected, errors.New("SHA256SUMS does not contain the exact desktop asset filename")
	}
	return expected, nil
}

func (u *Updater) createTempDir() (string, error) {
	root, err := filepath.Abs(u.tempRoot)
	if err != nil {
		return "", err
	}
	for range 5 {
		token, err := randomToken()
		if err != nil {
			return "", err
		}
		directory := filepath.Join(root, "tailsplit-update-"+token)
		if _, err := os.Lstat(directory); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		// EnsureDir creates the directory with a protected Windows ACL at
		// creation time. MkdirTemp would first expose an inherited DACL.
		if err := privatefs.EnsureDir(directory); err != nil {
			return "", err
		}
		u.mu.Lock()
		if u.issued == nil {
			u.issued = make(map[string]bool)
		}
		u.issued[directory] = true
		u.mu.Unlock()
		return directory, nil
	}
	return "", errors.New("could not allocate a unique private update directory")
}

// removeStaleDownloads deletes private update directories left by an earlier
// process, such as the Setup EXE that Windows ran after quitting the app.
// Packages this Updater issued, including one still being installed, stay.
func (u *Updater) removeStaleDownloads() {
	root, err := filepath.Abs(u.tempRoot)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		token, ok := strings.CutPrefix(entry.Name(), "tailsplit-update-")
		if !ok || !entry.IsDir() || len(token) != 32 || strings.Trim(token, "0123456789abcdef") != "" {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		u.mu.Lock()
		issued := u.issued[directory]
		u.mu.Unlock()
		if !issued {
			_ = os.RemoveAll(directory)
		}
	}
}

func randomToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("create random update name: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}

func (u *Updater) writePackage(ctx context.Context, asset releaseAsset, path string, expected [sha256.Size]byte) error {
	response, err := u.get(ctx, asset.URL, true)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 && response.ContentLength != asset.Size {
		return errors.New("desktop package size does not match release metadata")
	}
	file, err := privatefs.CreateFile(path)
	if err != nil {
		return fmt.Errorf("create private update file: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	progress := &progressWriter{updater: u, writer: io.MultiWriter(file, hash)}
	written, err := io.Copy(progress, io.LimitReader(response.Body, asset.Size+1))
	if err != nil {
		return fmt.Errorf("download desktop package: %w", err)
	}
	if written != asset.Size {
		return errors.New("desktop package size does not match release metadata")
	}
	if !bytes.Equal(hash.Sum(nil), expected[:]) {
		return errors.New("desktop package SHA-256 checksum does not match SHA256SUMS")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("flush desktop package: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close desktop package: %w", err)
	}
	if err := privatefs.CheckFile(path); err != nil {
		return fmt.Errorf("check private update file: %w", err)
	}
	return nil
}

type progressWriter struct {
	updater *Updater
	writer  io.Writer
}

func (w *progressWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	w.updater.mu.Lock()
	w.updater.state.DownloadedBytes += int64(written)
	w.updater.mu.Unlock()
	return written, err
}
