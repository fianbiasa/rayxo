package actions

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"rayxo/agent/internal/ioncube"
	"rayxo/agent/internal/rpc"
	"rayxo/agent/internal/runner"
)

// ionCubeTimeout bounds turning the ionCube Loader on: its download is
// about 20 MB, then every PHP-FPM pool reloads.
const ionCubeTimeout = 15 * time.Minute

// ionCubeLoader is what php.ioncube needs of the ionCube Loader.
type ionCubeLoader interface {
	Status(ctx context.Context, versions []string) ioncube.State
	Enable(ctx context.Context, versions []string) (ioncube.State, error)
	Disable(ctx context.Context, versions []string) (ioncube.State, error)
}

// ionCubeActions implements php.ioncube and php.ioncube_status for every
// installed PHP version.
type ionCubeActions struct {
	loader   ionCubeLoader
	versions func() ([]string, error)
}

func ionCubeMethods(loader ionCubeLoader, versions func() ([]string, error)) map[string]rpc.Method {
	actions := ionCubeActions{loader: loader, versions: versions}
	lock := func(json.RawMessage) (string, error) { return "server:ioncube", nil }

	return map[string]rpc.Method{
		"php.ioncube":        {Handler: actions.set, LockKey: lock, Timeout: ionCubeTimeout},
		"php.ioncube_status": {Handler: actions.status},
	}
}

// newIonCube is the real server's ionCube Loader: php<version> runs PHP,
// and every PHP-FPM pool of a version reloads.
func newIonCube() *ioncube.Manager {
	php := func(ctx context.Context, version string, args ...string) (string, error) {
		output, err := runner.Run(ctx, "/usr/bin/php"+version, args...)

		return string(output), err
	}

	reload := func(ctx context.Context, version string) error {
		_, err := runner.Run(ctx, "/usr/bin/systemctl", "reload-or-restart", "rayxo-php"+strings.ReplaceAll(version, ".", "")+"@*.service")

		return err
	}

	return ioncube.New(ioncube.DefaultConfig(php, reload))
}

// ionCubeParams are the params of php.ioncube.
type ionCubeParams struct {
	Enabled bool `json:"enabled"`
}

// set implements php.ioncube: the loader on (downloaded from ionCube) or
// off, for every installed PHP version.
func (a ionCubeActions) set(ctx context.Context, raw json.RawMessage) (any, error) {
	var params ionCubeParams
	if err := rpc.DecodeParams(raw, &params); err != nil {
		return nil, err
	}

	versions, err := a.versions()
	if err != nil {
		return nil, systemError(err)
	}

	var state ioncube.State

	if params.Enabled {
		state, err = a.loader.Enable(ctx, versions)
	} else {
		state, err = a.loader.Disable(ctx, versions)
	}

	if err != nil {
		return nil, rpc.Errorf(rpc.CodeActionFailed, "%v", err)
	}

	return state, nil
}

// status implements php.ioncube_status: which PHP versions load the
// loader now.
func (a ionCubeActions) status(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := rpc.DecodeParams(raw, &struct{}{}); err != nil {
		return nil, err
	}

	versions, err := a.versions()
	if err != nil {
		return nil, systemError(err)
	}

	return a.loader.Status(ctx, versions), nil
}
