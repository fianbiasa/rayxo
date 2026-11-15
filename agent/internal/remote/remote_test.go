package remote

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

const (
	testKey         = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINpOPvVMXqP1ax8y6tKpUgt0+wywrxTez3mjT6wuickk"
	testFingerprint = "SHA256:mZDgZZnet2aZmN6lh5lZwX1rB6zlXHjz6180HOuXBOc"
)

// fakeRun records the programs a client runs and answers them.
type fakeRun struct {
	calls      []fakeCall
	answer     func(call fakeCall) ([]byte, error)
	knownHosts string
}

type fakeCall struct {
	env     []string
	input   string
	program string
	args    []string
}

func (f *fakeRun) client() Client {
	return New(Config{
		Rclone:  "/usr/bin/rclone",
		KeyScan: "/usr/bin/ssh-keyscan",
		Run: func(_ context.Context, env []string, input []byte, program string, args ...string) ([]byte, error) {
			call := fakeCall{env: env, input: string(input), program: program, args: args}
			f.calls = append(f.calls, call)

			if file := envValue(env, "RCLONE_CONFIG_RX_KNOWN_HOSTS_FILE"); file != "" {
				content, _ := os.ReadFile(file)
				f.knownHosts = string(content)
			}

			if slices.Contains(args, "obscure") {
				return []byte("OBSCURED\n"), nil
			}

			if f.answer != nil {
				return f.answer(call)
			}

			return nil, nil
		},
		Output: func(err error) string { return err.Error() },
	})
}

func envValue(env []string, key string) string {
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			return value
		}
	}

	return ""
}

func sftp() Destination {
	return Destination{Type: SFTP, Host: "backup.example.com", Port: 2200, User: "rayxo", Password: "s3cret pass", HostKey: testKey, Path: "/srv/backups"}
}

func TestDestinationsAreChecked(t *testing.T) {
	good := []Destination{
		{Type: FTP, Host: "ftp.example.com", User: "u", Password: "p", TLS: true, Path: "rayxo"},
		sftp(),
		{Type: S3, Bucket: "my-backups", Region: "auto", Endpoint: "https://abc.r2.cloudflarestorage.com", AccessKey: "AKIA", SecretKey: "secret", Path: "servers/one"},
		{Type: S3, Bucket: "my-backups", Endpoint: "http://127.0.0.1:9000", AccessKey: "minio", SecretKey: "minio-secret"},
		{Type: S3, Bucket: "aws-bucket", Region: "eu-west-1", AccessKey: "AKIA", SecretKey: "secret"},
	}

	for _, destination := range good {
		if err := destination.Validate(); err != nil {
			t.Errorf("%+v: %v", destination, err)
		}
	}

	bad := map[string]Destination{
		"unknown type":          {Type: "dropbox"},
		"host with a space":     {Type: FTP, Host: "ftp example.com", User: "u"},
		"no user":               {Type: FTP, Host: "ftp.example.com"},
		"climbing out":          {Type: FTP, Host: "ftp.example.com", User: "u", Path: "backups/../../etc"},
		"newline in password":   {Type: FTP, Host: "ftp.example.com", User: "u", Password: "a\nb"},
		"no accepted host key":  {Type: SFTP, Host: "backup.example.com", User: "u"},
		"bad host key":          {Type: SFTP, Host: "backup.example.com", User: "u", HostKey: "ssh-dss AAAA"},
		"bad bucket":            {Type: S3, Bucket: "Bad_Bucket", AccessKey: "a", SecretKey: "b"},
		"keys in the clear":     {Type: S3, Bucket: "my-backups", Endpoint: "http://s3.example.com", AccessKey: "a", SecretKey: "b"},
		"endpoint with a path":  {Type: S3, Bucket: "my-backups", Endpoint: "https://s3.example.com/x?y", AccessKey: "a", SecretKey: "b"},
		"absolute prefix on S3": {Type: S3, Bucket: "my-backups", Path: "/x", AccessKey: "a", SecretKey: "b"},
		"no secret key":         {Type: S3, Bucket: "my-backups", AccessKey: "a"},
	}

	for name, destination := range bad {
		if err := destination.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if ValidateFolder("server1.example.com") != nil || ValidateFolder("../x") == nil || ValidateFolder("") == nil {
		t.Error("folder names are not checked")
	}
}

func TestAnUploadKeepsThePasswordOffTheCommandLineAndAcceptsOnlyTheKnownHostKey(t *testing.T) {
	run := &fakeRun{}

	if err := run.client().Upload(context.Background(), sftp(), "/var/lib/rayxo/backups/budi-1.tar.gz", "server1.example.com", "accounts", "budi", "budi-1.tar.gz"); err != nil {
		t.Fatal(err)
	}

	if len(run.calls) != 2 {
		t.Fatalf("calls = %+v", run.calls)
	}

	obscure, upload := run.calls[0], run.calls[1]
	if obscure.input != "s3cret pass" || !slices.Equal(obscure.args, []string{"--config=", "obscure", "-"}) {
		t.Errorf("the password must reach rclone obscure on stdin: %+v", obscure)
	}

	for _, part := range append(upload.args, upload.env...) {
		if strings.Contains(part, "s3cret") {
			t.Errorf("the password shows in %q", part)
		}
	}

	if !slices.Contains(upload.args, "--config=") || !slices.Equal(upload.args[len(upload.args)-3:], []string{"copyto", "/var/lib/rayxo/backups/budi-1.tar.gz", "rx:/srv/backups/server1.example.com/accounts/budi/budi-1.tar.gz"}) {
		t.Errorf("args = %q", upload.args)
	}

	for key, want := range map[string]string{"TYPE": "sftp", "HOST": "backup.example.com", "PORT": "2200", "USER": "rayxo", "PASS": "OBSCURED", "SHELL_TYPE": "none", "DISABLE_HASHCHECK": "true"} {
		if got := envValue(upload.env, "RCLONE_CONFIG_RX_"+key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	if run.knownHosts != "[*]:2200 "+testKey+"\n" {
		t.Errorf("known_hosts = %q", run.knownHosts)
	}

	if _, err := os.Stat(envValue(upload.env, "HOME")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rclone's folder stayed behind: %v", err)
	}
}

func TestS3DestinationsNeedNoBucketRightsAndStayPrivate(t *testing.T) {
	run := &fakeRun{}
	destination := Destination{Type: S3, Bucket: "my-backups", Region: "auto", Endpoint: "https://abc.r2.cloudflarestorage.com/", AccessKey: "AKIA", SecretKey: "secret", Path: "servers"}

	if err := run.client().Delete(context.Background(), destination, "server1.example.com", "system", "rayxo-system-1.tar.gz"); err != nil {
		t.Fatal(err)
	}

	call := run.calls[0]
	if len(run.calls) != 1 || call.args[len(call.args)-1] != "rx:my-backups/servers/server1.example.com/system/rayxo-system-1.tar.gz" {
		t.Fatalf("calls = %+v", run.calls)
	}

	for key, want := range map[string]string{"PROVIDER": "Other", "ENDPOINT": "https://abc.r2.cloudflarestorage.com", "ACCESS_KEY_ID": "AKIA", "SECRET_ACCESS_KEY": "secret", "ACL": "private", "NO_CHECK_BUCKET": "true", "ENV_AUTH": "false", "REGION": "auto"} {
		if got := envValue(call.env, "RCLONE_CONFIG_RX_"+key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	run = &fakeRun{}
	_ = run.client().Delete(context.Background(), Destination{Type: S3, Bucket: "aws-bucket", AccessKey: "a", SecretKey: "b"}, "x")
	if got := envValue(run.calls[0].env, "RCLONE_CONFIG_RX_PROVIDER"); got != "AWS" {
		t.Errorf("provider without an endpoint = %q", got)
	}
}

func TestATestWritesAndDeletesAProbe(t *testing.T) {
	run := &fakeRun{}
	ftp := Destination{Type: FTP, Host: "ftp.example.com", User: "u", TLS: true}

	if err := run.client().Test(context.Background(), ftp, "server1.example.com"); err != nil {
		t.Fatal(err)
	}

	var commands []string
	for _, call := range run.calls {
		commands = append(commands, strings.Join(call.args[len(call.args)-2:], " "))
	}

	if !slices.Equal(commands, []string{"touch rx:server1.example.com/.rayxo-test", "deletefile rx:server1.example.com/.rayxo-test"}) {
		t.Errorf("commands = %q", commands)
	}

	if envValue(run.calls[0].env, "RCLONE_CONFIG_RX_EXPLICIT_TLS") != "true" || envValue(run.calls[0].env, "RCLONE_CONFIG_RX_PORT") != "21" {
		t.Errorf("env = %q", run.calls[0].env)
	}
}

func TestFailuresSayWhatRcloneSaid(t *testing.T) {
	run := &fakeRun{answer: func(fakeCall) ([]byte, error) {
		return nil, errors.New("2026/11/15 20:18:49 ERROR : Attempt 1/3 failed\n2026/11/15 20:18:49 ERROR : couldn't connect SSH: ssh: handshake failed: knownhosts: key mismatch")
	}}

	err := run.client().Upload(context.Background(), sftp(), "/tmp/x.tar.gz", "s", "system", "x.tar.gz")

	var failure *Error
	if !errors.As(err, &failure) || failure.Message != "the SFTP server presented another host key than the one accepted" {
		t.Errorf("err = %v", err)
	}

	run.answer = func(fakeCall) ([]byte, error) {
		return nil, errors.New("2026/11/15 20:18:49 ERROR : Failed to copy: 530 Login incorrect.")
	}

	if err := run.client().Upload(context.Background(), sftp(), "/tmp/x.tar.gz", "x"); err == nil || err.Error() != "Failed to copy: 530 Login incorrect." {
		t.Errorf("err = %v", err)
	}
}

func TestDeletingAFileThatIsNotThereSucceeds(t *testing.T) {
	run := &fakeRun{answer: func(fakeCall) ([]byte, error) {
		return nil, errors.New("2026/11/15 21:24:09 ERROR : Failed to deletefile: rx:remote/nope.tar.gz is a directory or doesn't exist")
	}}

	if err := run.client().Delete(context.Background(), sftp(), "nope.tar.gz"); err != nil {
		t.Errorf("err = %v", err)
	}

	run.answer = func(fakeCall) ([]byte, error) {
		return nil, errors.New("2026/11/15 21:24:09 ERROR : Failed to deletefile: permission denied")
	}

	if err := run.client().Delete(context.Background(), sftp(), "kept.tar.gz"); err == nil {
		t.Error("a refused delete passed for done")
	}
}

func TestListingsReadRclonesJSONAndAMissingFolderIsEmpty(t *testing.T) {
	run := &fakeRun{answer: func(call fakeCall) ([]byte, error) {
		return []byte(`[{"Path":"budi/budi-1.tar.gz","Size":42,"ModTime":"2026-11-15T10:00:00Z","IsDir":false}]`), nil
	}}

	entries, err := run.client().List(context.Background(), sftp(), "server1.example.com", "accounts")
	if err != nil || len(entries) != 1 || entries[0].Path != "budi/budi-1.tar.gz" || entries[0].Size != 42 {
		t.Fatalf("entries = %+v, err = %v", entries, err)
	}

	if !slices.Contains(run.calls[1].args, "--recursive") || !slices.Contains(run.calls[1].args, "--files-only") {
		t.Errorf("args = %q", run.calls[1].args)
	}

	run.answer = func(fakeCall) ([]byte, error) {
		return nil, errors.New("2026/11/15 20:18:49 ERROR : error listing: directory not found")
	}

	entries, err = run.client().List(context.Background(), sftp(), "nowhere")
	if err != nil || len(entries) != 0 {
		t.Errorf("a missing folder: %+v, %v", entries, err)
	}
}

func TestTheStrongestHostKeyIsOfferedForAcceptance(t *testing.T) {
	run := &fakeRun{answer: func(call fakeCall) ([]byte, error) {
		return []byte("# backup.example.com:22 SSH-2.0-OpenSSH_9.6\nbackup.example.com ssh-rsa AAAAB3NzaC1yc2E=\nbackup.example.com " + testKey + "\n"), nil
	}}

	key, err := run.client().HostKey(context.Background(), "backup.example.com", 0)
	if err != nil || key.Key != testKey+"\nssh-rsa AAAAB3NzaC1yc2E=" || key.Fingerprint != testFingerprint {
		t.Fatalf("key = %+v, err = %v", key, err)
	}

	if !slices.Equal(run.calls[0].args, []string{"-T", "10", "-p", "22", "backup.example.com"}) {
		t.Errorf("args = %q", run.calls[0].args)
	}

	if _, err := run.client().HostKey(context.Background(), "-oProxyCommand=x", 22); err == nil {
		t.Error("a host that is an option was accepted")
	}
}
