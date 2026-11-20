package actions

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"rayxo/agent/internal/drift"
	"rayxo/agent/internal/rpc"
	"rayxo/agent/internal/waf"
)

// wafServer is what the waf.* actions need. *waf.Manager implements it.
type wafServer interface {
	Apply(ctx context.Context, s waf.Settings) error
	Status(ctx context.Context) (waf.Status, error)
	Check(s waf.Settings) (drift.Report, error)
}

// wafActions implements the waf.* methods. They are server-wide, like the
// clamav.* and firewall.* methods, so they share one lock rather than an
// account lock.
type wafActions struct {
	server wafServer
}

func wafMethods(server wafServer) map[string]rpc.Method {
	actions := wafActions{server: server}
	lock := func(json.RawMessage) (string, error) { return "waf", nil }

	return map[string]rpc.Method{
		"waf.status": {Handler: actions.status},
		"waf.check":  {Handler: actions.check, LockKey: lock},
		// Applying rewrites one file and reloads Apache; a couple of minutes is
		// plenty even on a busy server.
		"waf.apply": {Handler: actions.apply, LockKey: lock, Timeout: 2 * time.Minute},
	}
}

// check implements waf.check: report whether the ModSecurity engine file drifted.
func (w wafActions) check(_ context.Context, raw json.RawMessage) (any, error) {
	var settings waf.Settings
	if err := rpc.DecodeParams(raw, &settings); err != nil {
		return nil, err
	}

	report, err := w.server.Check(settings)
	if err != nil {
		return nil, wafError(err, "check the ModSecurity config")
	}

	return report, nil
}

// status implements waf.status.
func (w wafActions) status(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := rpc.DecodeParams(raw, &struct{}{}); err != nil {
		return nil, err
	}

	status, err := w.server.Status(ctx)
	if err != nil {
		return nil, wafError(err, "read the ModSecurity status")
	}

	return status, nil
}

// apply implements waf.apply: set the global ModSecurity mode, then report the
// resulting status.
func (w wafActions) apply(ctx context.Context, raw json.RawMessage) (any, error) {
	var settings waf.Settings
	if err := rpc.DecodeParams(raw, &settings); err != nil {
		return nil, err
	}

	if err := w.server.Apply(ctx, settings); err != nil {
		return nil, wafError(err, "apply the ModSecurity settings")
	}

	status, err := w.server.Status(ctx)
	if err != nil {
		return nil, wafError(err, "read the ModSecurity status")
	}

	return status, nil
}

// wafError logs the system detail and returns a generic failure.
func wafError(err error, what string) error {
	slog.Error("ModSecurity command failed", "task", what, "error", err)

	return rpc.Errorf(rpc.CodeActionFailed, "could not %s; the agent log has the details", what)
}
