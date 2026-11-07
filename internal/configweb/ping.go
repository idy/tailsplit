package configweb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"

	"github.com/idy/tailsplit"
)

func (s *Server) exitPing(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID string `json:"id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&request) != nil || decoder.Decode(&extra) != io.EOF || request.ID == "" || len(request.ID) > 256 {
		pingFailure(w, tailsplit.ErrPingTarget)
		return
	}
	if s.options.PingExit == nil || s.options.Snapshot == nil {
		pingFailure(w, tailsplit.ErrPingUnavailable)
		return
	}
	snapshot := s.options.Snapshot()
	if !snapshot.PingAvailable {
		pingFailure(w, tailsplit.ErrPingUnavailable)
		return
	}
	if err := tailsplit.ValidateExitPingTarget(snapshot.ExitNodes, request.ID); err != nil {
		pingFailure(w, err)
		return
	}
	if !s.ping.TryLock() {
		pingFailure(w, tailsplit.ErrPingBusy)
		return
	}
	defer s.ping.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), tailsplit.ExitPingTimeout)
	defer cancel()
	result, err := s.options.PingExit(ctx, request.ID)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		pingFailure(w, err)
		return
	}
	if result.ID != request.ID || result.LatencyMS < 0 || math.IsNaN(result.LatencyMS) || math.IsInf(result.LatencyMS, 0) || len(result.Relay) > 64 {
		pingFailure(w, tailsplit.ErrPingFailed)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func pingFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrBusy) {
		err = tailsplit.ErrPingBusy
	}
	code := tailsplit.PingErrorCode(err)
	status := http.StatusBadGateway
	message := "出口节点未响应 Tailscale Ping；请检查连接后重试。"
	switch code {
	case "ping_target":
		status, message = http.StatusBadRequest, "请选择已发现且已授权的出口节点进行 Ping。"
	case "ping_busy":
		status, message = http.StatusConflict, "另一项设备操作正在进行，请稍后重试 Ping。"
	case "ping_offline":
		status, message = http.StatusConflict, "此出口节点已离线或授权已过期。"
	case "ping_unavailable":
		status, message = http.StatusServiceUnavailable, "当前无法使用 Tailscale Ping；请先登录并等待设备连接完成。"
	case "ping_timeout":
		status, message = http.StatusGatewayTimeout, "Tailscale Ping 超时，请检查出口节点后重试。"
	case "ping_canceled":
		status, message = http.StatusRequestTimeout, "Tailscale Ping 已取消。"
	}
	fail(w, status, code, message)
}
