package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"rayxo/agent/internal/redis"
	"rayxo/agent/internal/rpc"
	"rayxo/agent/internal/validate"
)

// redisServer is what the redis.* actions need. *redis.Manager implements it.
type redisServer interface {
	Provision(ctx context.Context, in redis.Instance) error
	Deprovision(ctx context.Context, user string) error
	Running(ctx context.Context, user string) (bool, error)
}

// redisProvisionParams are the params of redis.provision: the account, the
// password its app authenticates with, and the memory cap in MB (0 uses the
// default). The password is plaintext because redis stores requirepass
// plainly in its config; the account reads only its own config.
type redisProvisionParams struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	MaxMemoryMB int    `json:"max_memory_mb"`
}

func (p redisProvisionParams) account() string { return p.Username }

func (p redisProvisionParams) validate() error {
	if err := validate.Username(p.Username); err != nil {
		return err
	}

	if err := validateRedisPassword(p.Password); err != nil {
		return err
	}

	if p.MaxMemoryMB < 0 || p.MaxMemoryMB > 65536 {
		return fmt.Errorf("max_memory_mb %d is out of range", p.MaxMemoryMB)
	}

	return nil
}

// redisAccountParams are the params of redis.deprovision and redis.status.
type redisAccountParams struct {
	Username string `json:"username"`
}

func (p redisAccountParams) account() string { return p.Username }

func (p redisAccountParams) validate() error { return validate.Username(p.Username) }

// validateRedisPassword keeps the password alphanumeric so it is always safe
// inside the quoted requirepass line of the generated redis.conf.
func validateRedisPassword(password string) error {
	if n := len(password); n < 16 || n > 128 {
		return fmt.Errorf("redis password must be 16 to 128 characters")
	}

	for _, r := range password {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fmt.Errorf("redis password must be letters and digits only")
		}
	}

	return nil
}

// redisActions implements the redis.* methods.
type redisActions struct {
	accounts accountActions
	server   redisServer
}

// redisMethods returns the redis.* methods by name. Those that change an
// account lock it, like the account.* methods.
func redisMethods(system accountSystem, server redisServer) map[string]rpc.Method {
	actions := redisActions{accounts: accountActions{system: system}, server: server}

	return map[string]rpc.Method{
		"redis.provision":   {Handler: actions.provision, LockKey: webLockKey[redisProvisionParams]},
		"redis.deprovision": {Handler: actions.deprovision, LockKey: webLockKey[redisAccountParams]},
		"redis.status":      {Handler: actions.status, LockKey: webLockKey[redisAccountParams]},
	}
}

// provision implements redis.provision: it creates or updates the account's
// Redis instance with the given password and memory cap and starts it.
func (r redisActions) provision(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[redisProvisionParams](raw)
	if err != nil {
		return nil, err
	}

	user, err := r.accounts.requireAccount(ctx, params.Username)
	if err != nil {
		return nil, err
	}

	in := redis.Instance{
		User:        user.Name,
		UID:         user.UID,
		GID:         user.GID,
		Password:    params.Password,
		MaxMemoryMB: params.MaxMemoryMB,
	}

	if err := r.server.Provision(ctx, in); err != nil {
		return nil, redisError(err, "set up Redis for "+params.Username)
	}

	return struct{}{}, nil
}

// deprovision implements redis.deprovision. A missing instance or account
// still succeeds, like db.delete_user.
func (r redisActions) deprovision(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[redisAccountParams](raw)
	if err != nil {
		return nil, err
	}

	if err := r.server.Deprovision(ctx, params.Username); err != nil {
		return nil, redisError(err, "remove Redis for "+params.Username)
	}

	return struct{}{}, nil
}

// status implements redis.status: whether the account's instance is running.
func (r redisActions) status(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[redisAccountParams](raw)
	if err != nil {
		return nil, err
	}

	running, err := r.server.Running(ctx, params.Username)
	if err != nil {
		return nil, redisError(err, "check Redis for "+params.Username)
	}

	return map[string]any{"running": running}, nil
}

// redisError logs the system detail and returns a generic failure, so panel
// users never see system paths.
func redisError(err error, what string) error {
	slog.Error("Redis command failed", "task", what, "error", err)

	return rpc.Errorf(rpc.CodeActionFailed, "could not %s; the agent log has the details", what)
}
