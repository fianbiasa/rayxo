package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"rayxo/agent/internal/drift"
	"rayxo/agent/internal/nodeapp"
	"rayxo/agent/internal/rpc"
	"rayxo/agent/internal/validate"
)

// nodeappServer is what the nodeapp.* actions need. *nodeapp.Manager
// implements it.
type nodeappServer interface {
	Provision(ctx context.Context, app nodeapp.App) error
	Check(app nodeapp.App) (drift.Report, error)
	Deprovision(ctx context.Context, domain string) error
	Running(ctx context.Context, domain string) (bool, error)
	Restart(ctx context.Context, domain string) error
}

// nodeEnvKeyPattern matches an environment variable name (letters, digits and
// underscores, not starting with a digit).
var nodeEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	nodePortMin = 40000
	nodePortMax = 49999
)

// nodeappProvisionParams are the params of nodeapp.provision: the account, the
// domain, the entry script (relative to the domain's document root), the
// account's own environment variables, and the localhost port the panel
// allocated. The env is plaintext inside the (encrypted) task payload; the
// agent writes it 0600, owned by the account.
type nodeappProvisionParams struct {
	Username string            `json:"username"`
	Domain   string            `json:"domain"`
	Entry    string            `json:"entry"`
	Env      map[string]string `json:"env"`
	Port     int               `json:"port"`
}

func (p nodeappProvisionParams) account() string { return p.Username }

func (p nodeappProvisionParams) validate() error {
	if err := validate.Username(p.Username); err != nil {
		return err
	}

	if err := validate.Domain(p.Domain); err != nil {
		return err
	}

	if err := validateNodeEntry(p.Entry); err != nil {
		return err
	}

	if err := validateNodeEnv(p.Env); err != nil {
		return err
	}

	if p.Port < nodePortMin || p.Port > nodePortMax {
		return fmt.Errorf("node app port %d is out of range %d-%d", p.Port, nodePortMin, nodePortMax)
	}

	return nil
}

// nodeappDomainParams are the params of nodeapp.deprovision, nodeapp.status
// and nodeapp.restart.
type nodeappDomainParams struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
}

func (p nodeappDomainParams) account() string { return p.Username }

func (p nodeappDomainParams) validate() error {
	if err := validate.Username(p.Username); err != nil {
		return err
	}

	return validate.Domain(p.Domain)
}

// validateNodeEntry keeps the entry a relative path inside the document root:
// no absolute path and no "..", so it cannot run a script outside the app.
func validateNodeEntry(entry string) error {
	if entry == "" {
		return fmt.Errorf("entry is required")
	}

	if strings.ContainsAny(entry, "\n\r\x00") {
		return fmt.Errorf("entry must be a single path")
	}

	if filepath.IsAbs(entry) {
		return fmt.Errorf("entry must be relative to the app directory")
	}

	clean := filepath.Clean(entry)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("entry must stay inside the app directory")
	}

	return nil
}

// validateNodeEnv keeps env keys well-formed and values free of newlines, so
// each becomes exactly one KEY=value line of the systemd EnvironmentFile.
func validateNodeEnv(env map[string]string) error {
	for key, value := range env {
		if !nodeEnvKeyPattern.MatchString(key) {
			return fmt.Errorf("environment name %q is not valid", key)
		}

		if strings.ContainsAny(value, "\n\r\x00") {
			return fmt.Errorf("environment value of %q must be a single line", key)
		}
	}

	return nil
}

// nodeappActions implements the nodeapp.* methods.
type nodeappActions struct {
	accounts accountActions
	server   nodeappServer
}

// nodeappMethods returns the nodeapp.* methods by name. Those that change an
// account lock it, like the account.* methods.
func nodeappMethods(system accountSystem, server nodeappServer) map[string]rpc.Method {
	actions := nodeappActions{accounts: accountActions{system: system}, server: server}

	return map[string]rpc.Method{
		"nodeapp.provision":   {Handler: actions.provision, LockKey: webLockKey[nodeappProvisionParams]},
		"nodeapp.check":       {Handler: actions.check, LockKey: webLockKey[nodeappProvisionParams]},
		"nodeapp.deprovision": {Handler: actions.deprovision, LockKey: webLockKey[nodeappDomainParams]},
		"nodeapp.status":      {Handler: actions.status, LockKey: webLockKey[nodeappDomainParams]},
		"nodeapp.restart":     {Handler: actions.restart, LockKey: webLockKey[nodeappDomainParams]},
	}
}

// provision implements nodeapp.provision: it creates or updates the domain's
// Node.js app service, run as the account's user from its document root, and
// starts it.
func (n nodeappActions) provision(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[nodeappProvisionParams](raw)
	if err != nil {
		return nil, err
	}

	user, err := n.accounts.requireAccount(ctx, params.Username)
	if err != nil {
		return nil, err
	}

	app := nodeapp.App{
		User:    user.Name,
		UID:     user.UID,
		GID:     user.GID,
		Domain:  params.Domain,
		WorkDir: filepath.Join(user.Home, "domains", params.Domain, "public_html"),
		Entry:   params.Entry,
		Env:     params.Env,
		Port:    params.Port,
	}

	if err := n.server.Provision(ctx, app); err != nil {
		return nil, nodeappError(err, "start the Node.js app of "+params.Domain)
	}

	return struct{}{}, nil
}

// check implements nodeapp.check: report whether the domain's Node.js app unit
// and environment file drifted, without writing.
func (n nodeappActions) check(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[nodeappProvisionParams](raw)
	if err != nil {
		return nil, err
	}

	user, err := n.accounts.requireAccount(ctx, params.Username)
	if err != nil {
		return nil, err
	}

	report, err := n.server.Check(nodeapp.App{
		User:    user.Name,
		UID:     user.UID,
		GID:     user.GID,
		Domain:  params.Domain,
		WorkDir: filepath.Join(user.Home, "domains", params.Domain, "public_html"),
		Entry:   params.Entry,
		Env:     params.Env,
		Port:    params.Port,
	})
	if err != nil {
		return nil, nodeappError(err, "check the Node.js app of "+params.Domain)
	}

	return report, nil
}

// deprovision implements nodeapp.deprovision. A missing service or account
// still succeeds, like redis.deprovision.
func (n nodeappActions) deprovision(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[nodeappDomainParams](raw)
	if err != nil {
		return nil, err
	}

	if err := n.server.Deprovision(ctx, params.Domain); err != nil {
		return nil, nodeappError(err, "remove the Node.js app of "+params.Domain)
	}

	return struct{}{}, nil
}

// restart implements nodeapp.restart: restart the app after the account
// rebuilt it.
func (n nodeappActions) restart(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[nodeappDomainParams](raw)
	if err != nil {
		return nil, err
	}

	if err := n.server.Restart(ctx, params.Domain); err != nil {
		return nil, nodeappError(err, "restart the Node.js app of "+params.Domain)
	}

	return struct{}{}, nil
}

// status implements nodeapp.status: whether the domain's app is running.
func (n nodeappActions) status(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[nodeappDomainParams](raw)
	if err != nil {
		return nil, err
	}

	running, err := n.server.Running(ctx, params.Domain)
	if err != nil {
		return nil, nodeappError(err, "check the Node.js app of "+params.Domain)
	}

	return map[string]any{"running": running}, nil
}

// nodeappError logs the system detail and returns a generic failure, so panel
// users never see system paths.
func nodeappError(err error, what string) error {
	slog.Error("Node.js app command failed", "task", what, "error", err)

	return rpc.Errorf(rpc.CodeActionFailed, "could not %s; the agent log has the details", what)
}
