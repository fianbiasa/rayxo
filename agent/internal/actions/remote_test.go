package actions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rayxo/agent/internal/remote"
	"rayxo/agent/internal/rpc"
)

// fakeStore keeps a destination's files in memory, by their path.
type fakeStore struct {
	files   map[string][]byte
	folders []string
	fail    error
	lastMax int64
}

func (f *fakeStore) key(path []string) string { return strings.Join(path, "/") }

func (f *fakeStore) HostKey(context.Context, string, int) (remote.HostKey, error) {
	return remote.HostKey{Key: "ssh-ed25519 AAAA", Fingerprint: "SHA256:abc"}, f.fail
}

func (f *fakeStore) Test(context.Context, remote.Destination, string) error { return f.fail }

func (f *fakeStore) Upload(_ context.Context, _ remote.Destination, local string, path ...string) error {
	content, err := os.ReadFile(local)
	if err != nil {
		return err
	}

	f.files[f.key(path)] = content

	return nil
}

func (f *fakeStore) Download(_ context.Context, _ remote.Destination, local string, maxBytes int64, path ...string) error {
	f.lastMax = maxBytes
	content, ok := f.files[f.key(path)]
	if !ok {
		return &remote.Error{Message: "object not found"}
	}

	return os.WriteFile(local, content, 0o644)
}

func (f *fakeStore) Delete(_ context.Context, _ remote.Destination, path ...string) error {
	delete(f.files, f.key(path))

	return nil
}

func (f *fakeStore) List(_ context.Context, _ remote.Destination, path ...string) ([]remote.Entry, error) {
	prefix := f.key(path) + "/"
	entries := []remote.Entry{}

	for name, content := range f.files {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			entries = append(entries, remote.Entry{Path: rest, Size: int64(len(content)), ModTime: "2026-11-15T10:00:00Z"})
		}
	}

	return entries, nil
}

func (f *fakeStore) Folders(context.Context, remote.Destination, ...string) ([]string, error) {
	return f.folders, nil
}

type remoteEnv struct {
	*serverBackupEnv
	store     *fakeStore
	systemDir string
	methods   map[string]rpc.Method
}

var testDestination = map[string]any{"type": "s3", "bucket": "my-backups", "access_key": "a", "secret_key": "b"}

func newRemoteEnv(t *testing.T) *remoteEnv {
	t.Helper()

	env := &remoteEnv{serverBackupEnv: newServerBackupEnv(t), store: &fakeStore{files: map[string][]byte{}}, systemDir: filepath.Join(t.TempDir(), "system")}

	actions := remoteActions{
		store:  env.store,
		config: BackupsConfig{Dir: env.dir, SystemDir: env.systemDir, PanelGID: -1},
		space: func(string) (diskSpace, error) {
			return diskSpace{Total: 200 << 30, Free: env.free, Reserve: 10 << 30}, nil
		},
	}

	env.methods = map[string]rpc.Method{}
	for name, method := range remoteMethods(env.store, BackupsConfig{}) {
		env.methods[name] = method
	}

	env.methods["backup.remote_upload"] = rpc.Method{Handler: actions.upload}
	env.methods["backup.remote_download"] = rpc.Method{Handler: actions.download}
	env.methods["backup.remote_list"] = rpc.Method{Handler: actions.list}

	return env
}

func (env *remoteEnv) call(method string, params map[string]any) (map[string]any, error) {
	base := map[string]any{"destination": testDestination, "server": "server1.example.com"}
	for key, value := range params {
		base[key] = value
	}

	raw, err := callBackup(env.methods, method, base)
	if err != nil {
		return nil, err
	}

	var result map[string]any

	return result, json.Unmarshal(raw, &result)
}

func TestABackupGoesToTheDestinationAndComesBackOnlyAsABackup(t *testing.T) {
	env := newRemoteEnv(t)

	created, err := env.create(t, map[string]any{"files": true, "email": true})
	if err != nil {
		t.Fatal(err)
	}

	backup := map[string]any{"kind": "account", "username": "budi", "name": created.Name}

	if result, err := env.call("backup.remote_upload", backup); err != nil || result["size"] != float64(created.Size) {
		t.Fatalf("upload = %v, %v", result, err)
	}

	if _, ok := env.store.files["server1.example.com/accounts/budi/"+created.Name]; !ok {
		t.Fatalf("not on the destination: %d files", len(env.store.files))
	}

	if _, err := env.call("backup.remote_download", backup); err == nil || !strings.Contains(err.Error(), "on the server already") {
		t.Errorf("a backup on the server was downloaded over: %v", err)
	}

	local := filepath.Join(env.dir, created.Name)
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}

	result, err := env.call("backup.remote_download", backup)
	if err != nil || result["manifest"] != `{"format":1}` {
		t.Fatalf("download = %v, %v", result, err)
	}

	// The download is capped at the size the listing reported.
	if env.store.lastMax != created.Size {
		t.Errorf("download cap = %d, want %d", env.store.lastMax, created.Size)
	}

	if info, err := os.Lstat(local); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("downloaded = %v, %v", info, err)
	}

	// Something else under a backup's name is refused and leaves nothing.
	env.store.files["server1.example.com/accounts/budi/budi-junk.tar.gz"] = []byte("not a backup")

	if _, err := env.call("backup.remote_download", map[string]any{"kind": "account", "username": "budi", "name": "budi-junk.tar.gz"}); err == nil {
		t.Error("a file that is not a backup came back")
	}

	entries, _ := os.ReadDir(env.dir)
	for _, entry := range entries {
		if entry.Name() != created.Name {
			t.Errorf("left behind: %s", entry.Name())
		}
	}
}

func TestADownloadNeedsRoomBesidesTheReserve(t *testing.T) {
	env := newRemoteEnv(t)
	env.store.files["server1.example.com/accounts/budi/budi-big.tar.gz"] = make([]byte, 2048)
	env.free = 10<<30 + 1024

	_, err := env.call("backup.remote_download", map[string]any{"kind": "account", "username": "budi", "name": "budi-big.tar.gz"})

	var refused *rpc.Error
	if !errors.As(err, &refused) || refused.Data.(map[string]string)["reason"] != reasonNoSpace {
		t.Errorf("err = %#v", err)
	}

	if _, err := os.Lstat(filepath.Join(env.dir, "budi-big.tar.gz")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the backup was downloaded anyway")
	}
}

func TestBackupsOfThePanelHaveAFolderOfTheirOwnAndStayRoots(t *testing.T) {
	env := newRemoteEnv(t)
	if err := os.MkdirAll(env.systemDir, 0o700); err != nil {
		t.Fatal(err)
	}

	writeHomeFile(t, filepath.Join(env.systemDir, "rayxo-system-1.tar.gz"), "\x1f\x8bpanel")
	backup := map[string]any{"kind": "system", "name": "rayxo-system-1.tar.gz"}

	if _, err := env.call("backup.remote_upload", backup); err != nil {
		t.Fatal(err)
	}

	if string(env.store.files["server1.example.com/system/rayxo-system-1.tar.gz"]) != "\x1f\x8bpanel" {
		t.Fatalf("files = %v", env.store.files)
	}

	_ = os.Remove(filepath.Join(env.systemDir, "rayxo-system-1.tar.gz"))

	if _, err := env.call("backup.remote_download", backup); err != nil {
		t.Fatal(err)
	}

	if info, err := os.Lstat(filepath.Join(env.systemDir, "rayxo-system-1.tar.gz")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("downloaded = %v, %v", info, err)
	}

	env.store.files["server1.example.com/system/rayxo-system-2.tar.gz"] = []byte("plain text")
	if _, err := env.call("backup.remote_download", map[string]any{"kind": "system", "name": "rayxo-system-2.tar.gz"}); err == nil {
		t.Error("a file that is not a gzip came back as a backup of the panel")
	}
}

func TestRemoteParamsAreChecked(t *testing.T) {
	env := newRemoteEnv(t)

	for name, params := range map[string]map[string]any{
		"unknown kind":          {"kind": "home", "username": "budi", "name": "budi-1.tar.gz"},
		"system with username":  {"kind": "system", "username": "budi", "name": "rayxo-system-1.tar.gz"},
		"name climbing out":     {"kind": "account", "username": "budi", "name": "../../etc/passwd"},
		"username climbing out": {"kind": "account", "username": "../root", "name": "budi-1.tar.gz"},
		"server climbing out":   {"kind": "account", "username": "budi", "name": "budi-1.tar.gz", "server": "../other"},
		"bad destination":       {"kind": "account", "username": "budi", "name": "budi-1.tar.gz", "destination": map[string]any{"type": "s3", "bucket": "x"}},
	} {
		if _, err := env.call("backup.remote_upload", params); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestListingsLeaveOutWhatIsNotABackup(t *testing.T) {
	env := newRemoteEnv(t)
	env.store.folders = []string{"server1.example.com", "not a server!"}

	for _, name := range []string{"server1.example.com/accounts/budi/budi-1.tar.gz", "server1.example.com/accounts/budi/notes.txt", "server1.example.com/accounts/loose.tar.gz", "server1.example.com/system/rayxo-system-1.tar.gz"} {
		env.store.files[name] = []byte("x")
	}

	servers, err := env.call("backup.remote_list", map[string]any{"server": ""})
	if err != nil || len(servers["servers"].([]any)) != 1 || servers["servers"].([]any)[0] != "server1.example.com" {
		t.Fatalf("servers = %v, %v", servers, err)
	}

	listed, err := env.call("backup.remote_list", nil)
	if err != nil {
		t.Fatal(err)
	}

	accounts, system := listed["accounts"].([]any), listed["system"].([]any)
	if len(accounts) != 1 || accounts[0].(map[string]any)["username"] != "budi" || accounts[0].(map[string]any)["name"] != "budi-1.tar.gz" || accounts[0].(map[string]any)["modified"] != float64(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC).Unix()) {
		t.Errorf("accounts = %v", accounts)
	}

	if len(system) != 1 || system[0].(map[string]any)["name"] != "rayxo-system-1.tar.gz" {
		t.Errorf("system = %v", system)
	}
}

func TestTheDestinationsWordsReachThePanel(t *testing.T) {
	env := newRemoteEnv(t)
	env.store.fail = &remote.Error{Message: "Failed to copy: 530 Login incorrect."}

	_, err := env.call("backup.remote_test", nil)

	var refused *rpc.Error
	if !errors.As(err, &refused) || refused.Message != "Failed to copy: 530 Login incorrect." || refused.Data.(map[string]string)["reason"] != reasonRemote {
		t.Errorf("err = %#v", err)
	}

	if _, err := callBackup(env.methods, "backup.remote_hostkey", map[string]any{"host": "backup.example.com", "port": 22}); err == nil {
		t.Error("a failed key scan was not reported")
	}
}
