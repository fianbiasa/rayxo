package actions

import (
	"context"
	"encoding/json"
	"errors"

	"rayxo/agent/internal/bfm"
	"rayxo/agent/internal/rpc"
)

// bruteForceMonitor shows and controls fail2ban. bfm.Manager implements it.
type bruteForceMonitor interface {
	Status(ctx context.Context) (bfm.Report, error)
	Unban(ctx context.Context, ip string) error
	SetWhitelist(ctx context.Context, addresses []string) error
}

type bfmUnbanParams struct {
	IP string `json:"ip"`
}

type bfmWhitelistParams struct {
	IPs []string `json:"ips"`
}

// bfmMethods returns the bfm.* methods by name. The whitelist is written to a
// single file, so one change runs at a time.
func bfmMethods(monitor bruteForceMonitor) map[string]rpc.Method {
	return map[string]rpc.Method{
		"bfm.status": {Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := rpc.DecodeParams(raw, &struct{}{}); err != nil {
				return nil, err
			}

			report, err := monitor.Status(ctx)
			if err != nil {
				return nil, systemError(err)
			}

			return report, nil
		}},
		"bfm.unban": {Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var params bfmUnbanParams
			if err := rpc.DecodeParams(raw, &params); err != nil {
				return nil, err
			}

			if err := monitor.Unban(ctx, params.IP); err != nil {
				return nil, bfmError(err)
			}

			return map[string]any{"ip": params.IP}, nil
		}},
		"bfm.whitelist": {
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var params bfmWhitelistParams
				if err := rpc.DecodeParams(raw, &params); err != nil {
					return nil, err
				}

				if err := monitor.SetWhitelist(ctx, params.IPs); err != nil {
					return nil, bfmError(err)
				}

				return map[string]any{"ips": params.IPs}, nil
			},
			// The whitelist is one file: write it one change at a time.
			LockKey: func(json.RawMessage) (string, error) {
				return "bfm:whitelist", nil
			},
		},
	}
}

// bfmError turns a bad address into an invalid-parameter response and leaves
// everything else as a failed action.
func bfmError(err error) error {
	if errors.Is(err, bfm.ErrInvalidAddress) {
		return rpc.Errorf(rpc.CodeInvalidParams, "invalid params: %v", err)
	}

	return systemError(err)
}
