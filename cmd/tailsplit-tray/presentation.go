package main

import (
	"github.com/idy/tailsplit"
	"github.com/idy/tailsplit/internal/tray"
)

type connectionView struct {
	Summary string
	Action  string
}

func connectionPresentation(snapshot tray.Snapshot) connectionView {
	if !snapshot.Active {
		if snapshot.BackendState == "NeedsLogin" {
			return connectionView{"需要登录", "登录 Tailscale…"}
		}
		if snapshot.State == "failed" {
			return connectionView{"连接失败，查看日志", "重试连接"}
		}
		return connectionView{"未连接", "连接"}
	}
	switch snapshot.State {
	case "running":
		if snapshot.RoutingMode == tailsplit.RoutingGlobal {
			return connectionView{"全局代理 · 已连接", "断开连接"}
		}
		if snapshot.Mode == "tun" {
			return connectionView{"TUN 已连接", "断开连接"}
		}
		return connectionView{"代理已连接", "断开连接"}
	case "blocked":
		return connectionView{"出口不可用 · 已阻断", "断开连接"}
	case "discovering":
		return connectionView{"正在读取出口", "取消"}
	case "login":
		return connectionView{"等待 Tailscale 登录", "取消连接"}
	case "authenticating":
		return connectionView{"正在登录", "取消登录"}
	case "stopping":
		return connectionView{"正在断开", "正在断开…"}
	case "authorizing":
		return connectionView{"等待安装后台服务", "取消连接"}
	case "waiting-exit":
		return connectionView{"等待出口上线", "取消连接"}
	case "missing-exit":
		return connectionView{"请重新选择出口", "取消连接"}
	case "logging-out":
		return connectionView{"正在退出登录", "请稍候…"}
	default:
		return connectionView{"正在连接", "取消连接"}
	}
}

func canReconnect(snapshot tray.Snapshot) bool {
	switch snapshot.State {
	case "stopping", "logging-out", "authorizing":
		return false
	}
	return !snapshot.Active || snapshot.Mode == "tun" || snapshot.Mode == "proxy"
}

func shortMenuTitle(value string) string {
	runes := []rune(value)
	if len(runes) > 26 {
		return string(runes[:25]) + "…"
	}
	return value
}
