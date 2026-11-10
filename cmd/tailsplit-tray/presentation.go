package main

import (
	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

type connectionView struct {
	Summary string
	Action  string
}

func connectionPresentationForLanguage(snapshot tray.Snapshot, language string) connectionView {
	view := connectionPresentation(snapshot)
	return connectionView{tray.Translate(language, view.Summary), tray.Translate(language, view.Action)}
}

// The fallback menu describes the overall session. The primary device can need
// sign-in while a different device carries traffic, so its backend state must
// not override the controller's connection state or connect/disconnect action.
func connectionPresentation(snapshot tray.Snapshot) connectionView {
	if !snapshot.Active {
		if snapshot.State == "failed" {
			return connectionView{"连接失败，查看日志", "连接"}
		}
		return connectionView{"未连接", "连接"}
	}
	summary := "正在连接"
	switch snapshot.State {
	case "running":
		if snapshot.RoutingMode == tailsplit.RoutingGlobal {
			summary = "全局代理 · 已连接"
		} else if snapshot.Mode == "tun" {
			summary = "TUN 已连接"
		} else {
			summary = "代理已连接"
		}
	case "blocked":
		summary = "出口不可用 · 已阻断"
	case "discovering":
		summary = "正在读取出口"
	case "login":
		summary = "等待 Tailscale 登录"
	case "authenticating":
		summary = "正在登录"
	case "stopping":
		summary = "正在断开"
	case "authorizing":
		summary = "等待安装后台服务"
	case "waiting-exit":
		summary = "等待出口上线"
	case "missing-exit":
		summary = "请重新选择出口"
	case "logging-out":
		summary = "正在退出登录"
	}
	return connectionView{summary, "断开连接"}
}

// isConnected matches the configuration panel's connected status: a live tunnel
// or proxy session with traffic flowing, which the tray icon shows in green.
func isConnected(snapshot tray.Snapshot) bool {
	return snapshot.Active && snapshot.State == "running" && (snapshot.Mode == "tun" || snapshot.Mode == "proxy")
}
