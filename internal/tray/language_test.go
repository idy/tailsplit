package tray

import (
	"strings"
	"testing"
	"unicode"
)

func TestNativeTranslationCatalog(t *testing.T) {
	for source, english := range englishUI {
		if Translate("zh", source) != source {
			t.Fatalf("Chinese changed: %q", source)
		}
		if Translate("en", source) != english || english == "" || strings.ContainsFunc(english, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Fatalf("invalid English for %q", source)
		}
	}
	// User values and external diagnostics are not rewritten.
	for _, value := range []string{"北京出口", "/Users/用户/config.yaml", "https://example.com/路径", "service error: 外部日志"} {
		if Translate("en", value) != value {
			t.Fatalf("external value changed: %q", value)
		}
	}
	diagnostic := "无法解压更新: external error /Users/用户/update.zip"
	if got := Translate("en", diagnostic); got != "Could not extract the update: external error /Users/用户/update.zip" {
		t.Fatal(got)
	}
}

func TestLoginVisibilityIndependentOfLanguage(t *testing.T) {
	var flow LoginFlow
	if flow.ShowMessage() {
		t.Fatal("idle status should be hidden")
	}
	flow.Request()
	if !flow.ShowMessage() {
		t.Fatal("pending status should be shown")
	}
	flow.Next(Snapshot{BackendState: "Running"})
	if flow.ShowMessage() {
		t.Fatal("signed-in status should be hidden")
	}
	flow.Request()
	flow.Fail("无法准备登录，请查看日志")
	if !flow.ShowMessage() {
		t.Fatal("failure status should be shown")
	}
	flow.Cancel()
	if flow.ShowMessage() {
		t.Fatal("cancelled status should be hidden")
	}
}
