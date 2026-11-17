package files

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"testing"
)

// buildRayxoBackup makes a minimal RAYXO backup: the backup folder, its
// manifest and settings, then a home file of bodyBytes bytes.
func buildRayxoBackup(t *testing.T, bodyBytes int) []byte {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	reg := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: int64(len(data)), Mode: 0o600}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: BackupFolder + "/", Mode: 0o700}); err != nil {
		t.Fatal(err)
	}
	reg(BackupManifest, []byte(`{"format":1}`))
	reg(BackupSettings, []byte("{}"))

	// Random, so the archive does not compress down to a few bytes and cutting
	// its end really removes archive data.
	body := make([]byte, bodyBytes)
	if _, err := rand.Read(body); err != nil {
		t.Fatal(err)
	}
	reg("home/big.txt", body)

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func TestReadBackupMetaVerified(t *testing.T) {
	archive := buildRayxoBackup(t, 64*1024)

	// A whole backup reads through, with its manifest and settings.
	meta, err := ReadBackupMetaVerified(context.Background(), bytes.NewReader(archive))
	if err != nil || meta.Manifest != `{"format":1}` || meta.Settings != "{}" {
		t.Fatalf("whole backup: meta = %+v, err = %v", meta, err)
	}

	// A backup cut short still has its manifest at the start, so the old
	// meta-only read accepts it, but the verifying read refuses it.
	truncated := archive[:len(archive)-4096]

	if _, err := ReadBackupMeta(bytes.NewReader(truncated)); err != nil {
		t.Fatalf("ReadBackupMeta rejected a truncated backup it reads only the start of: %v", err)
	}

	if _, err := ReadBackupMetaVerified(context.Background(), bytes.NewReader(truncated)); err == nil {
		t.Fatal("ReadBackupMetaVerified accepted a truncated backup")
	}
}
