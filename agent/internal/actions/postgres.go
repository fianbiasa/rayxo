package actions

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"

	"rayxo/agent/internal/files"
	"rayxo/agent/internal/postgres"
	"rayxo/agent/internal/rpc"
)

// postgresServer is what the pg.* actions need. *postgres.Manager implements
// it.
type postgresServer interface {
	CreateDatabase(ctx context.Context, user, name string) error
	DeleteDatabase(ctx context.Context, user, name string) error
	ApplyUser(ctx context.Context, user, name, password string, databases []string) error
	DeleteUser(ctx context.Context, user, name string) error
	Report(ctx context.Context) ([]postgres.Size, error)
	Export(ctx context.Context, user, name string, out io.Writer) error
	Import(ctx context.Context, as postgres.Identity, user, name, file string, owners map[string]string) error
}

// pgDatabaseParams are the params of pg.create_database and
// pg.delete_database.
type pgDatabaseParams struct {
	Username string `json:"username"`
	Database string `json:"database"`
}

func (p pgDatabaseParams) account() string { return p.Username }

func (p pgDatabaseParams) validate() error {
	return postgres.ValidateName(p.Username, p.Database, postgres.MaxDatabaseName)
}

// pgApplyUserParams are the params of pg.apply_user: the whole state of one
// role. The password is plaintext inside the (encrypted) task payload, as
// PostgreSQL stores a SCRAM verifier it computes itself, not a hash the panel
// could make.
type pgApplyUserParams struct {
	Username  string   `json:"username"`
	User      string   `json:"user"`
	Password  string   `json:"password"`
	Databases []string `json:"databases"`
}

func (p pgApplyUserParams) account() string { return p.Username }

func (p pgApplyUserParams) validate() error {
	errs := []error{
		postgres.ValidateName(p.Username, p.User, postgres.MaxUserName),
		postgres.ValidatePassword(p.Password),
	}

	for _, name := range p.Databases {
		errs = append(errs, postgres.ValidateName(p.Username, name, postgres.MaxDatabaseName))
	}

	return firstError(errs...)
}

// pgUserParams are the params of pg.delete_user.
type pgUserParams struct {
	Username string `json:"username"`
	User     string `json:"user"`
}

func (p pgUserParams) account() string { return p.Username }

func (p pgUserParams) validate() error {
	return postgres.ValidateName(p.Username, p.User, postgres.MaxUserName)
}

// pgExportParams are the params of pg.export: the database, and the download
// transfer the gzipped dump goes to.
type pgExportParams struct {
	Username string `json:"username"`
	Database string `json:"database"`
	Transfer string `json:"transfer"`
}

func (p pgExportParams) account() string { return p.Username }

func (p pgExportParams) validate() error {
	return errors.Join(
		postgres.ValidateName(p.Username, p.Database, postgres.MaxDatabaseName),
		files.CheckTransferID(p.Transfer),
	)
}

// pgOwnerParam is one of the database's account roles and its password, so the
// agent can connect as the role that owns the database to load the import.
type pgOwnerParam struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// pgImportParams are the params of pg.import: the database, the file in the
// account's home (relative to it) to run in it, and the account's roles (with
// passwords) so the import connects as the database's owning role rather than
// the superuser. Remove deletes the file afterwards, as the account, also when
// the import failed.
type pgImportParams struct {
	Username string         `json:"username"`
	Database string         `json:"database"`
	Path     string         `json:"path"`
	Owners   []pgOwnerParam `json:"owners"`
	Remove   bool           `json:"remove"`
}

func (p pgImportParams) account() string { return p.Username }

func (p pgImportParams) validate() error {
	if p.Path == "" {
		return errors.New("path is required")
	}

	errs := []error{
		postgres.ValidateName(p.Username, p.Database, postgres.MaxDatabaseName),
		files.CheckPath(p.Path),
	}

	for _, owner := range p.Owners {
		errs = append(errs,
			postgres.ValidateName(p.Username, owner.Name, postgres.MaxUserName),
			postgres.ValidatePassword(owner.Password),
		)
	}

	return errors.Join(errs...)
}

// ownerMap turns the owners list into a role-to-password map.
func (p pgImportParams) ownerMap() map[string]string {
	owners := make(map[string]string, len(p.Owners))
	for _, owner := range p.Owners {
		owners[owner.Name] = owner.Password
	}

	return owners
}

// pgReport is the result of pg.report.
type pgReport struct {
	Databases []postgres.Size `json:"databases"`
}

// postgresActions implements the pg.* methods.
type postgresActions struct {
	accounts  accountActions
	server    postgresServer
	transfers fileTransfers
	helper    fileHelper
}

// postgresMethods returns the pg.* methods by name. Those that change an
// account lock it, like the account.* methods.
func postgresMethods(system accountSystem, server postgresServer, transfers fileTransfers, helper fileHelper) map[string]rpc.Method {
	actions := postgresActions{accounts: accountActions{system: system}, server: server, transfers: transfers, helper: helper}

	return map[string]rpc.Method{
		"pg.create_database": {Handler: actions.createDatabase, LockKey: webLockKey[pgDatabaseParams]},
		"pg.delete_database": {Handler: actions.deleteDatabase, LockKey: webLockKey[pgDatabaseParams]},
		"pg.apply_user":      {Handler: actions.applyUser, LockKey: webLockKey[pgApplyUserParams]},
		"pg.delete_user":     {Handler: actions.deleteUser, LockKey: webLockKey[pgUserParams]},
		"pg.report":          {Handler: actions.report},
		"pg.export":          {Handler: actions.export},
		"pg.import":          {Handler: actions.importFile, LockKey: webLockKey[pgImportParams]},
	}
}

// createDatabase implements pg.create_database.
func (d postgresActions) createDatabase(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[pgDatabaseParams](raw)
	if err != nil {
		return nil, err
	}

	if _, err := d.accounts.requireAccount(ctx, params.Username); err != nil {
		return nil, err
	}

	if err := d.server.CreateDatabase(ctx, params.Username, params.Database); err != nil {
		return nil, postgresError(err, "create database "+params.Database)
	}

	return struct{}{}, nil
}

// deleteDatabase implements pg.delete_database. A database that does not exist
// succeeds, also for an account that no longer exists.
func (d postgresActions) deleteDatabase(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[pgDatabaseParams](raw)
	if err != nil {
		return nil, err
	}

	if err := d.server.DeleteDatabase(ctx, params.Username, params.Database); err != nil {
		return nil, postgresError(err, "delete database "+params.Database)
	}

	return struct{}{}, nil
}

// applyUser implements pg.apply_user: it creates the role or updates it to the
// given password and ownership of exactly the given databases.
func (d postgresActions) applyUser(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[pgApplyUserParams](raw)
	if err != nil {
		return nil, err
	}

	if _, err := d.accounts.requireAccount(ctx, params.Username); err != nil {
		return nil, err
	}

	if err := d.server.ApplyUser(ctx, params.Username, params.User, params.Password, params.Databases); err != nil {
		return nil, postgresError(err, "set up database user "+params.User)
	}

	return struct{}{}, nil
}

// deleteUser implements pg.delete_user. A role that does not exist succeeds.
func (d postgresActions) deleteUser(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[pgUserParams](raw)
	if err != nil {
		return nil, err
	}

	if err := d.server.DeleteUser(ctx, params.Username, params.User); err != nil {
		return nil, postgresError(err, "delete database user "+params.User)
	}

	return struct{}{}, nil
}

// report implements pg.report: the size of every account database.
func (d postgresActions) report(ctx context.Context, raw json.RawMessage) (any, error) {
	if err := rpc.DecodeParams(raw, &struct{}{}); err != nil {
		return nil, err
	}

	sizes, err := d.server.Report(ctx)
	if err != nil {
		return nil, postgresError(err, "measure the databases")
	}

	return pgReport{Databases: sizes}, nil
}

// export implements pg.export: a gzipped dump of the database, written to the
// download transfer for the panel to send.
func (d postgresActions) export(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[pgExportParams](raw)
	if err != nil {
		return nil, err
	}

	if _, err := d.accounts.requireAccount(ctx, params.Username); err != nil {
		return nil, err
	}

	target, err := d.transfers.CreateDownload(params.Transfer)
	if err != nil {
		slog.Warn("database export refused", "transfer", params.Transfer, "error", err)

		return nil, rpc.Errorf(rpc.CodeActionFailed, "the download could not be prepared")
	}

	compressed := gzip.NewWriter(target)
	err = d.server.Export(ctx, params.Username, params.Database, compressed)

	err = errors.Join(err, compressed.Close(), target.Close())
	if err != nil {
		_ = os.Remove(d.transfers.DownloadPath(params.Transfer))

		return nil, postgresError(err, "export database "+params.Database)
	}

	return struct{}{}, nil
}

// importFile implements pg.import: it runs the SQL of a file in the account's
// home in the database (see postgres.Import).
func (d postgresActions) importFile(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[pgImportParams](raw)
	if err != nil {
		return nil, err
	}

	user, err := d.accounts.requireAccount(ctx, params.Username)
	if err != nil {
		return nil, err
	}

	as := postgres.Identity{UID: user.UID, GID: user.GID, Home: user.Home}
	err = d.server.Import(ctx, as, params.Username, params.Database, user.Home+"/"+params.Path, params.ownerMap())

	if params.Remove {
		account := files.Account{UID: user.UID, GID: user.GID, Home: user.Home}

		var result json.RawMessage
		if removeErr := d.helper.Run(ctx, account, files.OpRemove, files.PathsParams{Paths: []string{params.Path}}, nil, &result); removeErr != nil {
			slog.Warn("could not remove an imported file", "user", params.Username, "path", params.Path, "error", removeErr)
		}
	}

	var failed *postgres.ImportError
	if errors.As(err, &failed) {
		return nil, rpc.Errorf(rpc.CodeActionFailed, "%s", failed.Message)
	}

	if err != nil {
		return nil, postgresError(err, "import into database "+params.Database)
	}

	return struct{}{}, nil
}

// postgresError turns a PostgreSQL failure into a message panel users can
// read; PostgreSQL's own message stays in the agent log.
func postgresError(err error, what string) error {
	if errors.Is(err, postgres.ErrServer) {
		slog.Error("PostgreSQL refused a command", "task", what, "error", err)

		return rpc.Errorf(rpc.CodeActionFailed, "PostgreSQL could not %s; the agent log has the details", what)
	}

	return err
}
