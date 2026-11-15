// Package remote copies the backups the agent keeps to a storage elsewhere,
// and back: an FTP or FTPS server, an SFTP server, or an S3 bucket, so a
// lost server does not take its backups with it. It drives rclone (the
// package of the distribution); the agent itself speaks none of these
// protocols.
//
// Secrets never show in the process list or on disk: each call hands the
// destination to rclone in its environment, with no config file, and
// passwords are obscured by rclone itself reading them on stdin. An SFTP
// server must present the host key the administrator accepted.
//
// On the destination, a server keeps its backups in a folder of its own,
// so several servers may share one:
//
//	<path>/<server>/accounts/<username>/<backup>
//	<path>/<server>/system/<backup>
package remote

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Types of destination.
const (
	FTP  = "ftp"
	SFTP = "sftp"
	S3   = "s3"
)

const (
	// remoteName is the remote each call defines in rclone's environment.
	remoteName = "rx"
	envPrefix  = "RCLONE_CONFIG_RX_"
	// maxSecret bounds passwords and keys.
	maxSecret = 256
	// maxSegments bounds the folders of a path.
	maxSegments = 8
)

var (
	hostPattern    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	segmentPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._ -]{0,99}$`)
	bucketPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	regionPattern  = regexp.MustCompile(`^[a-z0-9-]{0,40}$`)
	hostKeyPattern = regexp.MustCompile(`^(ssh-ed25519|ecdsa-sha2-nistp256|ecdsa-sha2-nistp384|ecdsa-sha2-nistp521|ssh-rsa) [A-Za-z0-9+/]+={0,2}$`)
	// keyPreference is the order a host key is chosen in from what the
	// server offers.
	keyPreference = []string{"ssh-ed25519", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "ssh-rsa"}
	// logPrefix is the time rclone puts before a message.
	logPrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} (ERROR|NOTICE|INFO|DEBUG) ?: ?`)
)

// Destination is where backups go, as the administrator set it.
type Destination struct {
	Type string `json:"type"`
	// Host, Port, User and Password are those of an FTP or SFTP server;
	// a Port of 0 is the protocol's own.
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	// TLS makes FTP explicit FTPS.
	TLS bool `json:"tls"`
	// HostKey is the SFTP server's keys the administrator accepted, one per
	// line, such as "ssh-ed25519 AAAA...": every kind the server offered,
	// since the client may settle on any of them.
	HostKey string `json:"host_key"`
	// Endpoint is an S3 service's address (https://...); empty for AWS.
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	// Path is the folder the backups go under: on an FTP or SFTP server,
	// from the login folder unless it starts with /; in a bucket, a prefix.
	Path string `json:"path"`
}

// Validate checks every field of the destination's type.
func (d Destination) Validate() error {
	switch d.Type {
	case FTP, SFTP:
		errs := []error{
			validateHost(d.Host),
			validatePort(d.Port),
			validateText("user", d.User, true, 128),
			validateText("password", d.Password, false, maxSecret),
			validatePath(d.Path, true),
		}

		if d.Type == SFTP && validateHostKeys(d.HostKey) != nil {
			errs = append(errs, errors.New("the SFTP server's host key must be accepted first"))
		}

		return errors.Join(errs...)
	case S3:
		errs := []error{
			validateEndpoint(d.Endpoint),
			validatePath(d.Path, false),
			validateText("access key", d.AccessKey, true, maxSecret),
			validateText("secret key", d.SecretKey, true, maxSecret),
		}

		if !bucketPattern.MatchString(d.Bucket) {
			errs = append(errs, fmt.Errorf("bucket %q is not a bucket name", d.Bucket))
		}

		if !regionPattern.MatchString(d.Region) {
			errs = append(errs, fmt.Errorf("region %q is not a region", d.Region))
		}

		if strings.ContainsFunc(d.AccessKey, unicode.IsSpace) {
			errs = append(errs, errors.New("the access key must not hold spaces"))
		}

		return errors.Join(errs...)
	default:
		return fmt.Errorf("type %q must be ftp, sftp or s3", d.Type)
	}
}

// ValidateFolder checks the name of a server's folder on the destination,
// and of an account's folder in it.
func ValidateFolder(name string) error {
	if len(name) > 253 || !hostPattern.MatchString(name) {
		return fmt.Errorf("folder %q must be a host name", name)
	}

	return nil
}

// validateHostKeys checks the accepted keys of an SFTP server, one per
// line.
func validateHostKeys(keys string) error {
	lines := strings.Split(keys, "\n")
	if keys == "" || len(lines) > len(keyPreference) {
		return errors.New("no host keys")
	}

	for _, line := range lines {
		if !hostKeyPattern.MatchString(line) {
			return fmt.Errorf("%q is not a host key", line)
		}
	}

	return nil
}

func validateHost(host string) error {
	if net.ParseIP(host) != nil || (len(host) <= 253 && hostPattern.MatchString(host)) {
		return nil
	}

	return fmt.Errorf("host %q is not a host name or IP address", host)
}

func validatePort(port int) error {
	if port < 0 || port > 65535 {
		return fmt.Errorf("port %d is out of range", port)
	}

	return nil
}

func validateText(field, value string, required bool, limit int) error {
	switch {
	case required && value == "":
		return fmt.Errorf("the %s is missing", field)
	case len(value) > limit:
		return fmt.Errorf("the %s is longer than %d characters", field, limit)
	case strings.ContainsFunc(value, unicode.IsControl):
		return fmt.Errorf("the %s holds control characters", field)
	}

	return nil
}

func validatePath(path string, absolute bool) error {
	if path == "" {
		return nil
	}

	rest := path
	if absolute {
		rest = strings.TrimPrefix(path, "/")
	}

	segments := strings.Split(rest, "/")
	if len(segments) > maxSegments {
		return fmt.Errorf("the path %q has more than %d folders", path, maxSegments)
	}

	for _, segment := range segments {
		if !segmentPattern.MatchString(segment) {
			return fmt.Errorf("the path %q may only hold folders of letters, digits, spaces, dots, dashes and underscores", path)
		}
	}

	return nil
}

// validateEndpoint accepts https, and plain http only on this machine (a
// local S3 service), so keys never cross a network in the clear.
func validateEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || validateHost(parsed.Hostname()) != nil {
		return fmt.Errorf("endpoint %q must be an address such as https://s3.example.com", endpoint)
	}

	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(parsed.Hostname()); parsed.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}

	return fmt.Errorf("endpoint %q must use https", endpoint)
}

// port is the destination's port, or its protocol's own.
func (d Destination) port() int {
	switch {
	case d.Port > 0:
		return d.Port
	case d.Type == SFTP:
		return 22
	default:
		return 21
	}
}

// remote is the rclone path of parts under the destination's folder.
func (d Destination) remote(parts ...string) string {
	root := d.Path
	if d.Type == S3 {
		root = strings.Trim(d.Bucket+"/"+d.Path, "/")
	}

	for _, part := range parts {
		if root == "" || strings.HasSuffix(root, "/") {
			root += part
		} else {
			root += "/" + part
		}
	}

	return remoteName + ":" + root
}

// env is the remote in rclone's environment.
func (d Destination) env(obscured, knownHosts string) []string {
	var env []string
	set := func(key, value string) { env = append(env, envPrefix+key+"="+value) }

	set("TYPE", d.Type)

	switch d.Type {
	case FTP, SFTP:
		set("HOST", d.Host)
		set("PORT", strconv.Itoa(d.port()))
		set("USER", d.User)

		if obscured != "" {
			set("PASS", obscured)
		}

		if d.Type == FTP && d.TLS {
			set("EXPLICIT_TLS", "true")
		}

		if d.Type == SFTP {
			// Only the accepted host key; and no commands on the server,
			// only file transfer.
			set("KNOWN_HOSTS_FILE", knownHosts)
			set("SHELL_TYPE", "none")
			set("DISABLE_HASHCHECK", "true")
			set("KEY_USE_AGENT", "false")
		}
	case S3:
		provider := "AWS"
		if d.Endpoint != "" {
			provider = "Other"
			set("ENDPOINT", strings.TrimSuffix(d.Endpoint, "/"))
		}

		set("PROVIDER", provider)
		set("ENV_AUTH", "false")
		set("ACCESS_KEY_ID", d.AccessKey)
		set("SECRET_ACCESS_KEY", d.SecretKey)
		set("ACL", "private")
		// The bucket must exist already; the keys need not be allowed to
		// make buckets.
		set("NO_CHECK_BUCKET", "true")

		if d.Region != "" {
			set("REGION", d.Region)
		}
	}

	return env
}

// knownHosts is the destination's accepted keys in known_hosts form. The
// file serves this one destination only, so its keys stand for any name:
// the SSH client also checks the address the name resolved to.
func (d Destination) knownHosts() string {
	host := "*"
	if d.port() != 22 {
		host = "[*]:" + strconv.Itoa(d.port())
	}

	var lines strings.Builder
	for _, key := range strings.Split(d.HostKey, "\n") {
		lines.WriteString(host + " " + key + "\n")
	}

	return lines.String()
}

// Entry is a file on the destination.
type Entry struct {
	Path    string `json:"Path"`
	Size    int64  `json:"Size"`
	ModTime string `json:"ModTime"`
	IsDir   bool   `json:"IsDir"`
}

// HostKey is a key an SFTP server presents.
type HostKey struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

// Config says how rclone and ssh-keyscan run.
type Config struct {
	Rclone  string
	KeyScan string
	// Run runs program with env added to the agent's environment and
	// input on stdin, and returns its stdout.
	Run func(ctx context.Context, env []string, input []byte, program string, args ...string) ([]byte, error)
	// Output is the stderr a failed run carries, for its message.
	Output func(err error) string
}

// Client copies backups to and from destinations.
type Client struct {
	config Config
}

// New makes a client.
func New(config Config) Client {
	return Client{config: config}
}

// commonArgs go before every rclone command: no config file, only errors
// on stderr, and bounded waits.
var commonArgs = []string{"--config=", "--log-level", "ERROR", "--stats", "0", "--contimeout", "30s", "--timeout", "5m", "--retries", "3", "--low-level-retries", "10"}

// call runs rclone with the destination in its environment.
func (c Client) call(ctx context.Context, d Destination, args ...string) ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}

	home, err := os.MkdirTemp("", "rayxo-rclone-")
	if err != nil {
		return nil, fmt.Errorf("make a folder for rclone: %w", err)
	}
	defer os.RemoveAll(home)

	obscured := ""
	if d.Password != "" && d.Type != S3 {
		out, err := c.config.Run(ctx, nil, []byte(d.Password), c.config.Rclone, "--config=", "obscure", "-")
		if err != nil {
			return nil, fmt.Errorf("obscure the password: %w", err)
		}

		obscured = strings.TrimSpace(string(out))
	}

	knownHosts := ""
	if d.Type == SFTP {
		knownHosts = filepath.Join(home, "known_hosts")
		if err := os.WriteFile(knownHosts, []byte(d.knownHosts()), 0o600); err != nil {
			return nil, fmt.Errorf("write the accepted host key: %w", err)
		}
	}

	env := append([]string{"HOME=" + home}, d.env(obscured, knownHosts)...)

	out, err := c.config.Run(ctx, env, nil, c.config.Rclone, append(slices.Clone(commonArgs), args...)...)
	if err != nil {
		return out, c.failure(err)
	}

	return out, nil
}

// failure is what went wrong, in rclone's words without its log times.
func (c Client) failure(err error) error {
	output := ""
	if c.config.Output != nil {
		output = c.config.Output(err)
	}

	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		if line := strings.TrimSpace(logPrefix.ReplaceAllString(scanner.Text(), "")); line != "" {
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		return &Error{Message: "rclone failed", Err: err}
	}

	message := lines[len(lines)-1]
	switch {
	case strings.Contains(output, "knownhosts: key mismatch"):
		message = "the SFTP server presented another host key than the one accepted"
	case len(message) > 400:
		message = message[:400]
	}

	return &Error{Message: message, Err: err}
}

// Error is a failed copy, with rclone's own message.
type Error struct {
	Message string
	Err     error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Err }

// Test writes and deletes a file in the server's folder on the destination,
// which proves the address, the login and the right to write.
func (c Client) Test(ctx context.Context, d Destination, server string) error {
	probe := d.remote(server, ".rayxo-test")

	if _, err := c.call(ctx, d, "touch", probe); err != nil {
		return err
	}

	_, err := c.call(ctx, d, "deletefile", probe)

	return err
}

// Upload copies the local file to path on the destination.
func (c Client) Upload(ctx context.Context, d Destination, local string, path ...string) error {
	_, err := c.call(ctx, d, "copyto", local, d.remote(path...))

	return err
}

// Download copies path on the destination to the local file.
func (c Client) Download(ctx context.Context, d Destination, local string, path ...string) error {
	_, err := c.call(ctx, d, "copyto", d.remote(path...), local)

	return err
}

// Delete deletes the file at path on the destination; a file that is not
// there is gone already.
func (c Client) Delete(ctx context.Context, d Destination, path ...string) error {
	_, err := c.call(ctx, d, "deletefile", d.remote(path...))

	var failure *Error
	if errors.As(err, &failure) && (strings.Contains(failure.Message, "doesn't exist") || strings.Contains(failure.Message, "not found")) {
		return nil
	}

	return err
}

// List lists the files under path on the destination, in every folder
// below it; a folder that does not exist holds nothing.
func (c Client) List(ctx context.Context, d Destination, path ...string) ([]Entry, error) {
	return c.list(ctx, d, []string{"--recursive", "--files-only"}, path...)
}

// Folders lists the folders right under path on the destination.
func (c Client) Folders(ctx context.Context, d Destination, path ...string) ([]string, error) {
	entries, err := c.list(ctx, d, []string{"--dirs-only"}, path...)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Path)
	}

	return names, nil
}

func (c Client) list(ctx context.Context, d Destination, flags []string, path ...string) ([]Entry, error) {
	out, err := c.call(ctx, d, append(append([]string{"lsjson", "--no-mimetype"}, flags...), d.remote(path...))...)
	if err != nil {
		var failure *Error
		if errors.As(err, &failure) && strings.Contains(failure.Message, "not found") {
			return []Entry{}, nil
		}

		return nil, err
	}

	var entries []Entry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("read the listing of the destination: %w", err)
	}

	return entries, nil
}

// HostKey asks an SFTP server for its keys, for the administrator to
// accept: every kind it offers, strongest first, and the fingerprint of the
// strongest to compare with the server.
func (c Client) HostKey(ctx context.Context, host string, port int) (HostKey, error) {
	if err := errors.Join(validateHost(host), validatePort(port)); err != nil {
		return HostKey{}, err
	}

	if port == 0 {
		port = 22
	}

	out, err := c.config.Run(ctx, nil, nil, c.config.KeyScan, "-T", "10", "-p", strconv.Itoa(port), host)
	if err != nil && len(out) == 0 {
		return HostKey{}, &Error{Message: "the SFTP server did not answer with its host key", Err: err}
	}

	offered := map[string]string{}
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) == 3 && !strings.HasPrefix(fields[0], "#") && hostKeyPattern.MatchString(fields[1]+" "+fields[2]) {
			offered[fields[1]] = fields[1] + " " + fields[2]
		}
	}

	var keys []string
	for _, kind := range keyPreference {
		if key, ok := offered[kind]; ok {
			keys = append(keys, key)
		}
	}

	if len(keys) == 0 {
		return HostKey{}, &Error{Message: "the SFTP server did not answer with a host key"}
	}

	return HostKey{Key: strings.Join(keys, "\n"), Fingerprint: Fingerprint(keys[0])}, nil
}

// Fingerprint is the SHA256 fingerprint of a host key (the first of
// several), as ssh shows it.
func Fingerprint(key string) string {
	first, _, _ := strings.Cut(key, "\n")

	_, encoded, ok := strings.Cut(first, " ")
	if !ok {
		return ""
	}

	blob, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}

	sum := sha256.Sum256(blob)

	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}
