package ioncube

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// archive is an archive of loaders like ionCube's, for PHP 8.3 and 8.4
// (and a thread-safe build, which is not taken).
func archive(t *testing.T) []byte {
	t.Helper()

	var out bytes.Buffer

	compressed := gzip.NewWriter(&out)
	writer := tar.NewWriter(compressed)

	for name, content := range map[string]string{
		"ioncube/ioncube_loader_lin_8.3.so":    "loader 8.3",
		"ioncube/ioncube_loader_lin_8.4.so":    "loader 8.4",
		"ioncube/ioncube_loader_lin_8.4_ts.so": "thread safe",
		"ioncube/LICENSE.txt":                  "license",
	} {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}

		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}

	return out.Bytes()
}

type env struct {
	manager  *Manager
	php      string
	reloaded []string
	broken   map[string]bool
}

// newEnv serves ionCube's archive and lays out /etc/php for PHP 7.4, 8.3
// and 8.4. A PHP in broken does not start with the loader.
func newEnv(t *testing.T) *env {
	t.Helper()

	data := archive(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	t.Cleanup(server.Close)

	e := &env{php: t.TempDir(), broken: map[string]bool{}}

	for _, version := range []string{"7.4", "8.3", "8.4"} {
		for _, dir := range []string{"mods-available", "fpm/conf.d", "cli/conf.d"} {
			if err := os.MkdirAll(filepath.Join(e.php, version, dir), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	e.manager = New(Config{
		URL:    server.URL + "/ioncube_loaders_lin_x86-64.tar.gz",
		LibDir: filepath.Join(t.TempDir(), "ioncube"),
		PHPDir: e.php,
		Client: server.Client(),
		PHP: func(_ context.Context, version string, _ ...string) (string, error) {
			if e.broken[version] {
				return "", errors.New("PHP Fatal error: failed to load the loader")
			}

			if _, err := os.Stat(filepath.Join(e.php, version, "cli", "conf.d", linkName)); err != nil {
				return "PHP " + version + ".0 (cli)", nil
			}

			return "PHP " + version + ".0 (cli)\n    with the ionCube PHP Loader v15.0.0, Copyright (c) 2002-2026, by ionCube Ltd.", nil
		},
		Reload: func(_ context.Context, version string) error {
			e.reloaded = append(e.reloaded, version)

			return nil
		},
	})

	return e
}

func TestTheLoaderIsTurnedOnForEachVersionIonCubeMakesOneFor(t *testing.T) {
	e := newEnv(t)

	state, err := e.manager.Enable(context.Background(), []string{"7.4", "8.3", "8.4"})
	if err != nil {
		t.Fatal(err)
	}

	if !state.Enabled || !slices.Equal(state.Versions, []string{"8.3", "8.4"}) || !slices.Equal(state.Missing, []string{"7.4"}) || state.Loader != "15.0.0" {
		t.Errorf("state = %+v", state)
	}

	ini, _ := os.ReadFile(filepath.Join(e.php, "8.4", "mods-available", iniName))
	if !strings.Contains(string(ini), "zend_extension="+filepath.Join(e.manager.cfg.LibDir, "ioncube_loader_lin_8.4.so")) {
		t.Errorf("ini = %q", ini)
	}

	for _, sapi := range []string{"fpm", "cli"} {
		if target, err := os.Readlink(filepath.Join(e.php, "8.4", sapi, "conf.d", linkName)); err != nil || target != filepath.Join(e.php, "8.4", "mods-available", iniName) {
			t.Errorf("%s link = %q, %v", sapi, target, err)
		}
	}

	if loader, _ := os.ReadFile(filepath.Join(e.manager.cfg.LibDir, "ioncube_loader_lin_8.3.so")); string(loader) != "loader 8.3" {
		t.Errorf("loader = %q", loader)
	}

	if names, _ := filepath.Glob(filepath.Join(e.manager.cfg.LibDir, "*")); len(names) != 2 {
		t.Errorf("only the loaders of the versions here are kept: %v", names)
	}

	if !slices.Equal(e.reloaded, []string{"7.4", "8.3", "8.4"}) {
		t.Errorf("reloaded = %v", e.reloaded)
	}

	if status := e.manager.Status(context.Background(), []string{"7.4", "8.3", "8.4"}); !status.Enabled || !slices.Equal(status.Versions, []string{"8.3", "8.4"}) || status.Loader != "15.0.0" {
		t.Errorf("status = %+v", status)
	}
}

func TestAPHPThatDoesNotStartWithTheLoaderGoesWithout(t *testing.T) {
	e := newEnv(t)
	e.broken["8.4"] = true

	state, err := e.manager.Enable(context.Background(), []string{"8.3", "8.4"})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(state.Versions, []string{"8.3"}) || !slices.Equal(state.Missing, []string{"8.4"}) {
		t.Errorf("state = %+v", state)
	}

	if _, err := os.Lstat(filepath.Join(e.php, "8.4", "fpm", "conf.d", linkName)); !errors.Is(err, os.ErrNotExist) {
		t.Error("PHP 8.4 must not keep loading the loader")
	}
}

func TestTurningTheLoaderOffTakesItAway(t *testing.T) {
	e := newEnv(t)

	if _, err := e.manager.Enable(context.Background(), []string{"8.3", "8.4"}); err != nil {
		t.Fatal(err)
	}

	e.reloaded = nil

	state, err := e.manager.Disable(context.Background(), []string{"8.3", "8.4"})
	if err != nil || state.Enabled {
		t.Fatalf("state = %+v, err = %v", state, err)
	}

	for _, path := range []string{
		filepath.Join(e.php, "8.4", "fpm", "conf.d", linkName),
		filepath.Join(e.php, "8.4", "cli", "conf.d", linkName),
		filepath.Join(e.php, "8.4", "mods-available", iniName),
		e.manager.cfg.LibDir,
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s stayed", path)
		}
	}

	if !slices.Equal(e.reloaded, []string{"8.3", "8.4"}) || e.manager.Status(context.Background(), []string{"8.3", "8.4"}).Enabled {
		t.Errorf("reloaded = %v", e.reloaded)
	}
}

func TestAFailedDownloadChangesNothing(t *testing.T) {
	e := newEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	t.Cleanup(server.Close)
	e.manager.cfg.URL = server.URL

	if _, err := e.manager.Enable(context.Background(), []string{"8.4"}); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("err = %v", err)
	}

	if _, err := os.Lstat(filepath.Join(e.php, "8.4", "fpm", "conf.d", linkName)); !errors.Is(err, os.ErrNotExist) || len(e.reloaded) != 0 {
		t.Error("a failed download must change nothing")
	}
}
