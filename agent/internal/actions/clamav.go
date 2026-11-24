package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"rayxo/agent/internal/clamav"
	"rayxo/agent/internal/rpc"
)

// clamavServer is what the clamav.* actions need. *clamav.Manager implements it.
type clamavServer interface {
	Apply(ctx context.Context, s clamav.Settings) error
	Status(ctx context.Context) (clamav.Status, error)
	ScanAndQuarantine(ctx context.Context, path, source string) (clamav.ScanResult, error)
	QuarantineList() ([]clamav.QuarantineEntry, error)
	QuarantineRestore(id string) error
	QuarantineDelete(id string) error
}

// clamavScanParams are the params of clamav.scan.
type clamavScanParams struct {
	Path string `json:"path"`
}

// clamavQuarantineParams identify one quarantined file.
type clamavQuarantineParams struct {
	ID string `json:"id"`
}

// clamavActions implements the clamav.* methods. They are server-wide, like
// the service.* and firewall.* methods, so they share one lock rather than an
// account lock.
type clamavActions struct {
	server clamavServer
}

func clamavMethods(server clamavServer) map[string]rpc.Method {
	actions := clamavActions{server: server}
	lock := func(json.RawMessage) (string, error) { return "clamav", nil }

	return map[string]rpc.Method{
		"clamav.status": {Handler: actions.status},
		// Enabling downloads the ~300 MB signature database, so allow minutes.
		"clamav.apply": {Handler: actions.apply, LockKey: lock, Timeout: 20 * time.Minute},
		// A scan of a large home can take a while.
		"clamav.scan":               {Handler: actions.scan, LockKey: func(json.RawMessage) (string, error) { return "clamav-scan", nil }, Timeout: 30 * time.Minute},
		"clamav.quarantine.list":    {Handler: actions.quarantineList},
		"clamav.quarantine.restore": {Handler: actions.quarantineRestore, LockKey: lock},
		"clamav.quarantine.delete":  {Handler: actions.quarantineDelete, LockKey: lock},
	}
}

// status implements clamav.status.
func (c clamavActions) status(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := rpc.DecodeParams(raw, &struct{}{}); err != nil {
		return nil, err
	}

	status, err := c.server.Status(ctx)
	if err != nil {
		return nil, clamavError(err, "read ClamAV status")
	}

	return status, nil
}

// apply implements clamav.apply: bring ClamAV to the given settings, then
// report the resulting status.
func (c clamavActions) apply(ctx context.Context, raw json.RawMessage) (any, error) {
	var settings clamav.Settings
	if err := rpc.DecodeParams(raw, &settings); err != nil {
		return nil, err
	}

	if err := c.server.Apply(ctx, settings); err != nil {
		return nil, clamavError(err, "apply the ClamAV settings")
	}

	status, err := c.server.Status(ctx)
	if err != nil {
		return nil, clamavError(err, "read ClamAV status")
	}

	return status, nil
}

// scan implements clamav.scan: scan a path under /home and report infections.
func (c clamavActions) scan(ctx context.Context, raw json.RawMessage) (any, error) {
	var params clamavScanParams
	if err := rpc.DecodeParams(raw, &params); err != nil {
		return nil, err
	}

	if err := validateScanPath(params.Path); err != nil {
		return nil, rpc.Errorf(rpc.CodeInvalidParams, "invalid params: %v", err)
	}

	result, err := c.server.ScanAndQuarantine(ctx, params.Path, "on-demand")
	if err != nil {
		return nil, clamavError(err, "scan "+params.Path)
	}

	return result, nil
}

// quarantineList implements clamav.quarantine.list: every file now in
// quarantine, with its original path, owner, virus and source.
func (c clamavActions) quarantineList(_ context.Context, raw json.RawMessage) (any, error) {
	if err := rpc.DecodeParams(raw, &struct{}{}); err != nil {
		return nil, err
	}

	entries, err := c.server.QuarantineList()
	if err != nil {
		return nil, clamavError(err, "list the quarantine")
	}

	if entries == nil {
		entries = []clamav.QuarantineEntry{}
	}

	return map[string]any{"entries": entries}, nil
}

// quarantineRestore implements clamav.quarantine.restore: put a quarantined
// file back where it came from. The admin has confirmed restoring a file a
// scan flagged as infected.
func (c clamavActions) quarantineRestore(_ context.Context, raw json.RawMessage) (any, error) {
	var params clamavQuarantineParams
	if err := rpc.DecodeParams(raw, &params); err != nil {
		return nil, err
	}

	if err := c.server.QuarantineRestore(params.ID); err != nil {
		return nil, rpc.Errorf(rpc.CodeActionFailed, "%v", err)
	}

	return struct{}{}, nil
}

// quarantineDelete implements clamav.quarantine.delete: permanently remove a
// quarantined file.
func (c clamavActions) quarantineDelete(_ context.Context, raw json.RawMessage) (any, error) {
	var params clamavQuarantineParams
	if err := rpc.DecodeParams(raw, &params); err != nil {
		return nil, err
	}

	if err := c.server.QuarantineDelete(params.ID); err != nil {
		return nil, rpc.Errorf(rpc.CodeActionFailed, "%v", err)
	}

	return struct{}{}, nil
}

// validateScanPath keeps scans inside the accounts' home area.
func validateScanPath(path string) error {
	if path != "/home" && !strings.HasPrefix(path, "/home/") {
		return fmt.Errorf("path %q must be /home or under it", path)
	}

	if strings.Contains(path, "..") {
		return fmt.Errorf("path %q must not contain ..", path)
	}

	return nil
}

// clamavError logs the system detail and returns a generic failure.
func clamavError(err error, what string) error {
	slog.Error("ClamAV command failed", "task", what, "error", err)

	return rpc.Errorf(rpc.CodeActionFailed, "could not %s; the agent log has the details", what)
}
