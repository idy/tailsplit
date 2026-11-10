package configweb

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/idy/tailsplit/internal/tray"
)

func TestAddExitRevisionConflictsRetainBothLanguages(t *testing.T) {
	const chinese = "配置文件已更新，请重新读取后重试。"
	const english = "The configuration changed. Reload it and try again."
	for _, stage := range []string{"before-start", "during-start"} {
		for _, language := range []string{"en", "zh"} {
			t.Run(stage+"/"+language, func(t *testing.T) {
				s, _, _ := testServer(t)
				_, revision, err := tray.ReadConfigDocument(s.options.ConfigPath)
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				s.options.AddExit = func(context.Context, string, string) error { calls++; return tray.ErrConfigChanged }
				if stage == "before-start" {
					revision = "stale"
				}
				status, body := request(t, s, "POST", "/api/action", map[string]string{
					"action": "add-exit", "exit": "new-exit", "revision": revision,
				}, map[string]string{"Accept-Language": language})
				var result struct {
					Code     string            `json:"code"`
					Error    string            `json:"error"`
					Messages map[string]string `json:"error_i18n"`
				}
				if err := json.Unmarshal(body, &result); err != nil {
					t.Fatal(err)
				}
				want := chinese
				if language == "en" {
					want = english
				}
				if status != http.StatusConflict || result.Code != "conflict" || result.Error != want || result.Messages["en"] != english || result.Messages["zh"] != chinese {
					t.Fatalf("unexpected localized conflict: status=%d body=%s", status, body)
				}
				wantCalls := 0
				if stage == "during-start" {
					wantCalls = 1
				}
				if calls != wantCalls {
					t.Fatal("incorrect conflict stage", calls)
				}
			})
		}
	}
}
