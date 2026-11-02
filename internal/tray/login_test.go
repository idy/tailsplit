package tray

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestLoginClickWaitsForTheFreshURLAfterLogout(t *testing.T) {
	var flow LoginFlow
	flow.Request()
	if value := flow.Next(Snapshot{State: "starting"}); value != "" {
		t.Fatal("opened before the new login link was generated")
	}
	current := Snapshot{State: "login", BackendState: "NeedsLogin", LoginURL: "https://login.tailscale.com/a/first"}
	if value := flow.Next(current); value != current.LoginURL {
		t.Fatal("a click before link generation was lost")
	}
	if flow.Next(current) != "" {
		t.Fatal("opened duplicate browser windows while an open was in progress")
	}
	flow.Complete(nil)
	if flow.Next(current) != "" {
		t.Fatal("reopened the browser on a repeated status notification")
	}
	// Logout clears the snapshot. Its completion requests the next login,
	// which must wait for the restarted client's fresh authorization URL.
	flow.Cancel()
	flow.Request()
	if value := flow.Next(Snapshot{State: "starting"}); value != "" {
		t.Fatal("logout reused the previous client's authorization URL")
	}
	current.LoginURL = "https://login.tailscale.com/a/second"
	if value := flow.Next(current); value != current.LoginURL {
		t.Fatal("could not open login after logout")
	}
	flow.Complete(errors.New("fixture failure"))
	if !strings.Contains(flow.Message(), "复制") {
		t.Fatal("browser failure was silently discarded")
	}
	flow.Request()
	if flow.Next(current) == "" {
		t.Fatal("browser failure prevented retrying the same valid link")
	}
}

func TestBrowserLaunchUsesTheFreshInstanceAndDoesNotExposeTheLinkInErrors(t *testing.T) {
	link := "https://login.tailscale.com/a/private-test-link"
	calls := 0
	runner := func(_ context.Context, name string, args ...string) error {
		calls++
		if name != "/usr/bin/open" || !slices.Equal(args, []string{"-n", link}) {
			t.Fatalf("unexpected browser command: %s %v", name, args)
		}
		return errors.New("fixture failure including " + link)
	}
	err := openLoginPage(context.Background(), link, "", runner)
	if err == nil || strings.Contains(err.Error(), "private-test-link") || calls != 1 {
		t.Fatalf("unsafe or silent opener failure: %v; calls=%d", err, calls)
	}
	if err := openLoginPage(context.Background(), "https://untrusted.example/", "", runner); err == nil || calls != 1 {
		t.Fatal("arbitrary URL was passed to the browser launcher")
	}
	if err := openLoginPage(context.Background(), link, "com.google.chrome",
		func(_ context.Context, name string, args ...string) error {
			if name != "/usr/bin/open" || !slices.Equal(args,
				[]string{"-n", "-b", "com.google.chrome", "--args", "--new-window", link}) {
				t.Fatalf("Chrome login was not sent to an explicit new window: %s %v", name, args)
			}
			return nil
		}); err != nil {
		t.Fatal(err)
	}
}

func TestLoginDoesNotSignOutAnAlreadyLoggedInDevice(t *testing.T) {
	var flow LoginFlow
	flow.Request()
	if value := flow.Next(Snapshot{State: "running", BackendState: "Running"}); value != "" {
		t.Fatal("a login click tried to replace an existing device login")
	}
	if flow.Message() != "设备已登录" {
		t.Fatal("already logged in state was left waiting forever")
	}
}
