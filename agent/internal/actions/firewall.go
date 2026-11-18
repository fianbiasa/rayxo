package actions

import (
	"context"
	"encoding/json"
	"errors"

	"rayxo/agent/internal/firewall"
	"rayxo/agent/internal/rpc"
)

// firewallManager owns the nftables firewall. firewall.Manager implements it.
type firewallManager interface {
	Status(ctx context.Context) (firewall.Status, error)
	Apply(ctx context.Context, config firewall.Config) (firewall.Status, error)
	Confirm(ctx context.Context) (firewall.Status, error)
	Cancel(ctx context.Context) (firewall.Status, error)
}

// firewallMethods returns the firewall.* methods by name. Every change goes
// one at a time, so a trial is never raced by another.
func firewallMethods(manager firewallManager) map[string]rpc.Method {
	const lockKey = "firewall"

	lock := func(json.RawMessage) (string, error) { return lockKey, nil }

	return map[string]rpc.Method{
		"firewall.status": {Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := rpc.DecodeParams(raw, &struct{}{}); err != nil {
				return nil, err
			}

			status, err := manager.Status(ctx)
			if err != nil {
				return nil, systemError(err)
			}

			return status, nil
		}},
		"firewall.apply": {
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				var config firewall.Config
				if err := rpc.DecodeParams(raw, &config); err != nil {
					return nil, err
				}

				status, err := manager.Apply(ctx, config)
				if err != nil {
					return nil, firewallError(err)
				}

				return status, nil
			},
			LockKey: lock,
		},
		"firewall.confirm": {
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				if err := rpc.DecodeParams(raw, &struct{}{}); err != nil {
					return nil, err
				}

				status, err := manager.Confirm(ctx)
				if err != nil {
					return nil, firewallError(err)
				}

				return status, nil
			},
			LockKey: lock,
		},
		"firewall.cancel": {
			Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
				if err := rpc.DecodeParams(raw, &struct{}{}); err != nil {
					return nil, err
				}

				status, err := manager.Cancel(ctx)
				if err != nil {
					return nil, firewallError(err)
				}

				return status, nil
			},
			LockKey: lock,
		},
	}
}

// firewallError maps a bad config to an invalid-parameter response and the
// trial-state errors to a failed action the panel can explain, leaving
// everything else a plain failure.
func firewallError(err error) error {
	switch {
	case errors.Is(err, firewall.ErrInvalidConfig):
		return rpc.Errorf(rpc.CodeInvalidParams, "invalid params: %v", err)
	case errors.Is(err, firewall.ErrTrialRunning):
		return &rpc.Error{Code: rpc.CodeActionFailed, Message: err.Error(), Data: map[string]string{"reason": "trial_running"}}
	case errors.Is(err, firewall.ErrNoTrial):
		return &rpc.Error{Code: rpc.CodeActionFailed, Message: err.Error(), Data: map[string]string{"reason": "no_trial"}}
	default:
		return systemError(err)
	}
}
