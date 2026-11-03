// Package appupdate checks public TailSplit releases and downloads verified
// desktop packages. It never installs or executes downloaded content.
package appupdate

import (
	"errors"
	"os"
	"time"
)

// ErrBusy means another check or download is already in progress.
var ErrBusy = errors.New("an update operation is already in progress")

// State is a copy of the updater's current status. Installing is reserved for
// the desktop coordinator to set on its own snapshot copies.
type State struct {
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   string    `json:"latest_version"`
	Automatic       bool      `json:"automatic"`
	Checking        bool      `json:"checking"`
	Downloading     bool      `json:"downloading"`
	Installing      bool      `json:"installing"`
	Available       bool      `json:"available"`
	DownloadedBytes int64     `json:"downloaded_bytes"`
	TotalBytes      int64     `json:"total_bytes"`
	LastChecked     time.Time `json:"last_checked"`
	Error           string    `json:"error"`
	ReleaseURL      string    `json:"release_url"`
	ReleaseNotes    string    `json:"release_notes"`
}

// Package owns a private temporary directory containing a verified desktop
// archive or installer. Version omits the release tag's optional "v" prefix.
// The caller must call Cleanup after it has finished using the package.
type Package struct {
	Path      string
	Directory string
	Version   string

	ownedDirectory string
}

// Cleanup removes only the temporary directory created for this package.
// It is safe to call more than once, including on a zero Package.
func (p Package) Cleanup() error {
	if p.ownedDirectory == "" {
		return nil
	}
	return os.RemoveAll(p.ownedDirectory)
}
