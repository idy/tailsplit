package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/appservice"

	singlog "github.com/sagernet/sing-box/log"
	"gopkg.in/yaml.v3"
)

func TestRunEventsRejectsChangedRulesBeforeOpeningCore(t *testing.T) {
	state := filepath.Join(t.TempDir(), "unopened-state")
	config := tailsplit.Config{StateDir: state, ExpectedRulesRevision: strings.Repeat("1", 64)}
	compiled := tailsplit.Compiled{JSON: []byte("invalid core JSON"), RulesRevision: strings.Repeat("2", 64)}
	var output bytes.Buffer
	stream := newEventStream(t.Context(), &output, false)
	defer stream.close()
	if err := runEvents(t.Context(), config, compiled, stream); !errors.Is(err, tailsplit.ErrRulesChanged) {
		t.Fatal("changed rules reached the traffic core", err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("mismatched rules opened traffic state before rejection", err)
	}
	if output.Len() != 0 {
		t.Fatal("rejected rules reported a ready connection", output.String())
	}
}

func TestRunEventsChecksFinalPreparedRulesProjection(t *testing.T) {
	expected, changed := strings.Repeat("1", 64), strings.Repeat("2", 64)
	for _, test := range []struct {
		name, initial, prepared, expected string
		reject                            bool
	}{
		{"preparation changed rules", expected, changed, expected, true},
		{"preparation restored expected rules", changed, expected, expected, false},
		{"ordinary connection has no expectation", expected, changed, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "state")
			config := tailsplit.Config{
				StateDir: state, ExpectedRulesRevision: test.expected,
				Tailscale: &tailsplit.TailscaleConfig{ExitNode: "100.64.0.10"},
				// No file exists here: the guard must use the recorded projection,
				// rather than rereading rules after compilation or preparation.
				Rules: tailsplit.RulesConfig{GFWList: filepath.Join(t.TempDir(), "missing.txt")},
			}
			compiled := tailsplit.Compiled{JSON: []byte("invalid core JSON"), RulesRevision: test.initial}
			var output bytes.Buffer
			stream := newEventStream(t.Context(), &output, false)
			defer stream.close()
			prepared := false
			err := runEventsWithPreparation(t.Context(), config, compiled, stream,
				func(_ context.Context, received tailsplit.Config, current tailsplit.Compiled, _ func(tailsplit.Status), _ singlog.PlatformWriter) (tailsplit.Compiled, error) {
					prepared = true
					if received.ExpectedRulesRevision != test.expected || current.RulesRevision != test.initial {
						t.Fatal("preparation lost the expectation or initial compilation")
					}
					current.RulesRevision = test.prepared
					return current, nil
				})
			if !prepared {
				t.Fatal("rules guard ran before connection preparation")
			}
			if test.reject {
				if !errors.Is(err, tailsplit.ErrRulesChanged) {
					t.Fatal("preparation replaced the projection without rejecting restart", err)
				}
				if _, err := os.Stat(state); !os.IsNotExist(err) {
					t.Fatal("changed preparation opened the traffic core", err)
				}
			} else {
				// Invalid native JSON safely stops before any engine starts. Reaching
				// native validation proves the expectation accepted the final digest.
				if err == nil || !strings.Contains(err.Error(), "validate generated sing-box config") {
					t.Fatal("matching or ordinary connection did not reach native validation", err)
				}
			}
		})
	}
}

func TestExpectedRulesRevisionSurvivesServiceWireAndNormalization(t *testing.T) {
	config := tailsplit.Config{StateDir: t.TempDir(), ExpectedRulesRevision: strings.Repeat("a", 64)}
	err := runAppCommand(t.Context(), config, io.Discard,
		func(_ context.Context, _ string, received tailsplit.Config, _ io.Writer) error {
			data, err := json.Marshal(appservice.Request{Protocol: appservice.Protocol, Op: "run", Config: &received})
			if err != nil {
				return err
			}
			var request appservice.Request
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil {
				return err
			}
			normalized, err := tailsplit.Normalize(*request.Config, t.TempDir())
			if err != nil {
				return err
			}
			if normalized.ExpectedRulesRevision != config.ExpectedRulesRevision {
				t.Fatal("service wire or normalization discarded the restart expectation")
			}
			if err := appservice.ValidateAppConfig(normalized); err != nil {
				return err
			}
			saved, err := yaml.Marshal(normalized)
			if err != nil {
				return err
			}
			if strings.Contains(string(saved), config.ExpectedRulesRevision) || strings.Contains(string(saved), "expectedrulesrevision") {
				t.Fatal("transient rule identity leaked into saved YAML", string(saved))
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRulesRevisionFlagIsLimitedToAppRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	var output bytes.Buffer
	args := []string{"app-run", "--rules-revision", strings.Repeat("a", 64), "-c", path}
	if err := execute(args, &output, &output); !os.IsNotExist(err) {
		t.Fatal("app-run did not accept its rule revision argument", err)
	}
	for _, command := range []string{"run", "render", "check", "app-login", "init"} {
		args[0] = command
		if err := execute(args, &output, &output); err == nil || !strings.Contains(err.Error(), "--rules-revision is supported only by app-run") {
			t.Fatal("unrelated command accepted a restart-only argument", command, err)
		}
	}
}
