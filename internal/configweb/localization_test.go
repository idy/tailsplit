package configweb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/idy/tailsplit/internal/appupdate"
	"github.com/idy/tailsplit/internal/locale"
	"github.com/idy/tailsplit/internal/tray"
)

func TestLanguageAPIIsIndependentAndPersistent(t *testing.T) {
	server, path, applied := testServer(t)
	_, revision, err := tray.ReadConfigDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	status, body := request(t, server, "POST", "/api/language", map[string]string{"preference": "zh"}, nil)
	var state locale.State
	if json.Unmarshal(body, &state) != nil || status != 200 || state != (locale.State{Preference: "zh", Language: "zh"}) {
		t.Fatal(status, string(body))
	}
	status, body = request(t, server, "GET", "/api/language", nil, nil)
	if status != 200 || !bytes.Contains(body, []byte(`"preference":"zh"`)) {
		t.Fatal(status, string(body))
	}
	data, after, err := tray.ReadConfigDocument(path)
	if err != nil || string(data) != fixtureConfig || after != revision || applied.Load() != 0 {
		t.Fatal("language change touched configuration or runtime")
	}
	restarted, err := Start(Options{ConfigPath: path, Apply: func(context.Context, func(tray.Snapshot) error, func() error) error {
		t.Error("unexpected apply")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	status, body = request(t, restarted, "GET", "/api/language", nil, nil)
	if status != 200 || !bytes.Contains(body, []byte(`"preference":"zh"`)) {
		t.Fatal(status, string(body))
	}
	shared := locale.NewStore(path)
	another, err := Start(Options{ConfigPath: path, Language: shared, Apply: func(context.Context, func(tray.Snapshot) error, func() error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer another.Close()
	if err := shared.Set("en"); err != nil {
		t.Fatal(err)
	}
	_, body = request(t, another, "GET", "/api/language", nil, nil)
	if !bytes.Contains(body, []byte(`"language":"en"`)) {
		t.Fatal("shared store ignored", string(body))
	}
}

func TestLanguageAPIRestrictions(t *testing.T) {
	server, _, applied := testServer(t)
	for _, test := range []struct {
		name, body, header, value string
		status                    int
	}{
		{"unauthorized", `{"preference":"en"}`, "Authorization", "", 401},
		{"origin", `{"preference":"en"}`, "Origin", "https://foreign.example", 403},
		{"host", `{"preference":"en"}`, "Host", "foreign.example", 403},
		{"content type", `{"preference":"en"}`, "Content-Type", "text/plain", 415},
		{"content type prefix", `{"preference":"en"}`, "Content-Type", "application/json-invalid", 415},
		{"invalid", `{"preference":"fr"}`, "", "", 400},
		{"null", `null`, "", "", 400},
		{"unknown field", `{"preference":"en","other":true}`, "", "", 400},
		{"trailing", `{"preference":"en"} {}`, "", "", 400},
		{"oversized", `{"preference":"en"}` + strings.Repeat(" ", 1024), "", "", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://"+server.address+"/api/language", strings.NewReader(test.body))
			req.Header.Set("Authorization", "Bearer "+server.token)
			req.Header.Set("Content-Type", "application/json")
			if test.header == "Host" {
				req.Host = test.value
			} else if test.header != "" {
				req.Header.Set(test.header, test.value)
			}
			response := httptest.NewRecorder()
			server.handler().ServeHTTP(response, req)
			if response.Code != test.status {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
	if applied.Load() != 0 || server.options.Language.Get().Preference != "auto" {
		t.Fatal("invalid request changed state")
	}
	// Force a persistence failure without relying on permissions (tests may run as root).
	if err := os.Mkdir(server.options.ConfigPath+".ui.json", 0700); err != nil {
		t.Fatal(err)
	}
	status, _ := request(t, server, "POST", "/api/language", map[string]string{"preference": "en"}, nil)
	if status != 500 || server.options.Language.Get().Preference != "auto" {
		t.Fatal("save failure was published")
	}
}

func TestEnglishErrorsPreserveDiagnosticsAndOtherContent(t *testing.T) {
	server, _, _ := testServer(t)
	for _, language := range []string{"", "zh", "en"} {
		status, body := request(t, server, "POST", "/api/preview", map[string]any{"yaml": fixtureConfig, "unknown": true}, map[string]string{"Accept-Language": language})
		if status != 400 || !bytes.Contains(body, []byte(`unknown field`)) {
			t.Fatal(status, string(body))
		}
		var response map[string]any
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		prefix := "无法读取编辑内容："
		if language == "en" {
			prefix = "Could not read the edits: "
		}
		if !strings.HasPrefix(response["error"].(string), prefix) {
			t.Fatal(response)
		}
		status, body = request(t, server, "POST", "/api/preview", map[string]string{"yaml": "value: .nan\n"}, map[string]string{"Accept-Language": language})
		if status != 200 {
			t.Fatal(status, string(body))
		}
		var doc document
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc.Error, "unsupported value: NaN") || doc.YAML != "value: .nan\n" {
			t.Fatal(doc)
		}
		if language == "en" && !strings.HasPrefix(doc.Error, "Correct the values") {
			t.Fatal(doc.Error)
		}
	}
	source := []byte(`{"error":"启动失败，请查看日志","state":"failed","updates":{"error":"无法应用配置：detail 文件.yaml","notes":"启动失败，请查看日志"},"text":"无法读取日志。","values":{"error":"启动失败，请查看日志"}}`)
	var got map[string]any
	if err := json.Unmarshal(localizeErrors(source, true), &got); err != nil {
		t.Fatal(err)
	}
	if got["error"] != "Could not start; view logs" || got["state"] != "failed" || got["text"] != "无法读取日志。" {
		t.Fatal(got)
	}
	updates := got["updates"].(map[string]any)
	if updates["error"] != "Could not apply the configuration: detail 文件.yaml" || updates["notes"] != "启动失败，请查看日志" || got["values"].(map[string]any)["error"] != "启动失败，请查看日志" {
		t.Fatal(got)
	}
}

func TestLanguageNegotiation(t *testing.T) {
	for header, want := range map[string]bool{"": false, "en": true, "en-US": true, "zh-Hant": false, "en;q=0,zh": false, "zh;q=0.5,en;q=0.9": true, "en;q=0.5,zh": false, "fr": false} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Accept-Language", header)
		if requestsEnglish(request) != want {
			t.Fatal(header)
		}
	}
}

func TestStateEnglishErrorsKeepKeysAndReleaseNotes(t *testing.T) {
	s := &Server{address: "127.0.0.1:12345", token: "secret", options: Options{
		ConfigPath: "missing.yaml",
		Snapshot: func() tray.Snapshot {
			return tray.Snapshot{State: "failed", Error: "启动失败，请查看日志", LoginURL: "https://secret.example"}
		},
		UpdateState: func() appupdate.State {
			return appupdate.State{Error: "无法应用配置：underlying detail.yaml", ReleaseNotes: "启动失败，请查看日志"}
		},
	}}
	for _, language := range []string{"en", "zh", ""} {
		req := httptest.NewRequest("GET", "http://"+s.address+"/api/state", nil)
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Accept-Language", language)
		response := httptest.NewRecorder()
		s.handler().ServeHTTP(response, req)
		var state map[string]any
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &state) != nil {
			t.Fatal(response.Body.String())
		}
		wantError, wantUpdate := "启动失败，请查看日志", "无法应用配置：underlying detail.yaml"
		if language == "en" {
			wantError, wantUpdate = "Could not start; view logs", "Could not apply the configuration: underlying detail.yaml"
		}
		update := state["updates"].(map[string]any)
		if state["state"] != "failed" || state["error"] != wantError || update["error"] != wantUpdate || update["release_notes"] != "启动失败，请查看日志" || strings.Contains(response.Body.String(), "secret.example") {
			t.Fatal(state)
		}
	}
}

func TestErrorsRetainBothLanguagesForLiveSwitching(t *testing.T) {
	const diagnostic = "disk full: user/config.yaml"
	source, _ := json.Marshal(map[string]any{
		"code": "apply", "error": "无法应用配置：" + diagnostic,
		"yaml": "# 无法应用配置：keep this comment", "release_notes": "无法应用配置：keep these notes",
	})
	for _, english := range []bool{false, true} {
		var got map[string]any
		if err := json.Unmarshal(localizeErrors(source, english), &got); err != nil {
			t.Fatal(err)
		}
		messages := got["error_i18n"].(map[string]any)
		if messages["en"] != "Could not apply the configuration: "+diagnostic || messages["zh"] != "无法应用配置："+diagnostic {
			t.Fatal(messages)
		}
		want := messages["zh"]
		if english {
			want = messages["en"]
		}
		if got["error"] != want || got["code"] != "apply" || got["yaml"] != "# 无法应用配置：keep this comment" || got["release_notes"] != "无法应用配置：keep these notes" {
			t.Fatal(got)
		}
	}
}

func TestNativeUpdateErrorTranslation(t *testing.T) {
	for message, want := range map[string]string{
		"无法解压更新: archive checksum detail.zip": "Could not extract the update: archive checksum detail.zip",
		"新版 App 的签名校验失败":                      "The new app’s signature verification failed",
		"unknown diagnostic 文件.yaml":          "unknown diagnostic 文件.yaml",
	} {
		if got := translateError(message); got != want {
			t.Fatalf("%q: got %q, want %q", message, got, want)
		}
	}
}
