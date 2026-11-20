package waf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recorder struct {
	calls [][]string
}

func (r *recorder) run(_ context.Context, program string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{program}, args...))

	return nil, nil
}

func (r *recorder) ran(substr string) bool {
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), substr) {
			return true
		}
	}

	return false
}

func newManager(t *testing.T) (*Manager, *recorder, string) {
	t.Helper()
	engine := filepath.Join(t.TempDir(), "modsec", "engine.conf")
	rec := &recorder{}

	return New(Config{EngineFile: engine, Run: rec.run}), rec, engine
}

func TestApplyWritesEngineAndReloads(t *testing.T) {
	cases := map[string]string{
		ModeOff:       "SecRuleEngine Off",
		ModeDetection: "SecRuleEngine DetectionOnly",
		ModeOn:        "SecRuleEngine On",
	}

	for mode, directive := range cases {
		t.Run(mode, func(t *testing.T) {
			m, rec, engine := newManager(t)

			if err := m.Apply(context.Background(), Settings{Mode: mode}); err != nil {
				t.Fatalf("apply %s: %v", mode, err)
			}

			content, err := os.ReadFile(engine)
			if err != nil {
				t.Fatalf("read engine file: %v", err)
			}
			if !strings.Contains(string(content), directive) {
				t.Fatalf("engine file %q does not set %q", content, directive)
			}
			if !strings.Contains(string(content), "<IfModule security2_module>") {
				t.Fatalf("engine file is not guarded by IfModule: %q", content)
			}
			if !rec.ran("apache2ctl configtest") {
				t.Fatal("Apache config was not tested")
			}
			if !rec.ran("reload-or-restart apache2") {
				t.Fatal("Apache was not reloaded")
			}
		})
	}
}

func TestApplyRejectsUnknownMode(t *testing.T) {
	m, rec, _ := newManager(t)

	if err := m.Apply(context.Background(), Settings{Mode: "block-everything"}); err == nil {
		t.Fatal("expected an error for an unknown mode")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("a rejected mode still ran commands: %v", rec.calls)
	}
}

func TestApplyWithoutChangeDoesNotReload(t *testing.T) {
	m, rec, _ := newManager(t)
	ctx := context.Background()

	if err := m.Apply(ctx, Settings{Mode: ModeOn}); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	rec.calls = nil

	if err := m.Apply(ctx, Settings{Mode: ModeOn}); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("an unchanged apply reloaded Apache: %v", rec.calls)
	}
}

func TestStatusReadsEngineFile(t *testing.T) {
	m, _, _ := newManager(t)
	ctx := context.Background()

	// A missing file reports off.
	if status, _ := m.Status(ctx); status.Mode != ModeOff {
		t.Fatalf("missing engine file: got %q, want off", status.Mode)
	}

	if err := m.Apply(ctx, Settings{Mode: ModeDetection}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if status, _ := m.Status(ctx); status.Mode != ModeDetection {
		t.Fatalf("status: got %q, want detection", status.Mode)
	}
}
