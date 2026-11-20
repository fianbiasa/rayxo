package actions

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	"rayxo/agent/internal/postgres"
	"rayxo/agent/internal/rpc"
)

// fakePostgresServer records the pg.* calls that reach PostgreSQL.
type fakePostgresServer struct {
	calls []string
	fail  error
	sizes []postgres.Size
}

func (f *fakePostgresServer) CreateDatabase(_ context.Context, user, name string) error {
	f.calls = append(f.calls, "create "+user+" "+name)

	return f.fail
}

func (f *fakePostgresServer) DeleteDatabase(_ context.Context, user, name string) error {
	f.calls = append(f.calls, "drop "+user+" "+name)

	return f.fail
}

func (f *fakePostgresServer) ApplyUser(_ context.Context, user, name, password string, databases []string) error {
	f.calls = append(f.calls, "user "+user+" "+name+" "+password+" ["+strings.Join(databases, ",")+"]")

	return f.fail
}

func (f *fakePostgresServer) DeleteUser(_ context.Context, user, name string) error {
	f.calls = append(f.calls, "drop user "+user+" "+name)

	return f.fail
}

func (f *fakePostgresServer) Report(context.Context) ([]postgres.Size, error) {
	f.calls = append(f.calls, "report")

	return f.sizes, f.fail
}

func (f *fakePostgresServer) Export(_ context.Context, user, name string, out io.Writer) error {
	f.calls = append(f.calls, "export "+user+" "+name)

	if f.fail != nil {
		return f.fail
	}

	_, err := io.WriteString(out, "-- dump of "+name+"\n")

	return err
}

func (f *fakePostgresServer) Import(_ context.Context, _ postgres.Identity, user, name, file string, owners map[string]string) error {
	f.calls = append(f.calls, "import "+user+" "+name+" "+file+" owners="+strconv.Itoa(len(owners)))

	return f.fail
}

const testPassword = "Abcd1234Efgh5678"

func callPostgres(t *testing.T, system *fakeSystem, server *fakePostgresServer, method string, params any) (json.RawMessage, error) {
	t.Helper()

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}

	registered, ok := postgresMethods(system, server, nil, &fakeHelper{})[method]
	if !ok {
		t.Fatalf("unknown method %s", method)
	}

	if registered.LockKey != nil {
		key, err := registered.LockKey(raw)
		if err != nil {
			return nil, err
		}

		if key != "account:budi" {
			t.Fatalf("%s locks %q", method, key)
		}
	}

	result, err := registered.Handler(context.Background(), raw)
	if err != nil {
		return nil, err
	}

	encoded, _ := json.Marshal(result)

	return encoded, nil
}

func TestPostgresMethodsAreRegistered(t *testing.T) {
	registry := rpc.NewRegistry()
	Register(registry, Config{})

	for _, name := range []string{
		"pg.create_database", "pg.delete_database", "pg.apply_user", "pg.delete_user", "pg.report", "pg.export", "pg.import",
	} {
		if !slices.Contains(registry.Names(), name) {
			t.Errorf("method %s is not registered", name)
		}
	}

	if postgresMethods(newFakeSystem(), &fakePostgresServer{}, nil, &fakeHelper{})["pg.report"].LockKey != nil {
		t.Fatal("pg.report only reads, but locks")
	}
}

func TestPostgresMethodsReachPostgres(t *testing.T) {
	system := newFakeSystem()
	system.addAccount("budi", 1001)
	server := &fakePostgresServer{}

	calls := []struct {
		method string
		params map[string]any
	}{
		{"pg.create_database", map[string]any{"username": "budi", "database": "budi_shop"}},
		{"pg.apply_user", map[string]any{"username": "budi", "user": "budi_app", "password": testPassword, "databases": []string{"budi_shop"}}},
		{"pg.delete_user", map[string]any{"username": "budi", "user": "budi_app"}},
		{"pg.delete_database", map[string]any{"username": "budi", "database": "budi_shop"}},
	}

	for _, call := range calls {
		if result, err := callPostgres(t, system, server, call.method, call.params); err != nil || string(result) != `{}` {
			t.Fatalf("%s: %s, %v", call.method, result, err)
		}
	}

	want := []string{
		"create budi budi_shop",
		"user budi budi_app " + testPassword + " [budi_shop]",
		"drop user budi budi_app",
		"drop budi budi_shop",
	}

	if !slices.Equal(server.calls, want) {
		t.Fatalf("calls = %q", server.calls)
	}
}

func TestPostgresMethodsRefuseWhatIsNotTheAccounts(t *testing.T) {
	invalid := map[string][]map[string]any{
		"pg.create_database": {
			{"username": "budi", "database": "sari_shop"},
			{"username": "budi", "database": "postgres"},
			{"username": "budi", "database": "budi_shop\"; DROP DATABASE postgres; --"},
			{"username": "root", "database": "root_x"},
		},
		"pg.apply_user": {
			{"username": "budi", "user": "budi_app", "password": "short"},
			{"username": "budi", "user": "budi_app", "password": testPassword, "databases": []string{"sari_shop"}},
			{"username": "budi", "user": "root", "password": testPassword},
			{"username": "budi", "user": "budi_app", "password": "has space 1234567890"},
		},
		"pg.delete_user":     {{"username": "budi", "user": "sari_app"}},
		"pg.delete_database": {{"username": "budi", "database": "template1"}},
	}

	for method, cases := range invalid {
		for _, params := range cases {
			system := newFakeSystem()
			system.addAccount("budi", 1001)
			server := &fakePostgresServer{}

			_, err := callPostgres(t, system, server, method, params)

			assertCode(t, err, rpc.CodeInvalidParams)

			if len(server.calls) != 0 {
				t.Fatalf("%s %v reached PostgreSQL", method, params)
			}
		}
	}
}

func TestPostgresCreatingNeedsTheAccountButDeletingDoesNot(t *testing.T) {
	system := newFakeSystem()
	server := &fakePostgresServer{}

	_, err := callPostgres(t, system, server, "pg.create_database", map[string]any{"username": "budi", "database": "budi_shop"})
	if agentErr := assertCode(t, err, rpc.CodeActionFailed); agentErr.Message != "account budi does not exist" {
		t.Fatalf("message = %q", agentErr.Message)
	}

	if _, err := callPostgres(t, system, server, "pg.delete_database", map[string]any{"username": "budi", "database": "budi_shop"}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if !slices.Equal(server.calls, []string{"drop budi budi_shop"}) {
		t.Fatalf("calls = %q", server.calls)
	}
}
