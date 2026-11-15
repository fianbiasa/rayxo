package actions

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"rayxo/agent/internal/ioncube"
	"rayxo/agent/internal/rpc"
)

// fakeLoader records what php.ioncube asked of the ionCube Loader.
type fakeLoader struct {
	calls []string
	fail  error
}

func (f *fakeLoader) Status(_ context.Context, versions []string) ioncube.State {
	return ioncube.State{Enabled: true, Versions: versions, Missing: []string{}, Loader: "15.0.0"}
}

func (f *fakeLoader) Enable(_ context.Context, versions []string) (ioncube.State, error) {
	f.calls = append(f.calls, "enable")

	return ioncube.State{Enabled: true, Versions: versions, Missing: []string{}, Loader: "15.0.0"}, f.fail
}

func (f *fakeLoader) Disable(_ context.Context, versions []string) (ioncube.State, error) {
	f.calls = append(f.calls, "disable")

	return ioncube.State{Versions: []string{}, Missing: []string{}}, f.fail
}

func TestTheIonCubeLoaderIsTurnedOnAndOffForEveryPHPVersion(t *testing.T) {
	loader := &fakeLoader{}
	methods := ionCubeMethods(loader, func() ([]string, error) { return []string{"8.3", "8.4"}, nil })

	if methods["php.ioncube"].LockKey == nil || methods["php.ioncube"].Timeout != ionCubeTimeout {
		t.Error("turning the loader on must lock and may take minutes")
	}

	raw, err := callBackup(methods, "php.ioncube", map[string]any{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}

	var state ioncube.State
	if err := json.Unmarshal(raw, &state); err != nil || !state.Enabled || !slices.Equal(state.Versions, []string{"8.3", "8.4"}) {
		t.Errorf("state = %+v, err = %v", state, err)
	}

	if _, err := callBackup(methods, "php.ioncube", map[string]any{"enabled": false}); err != nil || !slices.Equal(loader.calls, []string{"enable", "disable"}) {
		t.Errorf("calls = %v, err = %v", loader.calls, err)
	}

	loader.fail = errors.New("download the ionCube Loader: HTTP 503")

	_, err = callBackup(methods, "php.ioncube", map[string]any{"enabled": true})

	var refused *rpc.Error
	if !errors.As(err, &refused) || refused.Message != "download the ionCube Loader: HTTP 503" {
		t.Errorf("err = %v", err)
	}
}
