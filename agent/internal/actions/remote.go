package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rayxo/agent/internal/files"
	"rayxo/agent/internal/remote"
	"rayxo/agent/internal/rpc"
	"rayxo/agent/internal/runner"
	"rayxo/agent/internal/validate"
)

const (
	// Kinds of backup a destination holds.
	remoteAccount = "account"
	remoteSystem  = "system"
	// remoteTimeout bounds the calls that move no backup.
	remoteTimeout = 5 * time.Minute
	// reasonRemote is the reason of an error the destination gave, which
	// the panel shows as it is.
	reasonRemote = "remote"
)

// remoteStore copies files to and from destinations; remote.Client is the
// real one.
type remoteStore interface {
	HostKey(ctx context.Context, host string, port int) (remote.HostKey, error)
	Test(ctx context.Context, d remote.Destination, server string) error
	Upload(ctx context.Context, d remote.Destination, local string, path ...string) error
	Download(ctx context.Context, d remote.Destination, local string, maxBytes int64, path ...string) error
	Delete(ctx context.Context, d remote.Destination, path ...string) error
	List(ctx context.Context, d remote.Destination, path ...string) ([]remote.Entry, error)
	Folders(ctx context.Context, d remote.Destination, path ...string) ([]string, error)
}

// remoteParams name a destination and the folder of this server on it.
type remoteParams struct {
	Destination remote.Destination `json:"destination"`
	Server      string             `json:"server"`
}

func (p remoteParams) account() string { return "" }

func (p remoteParams) validate() error {
	return errors.Join(p.Destination.Validate(), remote.ValidateFolder(p.Server))
}

// remoteFileParams name one backup on a destination: of an account, under
// its username, or of the panel itself.
type remoteFileParams struct {
	Destination remote.Destination `json:"destination"`
	Server      string             `json:"server"`
	Kind        string             `json:"kind"`
	Username    string             `json:"username"`
	Name        string             `json:"name"`
}

func (p remoteFileParams) account() string { return "" }

func (p remoteFileParams) validate() error {
	errs := []error{p.Destination.Validate(), remote.ValidateFolder(p.Server), validateBackupName(p.Name)}

	switch p.Kind {
	case remoteAccount:
		errs = append(errs, validate.Username(p.Username))
	case remoteSystem:
		if p.Username != "" {
			errs = append(errs, errors.New("a backup of the panel has no username"))
		}
	default:
		errs = append(errs, fmt.Errorf("kind %q must be account or system", p.Kind))
	}

	return errors.Join(errs...)
}

// folder is where the backup is on the destination, and file its path.
func (p remoteFileParams) folder() []string {
	if p.Kind == remoteSystem {
		return []string{p.Server, "system"}
	}

	return []string{p.Server, "accounts", p.Username}
}

func (p remoteFileParams) file() []string {
	return append(p.folder(), p.Name)
}

// remoteListParams name a destination, and the folder of a server on it
// to list; none lists the servers' folders.
type remoteListParams struct {
	Destination remote.Destination `json:"destination"`
	Server      string             `json:"server"`
}

func (p remoteListParams) account() string { return "" }

func (p remoteListParams) validate() error {
	if p.Server == "" {
		return p.Destination.Validate()
	}

	return errors.Join(p.Destination.Validate(), remote.ValidateFolder(p.Server))
}

// remoteHostKeyParams name an SFTP server.
type remoteHostKeyParams struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// remoteBackup is a backup on a destination.
type remoteBackup struct {
	Username string `json:"username,omitempty"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

// remoteActions implement the backup.remote_* methods: copies of the
// backups the agent keeps, on a destination elsewhere, and back.
type remoteActions struct {
	store  remoteStore
	config BackupsConfig
	space  func(dir string) (diskSpace, error)
}

func remoteMethods(store remoteStore, config BackupsConfig) map[string]rpc.Method {
	actions := remoteActions{store: store, config: config, space: readDiskSpace}

	return map[string]rpc.Method{
		"backup.remote_hostkey":  {Handler: actions.hostKey, Timeout: time.Minute},
		"backup.remote_test":     {Handler: actions.test, Timeout: remoteTimeout},
		"backup.remote_upload":   {Handler: actions.upload, Timeout: backupTimeout},
		"backup.remote_download": {Handler: actions.download, Timeout: backupTimeout},
		"backup.remote_delete":   {Handler: actions.delete, Timeout: remoteTimeout},
		"backup.remote_list":     {Handler: actions.list, Timeout: remoteTimeout},
	}
}

// hostKey implements backup.remote_hostkey: the key an SFTP server
// presents, for the administrator to accept before anything is sent.
func (r remoteActions) hostKey(ctx context.Context, raw json.RawMessage) (any, error) {
	var params remoteHostKeyParams
	if err := rpc.DecodeParams(raw, &params); err != nil {
		return nil, err
	}

	key, err := r.store.HostKey(ctx, params.Host, params.Port)
	if err != nil {
		return nil, remoteError(err)
	}

	return key, nil
}

// test implements backup.remote_test: a file written to and deleted from
// the server's folder on the destination.
func (r remoteActions) test(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[remoteParams](raw)
	if err != nil {
		return nil, err
	}

	if err := r.store.Test(ctx, params.Destination, params.Server); err != nil {
		return nil, remoteError(err)
	}

	return map[string]bool{"ok": true}, nil
}

// upload implements backup.remote_upload: a backup the agent keeps is
// copied to the destination.
func (r remoteActions) upload(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[remoteFileParams](raw)
	if err != nil {
		return nil, err
	}

	local := r.local(params)

	info, err := os.Lstat(local)
	if err != nil || !info.Mode().IsRegular() {
		return nil, rpc.Errorf(rpc.CodeActionFailed, "backup %s is not on the server", params.Name)
	}

	if err := r.store.Upload(ctx, params.Destination, local, params.file()...); err != nil {
		return nil, remoteError(err)
	}

	return map[string]int64{"size": info.Size()}, nil
}

// download implements backup.remote_download: a backup on the destination
// is copied back among those the agent keeps, when there is room for it
// besides the reserve. A backup of an account must be a RAYXO backup, and
// its manifest and settings are returned like an import's.
func (r remoteActions) download(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[remoteFileParams](raw)
	if err != nil {
		return nil, err
	}

	if err := r.ensureDir(params.Kind); err != nil {
		return nil, err
	}

	local := r.local(params)
	if _, err := os.Lstat(local); err == nil {
		return nil, rpc.Errorf(rpc.CodeActionFailed, "backup %s is on the server already", params.Name)
	}

	entries, err := r.store.List(ctx, params.Destination, params.folder()...)
	if err != nil {
		return nil, remoteError(err)
	}

	size := int64(-1)
	for _, entry := range entries {
		if entry.Path == params.Name {
			size = entry.Size
		}
	}

	if size < 0 {
		return nil, rpc.Errorf(rpc.CodeActionFailed, "backup %s is not on the destination", params.Name)
	}

	space, err := r.space(filepath.Dir(local))
	if err != nil {
		return nil, err
	}

	if space.Free < space.Reserve+uint64(size) {
		return nil, noSpaceError(fmt.Sprintf("the disk of the backups has %.1f GB free; this backup needs %.1f GB besides the %.1f GB backups always leave", gib(space.Free), gib(uint64(size)), gib(space.Reserve)))
	}

	part := filepath.Join(filepath.Dir(local), ".remote-"+params.Name+".part")
	_ = os.Remove(part)

	// Cap the transfer at the size the listing reported and we reserved space
	// for, so a destination that under-reports a file cannot fill the disk.
	if err := r.store.Download(ctx, params.Destination, part, size, params.file()...); err != nil {
		_ = os.Remove(part)

		return nil, remoteError(err)
	}

	result, err := r.accept(params, part)
	if err != nil {
		_ = os.Remove(part)

		return nil, err
	}

	if _, err := os.Lstat(local); err == nil {
		_ = os.Remove(part)

		return nil, rpc.Errorf(rpc.CodeActionFailed, "backup %s is on the server already", params.Name)
	}

	if err := os.Rename(part, local); err != nil {
		_ = os.Remove(part)

		return nil, fmt.Errorf("move backup %s into place: %w", params.Name, err)
	}

	return result, nil
}

// accept checks a downloaded backup and gives it the owner and mode of the
// backups the agent makes.
func (r remoteActions) accept(params remoteFileParams, part string) (map[string]any, error) {
	file, err := os.Open(part)
	if err != nil {
		return nil, fmt.Errorf("open the downloaded backup: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("read the downloaded backup: %w", err)
	}

	result := map[string]any{"name": params.Name, "size": info.Size()}

	if params.Kind == remoteSystem {
		magic := make([]byte, 2)
		if _, err := io.ReadFull(file, magic); err != nil || magic[0] != 0x1f || magic[1] != 0x8b {
			return nil, rpc.Errorf(rpc.CodeActionFailed, "the downloaded file is not a backup of the panel")
		}

		return result, file.Chmod(0o600)
	}

	meta, err := files.ReadBackupMeta(file)
	if err != nil {
		return nil, fileError(err)
	}

	if err := file.Chmod(0o640); err != nil {
		return nil, fmt.Errorf("protect backup %s: %w", params.Name, err)
	}

	if os.Geteuid() == 0 && r.config.PanelGID >= 0 {
		if err := file.Chown(0, r.config.PanelGID); err != nil {
			return nil, fmt.Errorf("give backup %s to root: %w", params.Name, err)
		}
	}

	result["manifest"] = meta.Manifest
	result["settings"] = meta.Settings

	return result, nil
}

// delete implements backup.remote_delete.
func (r remoteActions) delete(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[remoteFileParams](raw)
	if err != nil {
		return nil, err
	}

	if err := r.store.Delete(ctx, params.Destination, params.file()...); err != nil {
		return nil, remoteError(err)
	}

	return map[string]bool{"deleted": true}, nil
}

// list implements backup.remote_list: without a server, the servers'
// folders on the destination; with one, its backups of accounts and of
// the panel. Files that are not backups are left out.
func (r remoteActions) list(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[remoteListParams](raw)
	if err != nil {
		return nil, err
	}

	if params.Server == "" {
		folders, err := r.store.Folders(ctx, params.Destination)
		if err != nil {
			return nil, remoteError(err)
		}

		servers := []string{}
		for _, folder := range folders {
			if remote.ValidateFolder(folder) == nil {
				servers = append(servers, folder)
			}
		}

		return map[string][]string{"servers": servers}, nil
	}

	accounts, err := r.store.List(ctx, params.Destination, params.Server, "accounts")
	if err != nil {
		return nil, remoteError(err)
	}

	system, err := r.store.List(ctx, params.Destination, params.Server, "system")
	if err != nil {
		return nil, remoteError(err)
	}

	result := map[string][]remoteBackup{"accounts": {}, "system": {}}

	for _, entry := range accounts {
		username, name, ok := strings.Cut(entry.Path, "/")
		if ok && validate.Username(username) == nil && validateBackupName(name) == nil {
			result["accounts"] = append(result["accounts"], remoteBackup{Username: username, Name: name, Size: entry.Size, Modified: modified(entry)})
		}
	}

	for _, entry := range system {
		if validateBackupName(entry.Path) == nil {
			result["system"] = append(result["system"], remoteBackup{Name: entry.Path, Size: entry.Size, Modified: modified(entry)})
		}
	}

	return result, nil
}

// local is where the agent keeps the backup on this server.
func (r remoteActions) local(params remoteFileParams) string {
	if params.Kind == remoteSystem {
		return filepath.Join(r.config.SystemDir, params.Name)
	}

	return filepath.Join(r.config.Dir, params.Name)
}

func (r remoteActions) ensureDir(kind string) error {
	if kind == remoteSystem {
		return systemBackupActions{config: r.config}.ensureDir()
	}

	return serverBackupActions{config: r.config}.ensureDir()
}

func modified(entry remote.Entry) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, entry.ModTime)
	if err != nil {
		return 0
	}

	return parsed.Unix()
}

// newRemoteStore is the real server's: rclone and ssh-keyscan of the
// distribution, run with no shell and a fixed environment.
func newRemoteStore() remote.Client {
	return remote.New(remote.Config{
		Rclone:  "/usr/bin/rclone",
		KeyScan: "/usr/bin/ssh-keyscan",
		Run:     runner.RunEnv,
		Output: func(err error) string {
			if failure, ok := errors.AsType[*runner.Error](err); ok {
				return failure.Output
			}

			return ""
		},
	})
}

// remoteError passes on what the destination said, for the panel to show.
func remoteError(err error) error {
	var failure *remote.Error
	if errors.As(err, &failure) {
		return &rpc.Error{Code: rpc.CodeActionFailed, Message: failure.Message, Data: map[string]string{"reason": reasonRemote}}
	}

	return err
}
