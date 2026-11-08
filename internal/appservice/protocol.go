// Package appservice implements the local, authenticated control channel for
// the macOS app's installed network service. It never executes client commands.
package appservice

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/idy/tailsplit"
)

const (
	Protocol       = 1
	Version        = "0.5.1"
	Label          = "im.lbv.tailsplit.helper"
	SocketPath     = "/var/run/im.lbv.tailsplit.sock"
	ExecutablePath = "/Library/PrivilegedHelperTools/im.lbv.tailsplit.helper"
	SupportPath    = "/Library/Application Support/TailSplit"
	InstallPackage = "TailSplit Network Service.pkg"
	RemovePackage  = "Remove TailSplit Network Service.pkg"
	maxRequest     = 20 << 20
	maxState       = 1 << 20
	maxCache       = 8 << 20
	maxRules       = 2 << 20
	maxExits       = 8
)

// validExitID mirrors the configuration rule; the service rechecks it before
// using an ID as a directory name.
var exitIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

func validExitID(id string) bool { return id != tailsplit.MainExit && exitIDPattern.MatchString(id) }

// Files contains bounded copies read by the ordinary user's process. The root
// service never opens a config, state, rules file or executable supplied by it.
type Files struct {
	TailscaleState []byte `json:"tailscale_state,omitempty"`
	// ExitStates holds additional exits' saved logins, keyed by exit ID.
	ExitStates map[string][]byte `json:"exit_states,omitempty"`
	Cache      []byte            `json:"cache,omitempty"`
	Rules      []byte            `json:"rules,omitempty"`
}

type Request struct {
	Protocol   int               `json:"protocol"`
	Op         string            `json:"op"`
	Target     string            `json:"target,omitempty"`
	ActiveOnly bool              `json:"active_only,omitempty"`
	Config     *tailsplit.Config `json:"config,omitempty"`
	Files      Files             `json:"files,omitempty"`
}

type Info struct {
	Event         string `json:"event"`
	Protocol      int    `json:"protocol"`
	Version       string `json:"version"`
	BuildID       string `json:"build_id"`
	Initialized   bool   `json:"initialized"`
	PingAvailable bool   `json:"ping_available"`
}

func executableID(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Read exactly one bounded line, keeping any following stop/EOF in reader.
// A streaming JSON decoder can otherwise swallow a pipelined stop request.
func readRequest(reader *bufio.Reader) (Request, error) {
	var data []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(data)+len(part) > maxRequest {
			return Request{}, errors.New("network service request exceeds its size limit")
		}
		data = append(data, part...)
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return Request{}, errors.New("incomplete network service request")
		}
	}
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, errors.New("invalid network service request")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Request{}, errors.New("expected one network service request")
	}
	if request.Protocol != Protocol {
		return Request{}, errors.New("the app and network service protocols differ; reinstall the service")
	}
	if len(request.Files.TailscaleState) > maxState || len(request.Files.Cache) > maxCache ||
		len(request.Files.Rules) > maxRules || len(request.Files.ExitStates) > maxExits {
		return Request{}, errors.New("network service state exceeds its size limit")
	}
	for id, state := range request.Files.ExitStates {
		if !validExitID(id) || len(state) > maxState {
			return Request{}, errors.New("network service state exceeds its size limit")
		}
	}
	return request, nil
}

type eventWriter struct {
	mu         sync.Mutex
	connection net.Conn
	cancel     func()
}

func (w *eventWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
	n, err := w.connection.Write(data)
	if err != nil && w.cancel != nil {
		w.cancel()
	}
	return n, err
}

func (w *eventWriter) send(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}
