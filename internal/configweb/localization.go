package configweb

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/idy/tailsplit/internal/tray"
)

func (s *Server) readLanguage(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.options.Language.Get())
}

func (s *Server) setLanguage(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "content_type", "请求必须使用 JSON。")
		return
	}
	var request struct {
		Preference string `json:"preference"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF {
		fail(w, http.StatusBadRequest, "request", "无法读取语言设置。")
		return
	}
	if request.Preference != "auto" && request.Preference != "en" && request.Preference != "zh" {
		fail(w, http.StatusBadRequest, "preference", "语言设置必须为 auto、en 或 zh。")
		return
	}
	if err := s.options.Language.Set(request.Preference); err != nil {
		fail(w, http.StatusInternalServerError, "language", "无法保存语言设置："+err.Error())
		return
	}
	s.readLanguage(w, r)
}

func (s *Server) readCompanion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Enabled bool `json:"enabled"`
	}{Enabled: s.options.Language.CompanionEnabled()})
}

func (s *Server) setCompanion(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "content_type", "请求必须使用 JSON。")
		return
	}
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF || request.Enabled == nil {
		fail(w, http.StatusBadRequest, "request", "页面伙伴设置必须包含 enabled 布尔值。")
		return
	}
	if err := s.options.Language.SetCompanionEnabled(*request.Enabled); err != nil {
		fail(w, http.StatusInternalServerError, "companion", "无法保存页面伙伴设置："+err.Error())
		return
	}
	s.readCompanion(w, r)
}

type languageResponseWriter struct {
	http.ResponseWriter
	english bool
}

// Missing or unsupported headers preserve the original Chinese responses.
func requestsEnglish(r *http.Request) bool {
	best, english := -1.0, false
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		fields := strings.Split(part, ";")
		language := strings.ToLower(strings.TrimSpace(fields[0]))
		if language != "en" && !strings.HasPrefix(language, "en-") && language != "zh" && !strings.HasPrefix(language, "zh-") {
			continue
		}
		quality := 1.0
		for _, parameter := range fields[1:] {
			if key, value, ok := strings.Cut(strings.TrimSpace(parameter), "="); ok && key == "q" {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if quality > 0 && quality > best {
			best, english = quality, strings.HasPrefix(language, "en")
		}
	}
	return english
}

var errorTranslations = map[string]string{
	"重建配置需要当前文件版本。":                        "Rebuilding requires the current configuration revision.",
	"请先断开连接，再重建配置。":                        "Disconnect before rebuilding the configuration.",
	"当前配置有效，无需重建。":                         "The current configuration is valid and does not need rebuilding.",
	"请选择已发现且已授权的出口节点进行 Ping。":              "Select a discovered, authorized exit node to ping.",
	"当前无法使用 Tailscale Ping；请先登录并等待设备连接完成。": "Tailscale ping is unavailable; sign in and wait for the device to finish connecting.",
	"另一项设备操作正在进行，请稍后重试 Ping。":              "Another device operation is in progress; retry the ping when it finishes.",
	"此出口节点已离线或授权已过期。":                      "This exit node is offline or its authorization has expired.",
	"出口节点未响应 Tailscale Ping；请检查连接后重试。":     "The exit node did not answer the Tailscale ping; check its connection and retry.",
	"Tailscale Ping 超时，请检查出口节点后重试。":        "Tailscale ping timed out; check the exit node and retry.",
	"Tailscale Ping 已取消。":                  "Tailscale ping was canceled.",
	"请从 TailSplit 菜单打开本机配置页面。":             "Open the local configuration page from the TailSplit menu.",
	"页面授权已失效，请从 TailSplit 菜单重新打开。":         "Page authorization has expired. Reopen it from the TailSplit menu.",
	"请求必须使用 JSON。":                         "Requests must use JSON.",
	"配置不得超过 1 MiB，且只能提交一份内容。":              "Configuration must not exceed 1 MiB and must contain only one document.",
	"配置文件已由其他操作修改，请重新读取后再保存。":              "The configuration was changed by another operation. Reload it before saving.",
	"配置文件已更新，请重新读取后重试。":                    "The configuration changed. Reload it and try again.",
	"无法生成配置响应。":                            "Could not encode the configuration response.",
	"无法读取操作请求。":                            "Could not read the action request.",
	"未知操作。":                                "Unknown action.",
	"未找到这个出口，请先保存设置。":                      "This exit was not found. Save your settings first.",
	"无法添加出口，请先检查配置。":                       "Could not add an exit. Check the configuration first.",
	"这个出口已存在。":                             "This exit already exists.",
	"已完成登录，但无法保存出口。请重新读取配置后重试。":            "Signed in, but could not save the exit. Reload configuration and try Add exit again.",
	"无法读取日志。":                              "Could not read logs.",
	"无法读取语言设置。":                            "Could not read language preferences.",
	"语言设置必须为 auto、en 或 zh。":                "Language preference must be auto, en, or zh.",
	"页面伙伴设置必须包含 enabled 布尔值。":              "Companion preferences must include an enabled boolean.",
	"后台服务没有响应；请从“设置与工具 → 后台网络服务 → 更新或修复…”恢复服务": "The background service is not responding. Restore it from Settings & Tools → Background Network Service → Update or Repair…",
}

var errorPrefixes = []struct{ chinese, english string }{
	{"请在 YAML 原文中修正无法用于表单的值：", "Correct the values that cannot be used in the form in the YAML source: "},
	{"无法读取配置文件：", "Could not read the configuration file: "},
	{"无法读取编辑内容：", "Could not read the edits: "},
	{"无法应用配置：", "Could not apply the configuration: "},
	{"无法重建配置：", "Could not rebuild the configuration: "},
	{"无法保存语言设置：", "Could not save language preferences: "},
	{"无法保存页面伙伴设置：", "Could not save companion preferences: "},
}

func translateError(message string) string {
	if translated, ok := errorTranslations[message]; ok {
		return translated
	}
	for _, prefix := range errorPrefixes {
		if strings.HasPrefix(message, prefix.chinese) {
			return prefix.english + translateError(strings.TrimPrefix(message, prefix.chinese))
		}
	}
	return tray.Translate("en", message)
}

// Only error fields in the response envelope and its known state/update objects
// are eligible. Raw JSON preserves YAML, logs, release notes and numeric values.
func localizeErrors(data []byte, english bool) []byte {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return data
	}
	if raw, ok := object["error"]; ok {
		var message string
		if json.Unmarshal(raw, &message) == nil && message != "" {
			translated := translateError(message)
			// Keep both presentations with the original diagnostic. A client can
			// change language without repeating a failed save or other operation.
			if translated != message {
				object["error_i18n"], _ = json.Marshal(map[string]string{"en": translated, "zh": message})
			}
			if english {
				object["error"], _ = json.Marshal(translated)
			}
		}
	}
	for _, key := range []string{"state", "updates"} {
		if raw, ok := object[key]; ok && strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
			object[key] = localizeErrors(raw, english)
		}
	}
	result, err := json.Marshal(object)
	if err != nil {
		return data
	}
	return result
}
