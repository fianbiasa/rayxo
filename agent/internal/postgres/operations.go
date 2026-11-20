package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	pgDumpProgram = "/usr/bin/pg_dump"
	gzipProgram   = "/usr/bin/gzip"
	// maxMessage is the most of PostgreSQL's own words a failure carries.
	maxMessage = 500
)

// Identity is a Linux user something runs as: an account.
type Identity struct {
	UID  int
	GID  int
	Home string
}

// ImportError is a failed import, with what PostgreSQL or gzip said, which the
// account may see: it is about its own file.
type ImportError struct {
	Message string
}

func (e *ImportError) Error() string { return "the import failed: " + e.Message }

// Export writes a plain-SQL dump of database name of user to out, without
// ownership or grant statements, so it can be restored into a database of any
// name owned by any role.
func (m *Manager) Export(ctx context.Context, user, name string, out io.Writer) error {
	if err := ValidateName(user, name, MaxDatabaseName); err != nil {
		return err
	}

	if err := m.cfg.Dump(ctx, name, out); err != nil {
		return fmt.Errorf("%w: %w", ErrServer, err)
	}

	return nil
}

// Import runs the SQL of file (plain or gzip) in database name of user. The
// file is read as the account's Linux user (so it reaches only files the
// account may read); the SQL is loaded by connecting to the database AS its
// owning account role — a NOSUPERUSER role — not as the postgres superuser.
// This confines the dump: a malicious statement such as RESET ROLE lands on
// an unprivileged role, so it cannot reach another account's databases or run
// COPY ... TO PROGRAM. owners maps each of the database's account roles to its
// password; the password of the role that owns the database is used to
// connect. A failure the account should see is an *ImportError.
func (m *Manager) Import(ctx context.Context, as Identity, user, name, file string, owners map[string]string) error {
	if err := ValidateName(user, name, MaxDatabaseName); err != nil {
		return err
	}

	// Resolve the path first so "../" cannot escape the home, then confine it
	// there. The reader also runs as the account user, so this is defence in
	// depth on top of the caller's own path check.
	clean := filepath.Clean(file)
	if clean != as.Home && !strings.HasPrefix(clean, as.Home+"/") {
		return errors.New("the file to import must be in the account's home")
	}

	owner, err := m.databaseOwner(ctx, name)
	if err != nil {
		return err
	}

	if owner == "" {
		return &ImportError{Message: "give this database a user before importing into it"}
	}

	password, ok := owners[owner]
	if !ok || ValidatePassword(password) != nil {
		return errors.New("the owning role's password was not supplied")
	}

	return m.cfg.Import(ctx, as, name, owner, password, clean)
}

// databaseOwner returns the account role that owns database name, or "" when
// the postgres superuser owns it (no role to set).
func (m *Manager) databaseOwner(ctx context.Context, name string) (string, error) {
	rows, err := m.query(ctx, adminDatabase,
		"SELECT r.rolname FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba WHERE d.datname = "+literal(name)+";")
	if err != nil {
		return "", err
	}

	if len(rows) == 1 && accountNamePattern.MatchString(rows[0]) {
		return rows[0], nil
	}

	return "", nil
}

// dump runs pg_dump of database as the postgres user, writing to out.
func dump(ctx context.Context, database string, out io.Writer) error {
	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, sudoProgram, "-n", "-u", postgresUser,
		pgDumpProgram, "--no-owner", "--no-privileges", "--clean", "--if-exists", database)
	cmd.Stdout = out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, lastLine(stderr.String()))
	}

	return nil
}

// importAs runs `gzip -dcf file` as as and pipes it into `psql` connected to
// database AS the owning account role over a password (scram) loopback
// connection. The role is NOSUPERUSER, so the loaded SQL — however hostile —
// runs with only that role's privileges: no cross-account access, no
// COPY ... TO PROGRAM. role and password are validated by the caller.
func importAs(ctx context.Context, as Identity, database, role, password, file string) error {
	readEnv := []string{"HOME=" + as.Home, "PATH=/usr/bin:/bin", "LC_ALL=C.UTF-8"}

	read := exec.CommandContext(ctx, gzipProgram, "-dcf", "--", file)
	read.Dir = as.Home
	read.Env = readEnv
	read.SysProcAttr = sysProcAttr(as)
	read.Cancel = killGroup(read)
	read.WaitDelay = 10 * time.Second

	// Connect over TCP loopback (not the local socket) so scram password auth
	// signs the connecting role in as itself; no sudo, no superuser session,
	// no SET ROLE. PGPASSWORD is only visible to root (psql runs as root).
	load := exec.CommandContext(ctx, psqlProgram,
		"--no-psqlrc", "--set=ON_ERROR_STOP=1", "--quiet",
		"--host=127.0.0.1", "--username="+role, "--dbname="+database)
	load.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C.UTF-8", "PGPASSWORD=" + password, "PGCONNECT_TIMEOUT=10"}
	load.SysProcAttr = groupAttr()
	load.Cancel = killGroup(load)
	load.WaitDelay = 10 * time.Second

	var readErr, loadErr limitedBuffer
	readErr.limit, loadErr.limit = 4096, 4096
	read.Stderr = &readErr
	load.Stderr = &loadErr

	pipe, err := read.StdoutPipe()
	if err != nil {
		return err
	}

	load.Stdin = pipe

	if err := read.Start(); err != nil {
		return err
	}

	if err := load.Start(); err != nil {
		_ = read.Process.Kill()
		_ = read.Wait()

		return err
	}

	loadWait := load.Wait()
	readWait := read.Wait()

	switch {
	case loadWait != nil:
		return &ImportError{Message: firstLine(loadErr.String(), loadWait)}
	case readWait != nil:
		return &ImportError{Message: firstLine(readErr.String(), readWait)}
	}

	return nil
}

// killGroup kills a command's whole process group when its context is done.
func killGroup(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}

		return nil
	}
}

// firstLine is what a program said went wrong: its first ERROR line, else its
// first line with words, else its error.
func firstLine(output string, err error) string {
	fallback := ""

	for line := range strings.Lines(output) {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "ERROR") || strings.HasPrefix(line, "psql:") {
			return truncate(line)
		}

		if fallback == "" && line != "" {
			fallback = line
		}
	}

	if fallback != "" {
		return truncate(fallback)
	}

	return truncate(err.Error())
}

// lastLine is the last line of output.
func lastLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")

	return truncate(lines[len(lines)-1])
}

func truncate(message string) string {
	if len(message) > maxMessage {
		return message[:maxMessage] + "…"
	}

	return message
}

// limitedBuffer keeps the first limit bytes written to it.
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}

	return len(p), nil
}
