// Package postgres manages the PostgreSQL databases and roles of hosting
// accounts. It runs psql as the postgres superuser (over the local socket,
// peer auth) with the SQL on stdin, so nothing of it shows in the process
// list.
//
// Every database and role of an account is named <username>_<name>, like the
// MariaDB ones. That prefix is how the agent tells whose they are: usernames
// have no underscore, so no account's prefix is the start of another's. Names
// only hold a-z, 0-9 and underscores and passwords only letters and digits,
// so nothing a caller sends can change the SQL; every method validates again.
//
// A database is owned by the role it is granted to, so the app that connects
// as that role has full control of it (create tables and so on). A fresh
// database refuses connections from PUBLIC, so only the roles it is granted
// to can reach it; roles are NOSUPERUSER NOCREATEDB NOCREATEROLE and cannot
// reach another account's databases. The cluster listens on localhost only.
package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"rayxo/agent/internal/runner"
	"rayxo/agent/internal/validate"
)

const (
	// MaxDatabaseName is the longest database name we allow; PostgreSQL's own
	// limit is 63 bytes.
	MaxDatabaseName = 63
	// MaxUserName keeps role names within the same limit.
	MaxUserName = 63

	sudoProgram  = "/usr/bin/sudo"
	psqlProgram  = "/usr/bin/psql"
	postgresUser = "postgres"
	// adminDatabase is the database psql connects to for cluster-wide work
	// (creating databases and roles); it always exists.
	adminDatabase = "postgres"
)

var (
	namePattern = regexp.MustCompile(`^[a-z0-9_]+$`)
	// passwordPattern matches the passwords the panel generates: letters and
	// digits only, so the password is safe inside an SQL string literal.
	passwordPattern = regexp.MustCompile(`^[A-Za-z0-9]{16,128}$`)
	// accountNamePattern matches the name of any account's database or role.
	accountNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]{2,15}_[a-z0-9_]+$`)
)

// ErrServer means PostgreSQL refused or failed a command; the agent log has
// its message.
var ErrServer = errors.New("the database server could not do it")

// Config holds what the Manager needs from the system, so tests can run
// without PostgreSQL.
type Config struct {
	// Run runs a program with input on stdin and returns its stdout, like
	// runner.RunInput. The Manager uses it to run psql as the postgres user.
	Run func(ctx context.Context, input []byte, program string, args ...string) ([]byte, error)
	// Dump runs pg_dump of a database as the postgres user, writing to out.
	Dump func(ctx context.Context, database string, out io.Writer) error
	// Import runs the SQL of file (plain or gzip) into database, read as the
	// account user and loaded by connecting as role (a NOSUPERUSER account
	// role) with its password. A failure the account should see is an
	// *ImportError.
	Import func(ctx context.Context, as Identity, database, role, password, file string) error
}

// DefaultConfig returns the operations used on a server.
func DefaultConfig() Config {
	return Config{
		Run:    run,
		Dump:   dump,
		Import: importAs,
	}
}

// Manager applies PostgreSQL changes. Callers must make sure only one call
// changes a given account at a time (the agent's account lock).
type Manager struct {
	cfg Config
}

// New returns a Manager for cfg. It does not touch the system.
func New(cfg Config) *Manager {
	return &Manager{cfg: cfg}
}

// ValidateName returns an error unless name is a valid name of a database
// (max MaxDatabaseName) or role (max MaxUserName) of account user: <user>_
// followed by lowercase letters, digits and underscores.
func ValidateName(user, name string, maxLength int) error {
	if err := validate.Username(user); err != nil {
		return err
	}

	suffix, ok := strings.CutPrefix(name, user+"_")
	if !ok || suffix == "" {
		return fmt.Errorf("name %q must start with %s_", name, user)
	}

	if !namePattern.MatchString(suffix) || len(name) > maxLength {
		return fmt.Errorf("name %q may only hold lowercase letters, digits and underscores, at most %d characters", name, maxLength)
	}

	return nil
}

// ValidatePassword returns an error unless password is one the panel
// generated: 16 to 128 letters and digits.
func ValidatePassword(password string) error {
	if !passwordPattern.MatchString(password) {
		return errors.New("password must be 16 to 128 letters and digits")
	}

	return nil
}

// CreateDatabase creates database name of user unless it exists, refusing
// connections from everyone until a role is granted it.
func (m *Manager) CreateDatabase(ctx context.Context, user, name string) error {
	if err := ValidateName(user, name, MaxDatabaseName); err != nil {
		return err
	}

	exists, err := m.databaseExists(ctx, name)
	if err != nil {
		return err
	}

	if !exists {
		// CREATE DATABASE cannot run inside a transaction block, so it is its
		// own statement; the REVOKE follows once it exists.
		if _, err := m.exec(ctx, adminDatabase, "CREATE DATABASE "+identifier(name)+" ENCODING 'UTF8';"); err != nil {
			return err
		}
	}

	_, err = m.exec(ctx, adminDatabase, "REVOKE ALL ON DATABASE "+identifier(name)+" FROM PUBLIC;")

	return err
}

// DeleteDatabase drops database name of user. A database that does not exist
// succeeds. Connections to it are closed first so the drop does not fail.
func (m *Manager) DeleteDatabase(ctx context.Context, user, name string) error {
	if err := ValidateName(user, name, MaxDatabaseName); err != nil {
		return err
	}

	_, err := m.exec(ctx, adminDatabase,
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = "+literal(name)+";\n"+
			"DROP DATABASE IF EXISTS "+identifier(name)+";")

	return err
}

// ApplyUser creates role name of user, or updates it: it gets the password,
// may log in, and owns exactly databases (which must be databases of user and
// must exist), and nothing else. Running it again changes nothing.
func (m *Manager) ApplyUser(ctx context.Context, user, name, password string, databases []string) error {
	if err := ValidateName(user, name, MaxUserName); err != nil {
		return err
	}

	if err := ValidatePassword(password); err != nil {
		return err
	}

	for _, database := range databases {
		if err := ValidateName(user, database, MaxDatabaseName); err != nil {
			return err
		}
	}

	owned, err := m.ownedDatabases(ctx, name)
	if err != nil {
		return err
	}

	var sql strings.Builder

	// Create or update the role: it may log in with the password, but can do
	// nothing else on the cluster.
	sql.WriteString("DO $$ BEGIN\n")
	sql.WriteString("  IF EXISTS (SELECT FROM pg_roles WHERE rolname = " + literal(name) + ") THEN\n")
	sql.WriteString("    ALTER ROLE " + identifier(name) + " LOGIN PASSWORD " + literal(password) + " NOSUPERUSER NOCREATEDB NOCREATEROLE;\n")
	sql.WriteString("  ELSE\n")
	sql.WriteString("    CREATE ROLE " + identifier(name) + " LOGIN PASSWORD " + literal(password) + " NOSUPERUSER NOCREATEDB NOCREATEROLE;\n")
	sql.WriteString("  END IF;\n")
	sql.WriteString("END $$;\n")

	// Take ownership and full access of the databases it should have.
	for _, database := range databases {
		sql.WriteString("GRANT ALL ON DATABASE " + identifier(database) + " TO " + identifier(name) + ";\n")
		sql.WriteString("ALTER DATABASE " + identifier(database) + " OWNER TO " + identifier(name) + ";\n")
	}

	// Drop access and ownership of the databases it no longer should have.
	for _, database := range owned {
		if !slices.Contains(databases, database) {
			sql.WriteString("REVOKE ALL ON DATABASE " + identifier(database) + " FROM " + identifier(name) + ";\n")
			sql.WriteString("ALTER DATABASE " + identifier(database) + " OWNER TO " + identifier(postgresUser) + ";\n")
		}
	}

	if _, err := m.exec(ctx, adminDatabase, sql.String()); err != nil {
		return err
	}

	// The public schema of each owned database must belong to the role so it
	// can create tables there. This runs inside each database.
	for _, database := range databases {
		if _, err := m.exec(ctx, database, "ALTER SCHEMA public OWNER TO "+identifier(name)+";\nGRANT ALL ON SCHEMA public TO "+identifier(name)+";"); err != nil {
			return err
		}
	}

	return nil
}

// DeleteUser drops role name of user, handing anything it still owns back to
// the postgres superuser first. A role that does not exist succeeds.
func (m *Manager) DeleteUser(ctx context.Context, user, name string) error {
	if err := ValidateName(user, name, MaxUserName); err != nil {
		return err
	}

	return m.dropRole(ctx, name)
}

// dropRole reassigns and drops role, which the caller validated. It connects
// to each database the role owns to drop what depends on it there.
func (m *Manager) dropRole(ctx context.Context, name string) error {
	exists, err := m.roleExists(ctx, name)
	if err != nil || !exists {
		return err
	}

	owned, err := m.ownedDatabases(ctx, name)
	if err != nil {
		return err
	}

	for _, database := range owned {
		if _, err := m.exec(ctx, adminDatabase, "ALTER DATABASE "+identifier(database)+" OWNER TO "+identifier(postgresUser)+";"); err != nil {
			return err
		}

		if _, err := m.exec(ctx, database, "REASSIGN OWNED BY "+identifier(name)+" TO "+identifier(postgresUser)+";\nDROP OWNED BY "+identifier(name)+";"); err != nil {
			return err
		}
	}

	_, err = m.exec(ctx, adminDatabase, "REASSIGN OWNED BY "+identifier(name)+" TO "+identifier(postgresUser)+";\nDROP OWNED BY "+identifier(name)+";\nDROP ROLE IF EXISTS "+identifier(name)+";")

	return err
}

// RemoveAccount drops every database and role of user, so an account created
// later with the same username starts without them.
func (m *Manager) RemoveAccount(ctx context.Context, user string) error {
	if err := validate.Username(user); err != nil {
		return err
	}

	databases, roles, err := m.accountObjects(ctx, user)
	if err != nil {
		return err
	}

	for _, database := range databases {
		if err := m.DeleteDatabase(ctx, user, database); err != nil {
			return err
		}
	}

	for _, role := range roles {
		if err := m.dropRole(ctx, role); err != nil {
			return err
		}
	}

	return nil
}

// LockAccount stops every role of user from logging in while the account is
// suspended (NOLOGIN), or lets them in again (LOGIN).
func (m *Manager) LockAccount(ctx context.Context, user string, lock bool) error {
	if err := validate.Username(user); err != nil {
		return err
	}

	_, roles, err := m.accountObjects(ctx, user)
	if err != nil {
		return err
	}

	state := "LOGIN"
	if lock {
		state = "NOLOGIN"
	}

	var sql strings.Builder

	for _, role := range roles {
		sql.WriteString("ALTER ROLE " + identifier(role) + " " + state + ";\n")
	}

	if sql.Len() == 0 {
		return nil
	}

	_, err = m.exec(ctx, adminDatabase, sql.String())

	return err
}

// Size is the size of one account database.
type Size struct {
	Name  string `json:"name"`
	Bytes int64  `json:"size_bytes"`
}

// Report returns the size of every account database.
func (m *Manager) Report(ctx context.Context) ([]Size, error) {
	rows, err := m.query(ctx, adminDatabase,
		"SELECT datname, pg_database_size(oid) FROM pg_database WHERE NOT datistemplate ORDER BY datname;")
	if err != nil {
		return nil, err
	}

	sizes := []Size{}

	for _, row := range rows {
		name, value, ok := strings.Cut(row, "\t")
		if !ok || !accountNamePattern.MatchString(name) {
			continue
		}

		bytes, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}

		sizes = append(sizes, Size{Name: name, Bytes: bytes})
	}

	return sizes, nil
}

// databaseExists reports whether a database with name exists.
func (m *Manager) databaseExists(ctx context.Context, name string) (bool, error) {
	rows, err := m.query(ctx, adminDatabase, "SELECT 1 FROM pg_database WHERE datname = "+literal(name)+";")

	return len(rows) > 0, err
}

// roleExists reports whether a role with name exists.
func (m *Manager) roleExists(ctx context.Context, name string) (bool, error) {
	rows, err := m.query(ctx, adminDatabase, "SELECT 1 FROM pg_roles WHERE rolname = "+literal(name)+";")

	return len(rows) > 0, err
}

// ownedDatabases returns the account databases role owns.
func (m *Manager) ownedDatabases(ctx context.Context, role string) ([]string, error) {
	rows, err := m.query(ctx, adminDatabase,
		"SELECT d.datname FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba WHERE r.rolname = "+literal(role)+" ORDER BY d.datname;")
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(rows, func(name string) bool { return !accountNamePattern.MatchString(name) }), nil
}

// accountObjects returns the names of the databases and roles of user.
func (m *Manager) accountObjects(ctx context.Context, user string) ([]string, []string, error) {
	prefix := literal(user + `\_%`)

	databases, err := m.query(ctx, adminDatabase, "SELECT datname FROM pg_database WHERE datname LIKE "+prefix+" ORDER BY datname;")
	if err != nil {
		return nil, nil, err
	}

	roles, err := m.query(ctx, adminDatabase, "SELECT rolname FROM pg_roles WHERE rolname LIKE "+prefix+" ORDER BY rolname;")
	if err != nil {
		return nil, nil, err
	}

	keep := func(name string) bool { return ValidateName(user, name, MaxDatabaseName) != nil }

	return slices.DeleteFunc(databases, keep), slices.DeleteFunc(roles, keep), nil
}

// exec runs sql in database, which stops at the first statement that fails.
func (m *Manager) exec(ctx context.Context, database, sql string) ([]byte, error) {
	output, err := m.cfg.Run(ctx, []byte(sql+"\n"), sudoProgram, psqlArgs(database)...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServer, err)
	}

	return output, nil
}

// query runs sql in database and returns its rows, one tab-separated line each.
func (m *Manager) query(ctx context.Context, database, sql string) ([]string, error) {
	output, err := m.exec(ctx, database, sql)
	if err != nil {
		return nil, err
	}

	var rows []string

	for line := range strings.Lines(string(bytes.TrimSpace(output))) {
		if line = strings.TrimRight(line, "\r\n"); line != "" {
			rows = append(rows, line)
		}
	}

	return rows, nil
}

// psqlArgs runs psql as the postgres user, connected to database, stopping at
// the first error, with tab-separated unaligned rows and no startup file.
func psqlArgs(database string) []string {
	return []string{
		"-n", "-u", postgresUser, psqlProgram,
		"--no-psqlrc", "--set=ON_ERROR_STOP=1", "--tuples-only", "--no-align",
		"--field-separator=\t", "--quiet", "--dbname=" + database,
	}
}

// identifier quotes a validated name as an SQL identifier.
func identifier(name string) string {
	return `"` + name + `"`
}

// literal quotes a validated value as an SQL string. Validated names and
// passwords hold no quotes; the backslash of a LIKE pattern is kept as is
// (standard_conforming_strings is on by default).
func literal(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// run runs psql as the postgres user with input on stdin.
func run(ctx context.Context, input []byte, program string, args ...string) ([]byte, error) {
	return runner.RunInput(ctx, input, program, args...)
}
