package tailsplit

import (
	"os"
	"strings"
	"testing"
)

func TestOpenIgnoresAuthForOmittedPendingPrimary(t *testing.T) {
	if !TailscaleEnabled {
		t.Skip("constructing a native Tailscale engine requires with_gvisor")
	}
	const authEnv = "TAILSPLIT_TEST_OMITTED_PRIMARY_AUTH"
	for _, value := range []string{"", "unused-primary-test-key"} {
		name := "populated variable"
		if value == "" {
			name = "unset variable"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(authEnv, value)
			if value == "" {
				if err := os.Unsetenv(authEnv); err != nil {
					t.Fatal(err)
				}
			}
			config := focusedExitTestConfig(t)
			config.Tailscale.ExitNode = ""
			config.Tailscale.AuthKeyEnv = authEnv
			config.DNS.MagicDomains = nil
			config.Rules.DefaultExit = "office"
			compiled, err := CompileContext(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := Open(t.Context(), config, compiled)
			if err != nil {
				t.Fatalf("unused primary authentication blocked the ready additional device: %v", err)
			}
			t.Cleanup(func() {
				if err := engine.Close(); err != nil {
					t.Error(err)
				}
			})
			if len(engine.exits) != 1 || engine.exits[0].id != "office" || engine.exits[0].tag != "exit-office" {
				t.Fatalf("engine watched an omitted device: %+v", engine.exits)
			}
			if _, ok := engine.box.Endpoint().Get("tailnet"); ok {
				t.Fatal("engine constructed the omitted primary endpoint")
			}
		})
	}
}

func TestOpenRequiresAuthForCompiledPrimary(t *testing.T) {
	if !TailscaleEnabled {
		t.Skip("constructing a native Tailscale engine requires with_gvisor")
	}
	const authEnv = "TAILSPLIT_TEST_COMPILED_PRIMARY_AUTH"
	for _, value := range []string{"", "compiled-primary-test-key"} {
		name := "populated variable"
		if value == "" {
			name = "unset variable"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(authEnv, value)
			if value == "" {
				if err := os.Unsetenv(authEnv); err != nil {
					t.Fatal(err)
				}
			}
			config := focusedExitTestConfig(t)
			config.Tailscale.AuthKeyEnv = authEnv
			compiled, err := CompileContext(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			engine, err := Open(t.Context(), config, compiled)
			if engine != nil {
				t.Cleanup(func() {
					if err := engine.Close(); err != nil {
						t.Error(err)
					}
				})
			}
			if value == "" {
				if err == nil || !strings.Contains(err.Error(), "required authentication variable "+authEnv+" is empty") {
					t.Fatalf("compiled primary no longer requires its configured auth variable: %v", err)
				}
			} else if err != nil {
				t.Fatalf("compiled primary could not use its configured auth variable: %v", err)
			}
		})
	}
}
