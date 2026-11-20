package postgres

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// recorder is a fake psql: it captures every SQL input and returns a canned
// answer for the SELECTs the Manager makes.
type recorder struct {
	inputs    []string
	responses map[string]string
}

func (r *recorder) run(_ context.Context, input []byte, _ string, _ ...string) ([]byte, error) {
	sql := string(input)
	r.inputs = append(r.inputs, sql)

	for substr, resp := range r.responses {
		if strings.Contains(sql, substr) {
			return []byte(resp), nil
		}
	}

	return nil, nil
}

func (r *recorder) joined() string { return strings.Join(r.inputs, "\n") }

func newManager(responses map[string]string) (*Manager, *recorder) {
	rec := &recorder{responses: responses}

	return New(Config{Run: rec.run}), rec
}

func TestCreateDatabaseWhenMissing(t *testing.T) {
	m, rec := newManager(nil) // pg_database SELECT returns nothing: it does not exist.

	if err := m.CreateDatabase(context.Background(), "budi", "budi_shop"); err != nil {
		t.Fatal(err)
	}

	all := rec.joined()
	if !strings.Contains(all, `CREATE DATABASE "budi_shop"`) {
		t.Errorf("no CREATE DATABASE: %s", all)
	}

	if !strings.Contains(all, `REVOKE ALL ON DATABASE "budi_shop" FROM PUBLIC`) {
		t.Errorf("a fresh database must refuse PUBLIC: %s", all)
	}
}

func TestCreateDatabaseWhenPresentOnlyRevokes(t *testing.T) {
	m, rec := newManager(map[string]string{"FROM pg_database WHERE datname": "1"})

	if err := m.CreateDatabase(context.Background(), "budi", "budi_shop"); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(rec.joined(), "CREATE DATABASE") {
		t.Errorf("an existing database must not be created again: %s", rec.joined())
	}
}

func TestDeleteDatabaseClosesConnectionsFirst(t *testing.T) {
	m, rec := newManager(nil)

	if err := m.DeleteDatabase(context.Background(), "budi", "budi_shop"); err != nil {
		t.Fatal(err)
	}

	all := rec.joined()
	if !strings.Contains(all, "pg_terminate_backend") || !strings.Contains(all, `DROP DATABASE IF EXISTS "budi_shop"`) {
		t.Errorf("delete must close connections then drop: %s", all)
	}
}

func TestApplyUserCreatesRoleTakesOwnershipAndDropsTheRest(t *testing.T) {
	// The role already owns budi_old, which it should lose when it is granted
	// only budi_shop.
	m, rec := newManager(map[string]string{"JOIN pg_roles r ON r.oid = d.datdba": "budi_old"})

	if err := m.ApplyUser(context.Background(), "budi", "budi_app", "Abcd1234Efgh5678", []string{"budi_shop"}); err != nil {
		t.Fatal(err)
	}

	all := rec.joined()
	for _, want := range []string{
		`CREATE ROLE "budi_app" LOGIN PASSWORD 'Abcd1234Efgh5678' NOSUPERUSER NOCREATEDB NOCREATEROLE`,
		`ALTER DATABASE "budi_shop" OWNER TO "budi_app"`,
		`GRANT ALL ON DATABASE "budi_shop" TO "budi_app"`,
		`ALTER DATABASE "budi_old" OWNER TO "postgres"`,
		`ALTER SCHEMA public OWNER TO "budi_app"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

func TestDeleteUserReassignsThenDrops(t *testing.T) {
	m, rec := newManager(map[string]string{"FROM pg_roles WHERE rolname": "1"})

	if err := m.DeleteUser(context.Background(), "budi", "budi_app"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(rec.joined(), `DROP ROLE IF EXISTS "budi_app"`) {
		t.Errorf("delete must drop the role: %s", rec.joined())
	}
}

func TestReportKeepsOnlyAccountDatabases(t *testing.T) {
	m, _ := newManager(map[string]string{"pg_database_size": "budi_shop\t2048\npostgres\t999\ntemplate1\t5\nsari_blog\t4096"})

	sizes, err := m.Report(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	got := []Size{}
	got = append(got, sizes...)

	want := []Size{{Name: "budi_shop", Bytes: 2048}, {Name: "sari_blog", Bytes: 4096}}
	if !slices.Equal(got, want) {
		t.Fatalf("sizes = %+v", got)
	}
}

func TestValidationRejectsForeignAndUnsafeNames(t *testing.T) {
	m, rec := newManager(nil)
	ctx := context.Background()

	cases := []func() error{
		func() error { return m.CreateDatabase(ctx, "budi", "sari_shop") },
		func() error { return m.CreateDatabase(ctx, "budi", `budi_shop"; DROP DATABASE postgres`) },
		func() error { return m.CreateDatabase(ctx, "budi", "postgres") },
		func() error { return m.ApplyUser(ctx, "budi", "budi_app", "short", []string{"budi_shop"}) },
		func() error { return m.ApplyUser(ctx, "budi", "budi_app", "Abcd1234Efgh5678", []string{"sari_shop"}) },
		func() error { return m.DeleteUser(ctx, "budi", "sari_app") },
	}

	for i, run := range cases {
		if err := run(); err == nil {
			t.Errorf("case %d was allowed", i)
		}
	}

	if len(rec.inputs) != 0 {
		t.Fatalf("an invalid request reached PostgreSQL: %q", rec.inputs)
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("Abcd1234Efgh5678"); err != nil {
		t.Errorf("a 16-character alphanumeric password was rejected: %v", err)
	}

	for _, bad := range []string{"short", "has space 12345678", "symbols!" + strings.Repeat("a", 10), strings.Repeat("a", 200)} {
		if err := ValidatePassword(bad); err == nil {
			t.Errorf("%q was allowed", bad)
		}
	}
}
