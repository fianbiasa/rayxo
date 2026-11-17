package files

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"testing"
)

// TestReadForeignBackupHonorsContext checks that reading a foreign backup
// through stops when the request's context is done, rather than decompressing
// and reading the whole archive regardless.
func TestReadForeignBackupHonorsContext(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(bytes.Repeat([]byte("rayxo backup body "), 1024)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ReadForeignBackup(ctx, &buf); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadForeignBackup with a cancelled context = %v; want context.Canceled", err)
	}
}
