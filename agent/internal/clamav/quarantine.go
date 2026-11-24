package clamav

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QuarantineEntry is one infected file held in quarantine. Owner and
// OriginalPath can be empty when a file reached quarantine through on-access or
// scheduled scanning and its origin could not be recovered from the scan log.
type QuarantineEntry struct {
	ID            string `json:"id"`
	OriginalPath  string `json:"original_path"`
	Owner         string `json:"owner"`
	Virus         string `json:"virus"`
	QuarantinedAt string `json:"quarantined_at"` // RFC3339
	Source        string `json:"source"`         // on-demand | scheduled | on-access
}

func (m *Manager) storeDir() string   { return m.cfg.QuarantineDir }
func (m *Manager) stagingDir() string { return filepath.Join(m.cfg.QuarantineDir, stagingName) }
func (m *Manager) scanLogPath() string {
	return filepath.Join(m.cfg.QuarantineDir, logsName, "scan.log")
}
func (m *Manager) onAccessLogPath() string {
	return filepath.Join(m.cfg.QuarantineDir, logsName, "onaccess.log")
}
func (m *Manager) sidecarPath(id string) string { return filepath.Join(m.storeDir(), id+".json") }
func (m *Manager) filePath(id string) string    { return filepath.Join(m.storeDir(), id) }

// ScanAndQuarantine scans path, moves every infected file into quarantine with
// its provenance, and returns the detections (their File is the original path).
func (m *Manager) ScanAndQuarantine(ctx context.Context, path, source string) (ScanResult, error) {
	result, err := m.Scan(ctx, path)
	if err != nil {
		return ScanResult{}, err
	}

	for _, d := range result.Infected {
		if _, err := m.quarantineFile(d.File, d.Virus, source); err != nil {
			return ScanResult{}, fmt.Errorf("quarantine %s: %w", d.File, err)
		}
	}

	return result, nil
}

// QuarantineList reconciles any staged files into the index, then returns all
// quarantined files, newest first.
func (m *Manager) QuarantineList() ([]QuarantineEntry, error) {
	m.reconcile()

	entries, err := m.readEntries()
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].QuarantinedAt > entries[j].QuarantinedAt
	})

	return entries, nil
}

// QuarantineRestore moves a quarantined file back to its original path, owned
// by the original account, and removes it from the index. The file is, by
// definition, one a scan flagged as infected; the caller confirms the restore.
func (m *Manager) QuarantineRestore(id string) error {
	if err := validateID(id); err != nil {
		return err
	}

	entry, err := m.readEntry(id)
	if err != nil {
		return err
	}

	if entry.OriginalPath == "" {
		return fmt.Errorf("the original path of quarantine item %s is unknown; cannot restore", id)
	}

	dir := filepath.Dir(entry.OriginalPath)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("the folder %s no longer exists; cannot restore", dir)
	}

	if _, err := os.Lstat(entry.OriginalPath); err == nil {
		return fmt.Errorf("%s already exists; not overwriting", entry.OriginalPath)
	}

	if err := moveFile(m.filePath(id), entry.OriginalPath); err != nil {
		return fmt.Errorf("restore %s: %w", entry.OriginalPath, err)
	}

	if entry.Owner != "" {
		_ = chownToOwner(entry.OriginalPath, entry.Owner)
	}

	_ = os.Remove(m.sidecarPath(id))

	return nil
}

// QuarantineDelete permanently removes a quarantined file and its sidecar.
func (m *Manager) QuarantineDelete(id string) error {
	if err := validateID(id); err != nil {
		return err
	}

	if _, err := m.readEntry(id); err != nil {
		return err
	}

	_ = os.Remove(m.filePath(id))
	_ = os.Remove(m.sidecarPath(id))

	return nil
}

// quarantineFile moves origPath into the store under a new id and writes its
// sidecar, returning the id.
func (m *Manager) quarantineFile(origPath, virus, source string) (string, error) {
	if err := os.MkdirAll(m.storeDir(), 0o750); err != nil {
		return "", err
	}

	id, err := newID()
	if err != nil {
		return "", err
	}

	if err := moveFile(origPath, m.filePath(id)); err != nil {
		return "", err
	}

	entry := QuarantineEntry{
		ID:            id,
		OriginalPath:  origPath,
		Owner:         ownerFromPath(origPath),
		Virus:         virus,
		QuarantinedAt: time.Now().UTC().Format(time.RFC3339),
		Source:        source,
	}

	if err := m.writeSidecar(entry); err != nil {
		return "", err
	}

	return id, nil
}

// reconcile imports files the scheduled and on-access scanners moved to staging
// into the index, recovering each file's original path and virus from the scan
// logs. Files it cannot map keep an unknown origin rather than staying hidden.
// It is safe to call before every list.
func (m *Manager) reconcile() {
	staged, err := os.ReadDir(m.stagingDir())
	if err != nil || len(staged) == 0 {
		return
	}

	moves := m.parseMoveLogs()

	for _, e := range staged {
		if e.IsDir() {
			continue
		}

		stagedPath := filepath.Join(m.stagingDir(), e.Name())
		info := moves[e.Name()]

		source := info.source
		if source == "" {
			source = sourceScheduled
		}

		id, err := newID()
		if err != nil {
			continue
		}

		if err := os.MkdirAll(m.storeDir(), 0o750); err != nil {
			continue
		}

		if err := moveFile(stagedPath, m.filePath(id)); err != nil {
			continue
		}

		entry := QuarantineEntry{
			ID:            id,
			OriginalPath:  info.orig,
			Owner:         ownerFromPath(info.orig),
			Virus:         orDefault(info.virus, "unknown"),
			QuarantinedAt: time.Now().UTC().Format(time.RFC3339),
			Source:        source,
		}

		_ = m.writeSidecar(entry)
	}
}

// moveInfo is what a scan log tells us about one staged file.
type moveInfo struct {
	orig   string
	virus  string
	source string
}

// parseMoveLogs maps each staged file's basename to its recovered origin, from
// both scan logs.
func (m *Manager) parseMoveLogs() map[string]moveInfo {
	out := map[string]moveInfo{}
	m.parseOneLog(m.scanLogPath(), sourceScheduled, out)
	m.parseOneLog(m.onAccessLogPath(), sourceOnAccess, out)

	return out
}

// parseOneLog reads a clamdscan/clamonacc log for "<path>: <Virus> FOUND" and
// "<path>: moved to '<staging>'" lines, mapping each moved file's basename to
// its original path and virus.
func (m *Manager) parseOneLog(path, source string, out map[string]moveInfo) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	const marker = ": moved to '"
	virusOf := map[string]string{}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)

		if strings.HasSuffix(line, " FOUND") {
			body := strings.TrimSuffix(line, " FOUND")
			if i := strings.LastIndex(body, ": "); i >= 0 {
				virusOf[body[:i]] = body[i+2:]
			}

			continue
		}

		if i := strings.Index(line, marker); i >= 0 && strings.HasSuffix(line, "'") {
			orig := line[:i]
			staging := line[i+len(marker) : len(line)-1]
			out[filepath.Base(staging)] = moveInfo{orig: orig, virus: virusOf[orig], source: source}
		}
	}
}

func (m *Manager) writeSidecar(e QuarantineEntry) error {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}

	return writeFile(m.sidecarPath(e.ID), append(data, '\n'), 0o600)
}

func (m *Manager) readEntry(id string) (QuarantineEntry, error) {
	data, err := os.ReadFile(m.sidecarPath(id))
	if err != nil {
		return QuarantineEntry{}, fmt.Errorf("quarantine item %s not found", id)
	}

	var entry QuarantineEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return QuarantineEntry{}, fmt.Errorf("quarantine item %s is unreadable", id)
	}

	return entry, nil
}

func (m *Manager) readEntries() ([]QuarantineEntry, error) {
	dir, err := os.ReadDir(m.storeDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, err
	}

	var entries []QuarantineEntry
	for _, e := range dir {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(m.storeDir(), name))
		if err != nil {
			continue
		}

		var entry QuarantineEntry
		if json.Unmarshal(data, &entry) != nil {
			continue
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// ownerFromPath returns the account a /home/<user>/... path belongs to, or ""
// when the path is not under a home.
func ownerFromPath(p string) string {
	const home = "/home/"
	if !strings.HasPrefix(p, home) {
		return ""
	}

	rest := p[len(home):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}

	return rest
}

func chownToOwner(path, owner string) error {
	u, err := user.Lookup(owner)
	if err != nil {
		return err
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}

	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}

	return os.Chown(path, uid, gid)
}

// validateID keeps an id to the hex the agent generates, so it can never walk
// out of the store directory.
func validateID(id string) error {
	if id == "" || len(id) > 64 {
		return fmt.Errorf("invalid quarantine id")
	}

	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return fmt.Errorf("invalid quarantine id")
		}
	}

	return nil
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return hex.EncodeToString(b), nil
}

// moveFile renames src to dst, falling back to copy-and-remove across
// filesystems (quarantine and /home can be different mounts).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)

		return err
	}

	if err := out.Close(); err != nil {
		return err
	}

	return os.Remove(src)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}

	return s
}
