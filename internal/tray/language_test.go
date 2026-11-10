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

func TestApplicationMenuAndStartupTranslations(t *testing.T) {
	for source, want := range map[string]string{
		"隐藏 TailSplit": "Hide TailSplit",
		"隐藏其他":         "Hide Others",
		"全部显示":         "Show All",
		"编辑":           "Edit",
		"撤销":           "Undo",
		"重做":           "Redo",
		"剪切":           "Cut",
		"拷贝":           "Copy",
		"粘贴":           "Paste",
		"粘贴并匹配样式":      "Paste and Match Style",
		"删除":           "Delete",
		"全选":           "Select All",
		"窗口":           "Window",
		"最小化":          "Minimize",
		"缩放":           "Zoom",
		"关闭":           "Close",
		"等待确认更新…":      "Waiting for update confirmation…",
		"等待确认升级…":      "Waiting for update confirmation…",
		"后台网络服务准备失败，请重试": "Could not prepare the network service; try again",
	} {
		if got := Translate("en", source); got != want {
			t.Errorf("English translation of %q = %q; want %q", source, got, want)
		}
		if got := Translate("zh", source); got != source {
			t.Errorf("Chinese translation of %q = %q", source, got)
		}
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
