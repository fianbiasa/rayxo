package files

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
)

// ctxReader fails a read once ctx is done, so reading a foreign backup
// through — decompressing and all — stops when the request's deadline passes
// instead of running on. It wraps the decompressed stream, so even an archive
// that decompresses to far more than it holds is checked as it is read.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}

	return c.r.Read(p)
}

// Backups made by other panels (cPanel, DirectAdmin) become RAYXO backups
// through an import: ReadForeignBackup says which panel made a backup and
// what it holds, CopyForeignDumps hands over the dumps of its databases,
// and AddForeignHome copies the files of its home into a RAYXO backup at
// the places the panel chose. A backup is read whatever it is compressed
// with: gzip, zstd (DirectAdmin's default, through the zstd program) or
// nothing.

const (
	// KindCpanel and KindDirectAdmin are the panels a backup comes from.
	KindCpanel      = "cpanel"
	KindDirectAdmin = "directadmin"

	// maxForeignFile and maxForeignFiles bound one of the small files the
	// panel reads, and all of them.
	maxForeignFile  = 1 << 20
	maxForeignFiles = 32 << 20
	// maxForeignHomeNames bounds the names listed at the top of the home.
	maxForeignHomeNames = 10_000
	// maxZstdErrors bounds what is kept of what zstd says on failure.
	maxZstdErrors = 4 << 10
)

// ZstdProgram decompresses zstd. It is Debian's, which the installer puts
// on every server.
var ZstdProgram = "/usr/bin/zstd"

// ForeignInspection is what a backup of another panel holds.
type ForeignInspection struct {
	// Kind is the panel that made it.
	Kind string `json:"kind"`
	// Top is the folder a cPanel backup holds everything in, such as
	// cpmove-<user>; DirectAdmin's have none.
	Top string `json:"top"`
	// Files are the small files the panel reads, by their name in the
	// backup (under Top).
	Files map[string]string `json:"files"`
	// Databases are the dumps it holds.
	Databases []ForeignDump `json:"databases"`
	// Home are the files and folders at the top of the home.
	Home []ForeignHomeEntry `json:"home"`
	// Entries and Bytes are all the files and folders of the home, and
	// the size of its files.
	Entries int   `json:"entries"`
	Bytes   int64 `json:"bytes"`
}

// ForeignDump is the dump of one database.
type ForeignDump struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// ForeignHomeEntry is a file or folder at the top of the home, with the
// size of the files in it.
type ForeignHomeEntry struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Bytes int64  `json:"bytes"`
}

// HomeRule moves a part of the home of a backup: what is in From (a path
// in the home) goes into To in the RAYXO home; an empty To leaves it out.
// The rule with the longest From that holds a file applies; files no rule
// holds are left out.
type HomeRule struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Mail drops the caches Dovecot keeps in a mailbox (indexes and quota
	// counts): Dovecot here makes them again from the mail itself.
	Mail bool `json:"mail"`
}

// HomeCopied counts what a copy of the home took and left out. Refused
// counts the entries left out because their names could lead out of the
// home (non-UTF-8, traversal, absolute); RefusedNames are the first
// maxDroppedNames of those names, quoted so non-UTF-8 bytes are safe to show.
type HomeCopied struct {
	Entries      int      `json:"entries"`
	Dropped      int      `json:"dropped"`
	Refused      int      `json:"refused"`
	RefusedNames []string `json:"refused_names"`
}

// CheckHomeRule reports whether rule only names paths inside the home.
func CheckHomeRule(rule HomeRule) error {
	if name, ok := entryName(rule.From); !ok || name != rule.From {
		return fmt.Errorf("%q is not a path in the home", rule.From)
	}

	if rule.To == "" {
		return nil
	}

	if name, ok := entryName(rule.To); !ok || name != rule.To {
		return fmt.Errorf("%q is not a path in the home", rule.To)
	}

	if top, _, _ := strings.Cut(rule.To, "/"); top == BackupFolder {
		return fmt.Errorf("%q is a folder of the backup itself", rule.To)
	}

	return nil
}

// ReadForeignBackup reads a backup of cPanel or DirectAdmin through.
func ReadForeignBackup(ctx context.Context, backup io.Reader) (ForeignInspection, error) {
	inspection := ForeignInspection{Files: map[string]string{}, Databases: []ForeignDump{}, Home: []ForeignHomeEntry{}}

	stream, err := openArchive(backup)
	if err != nil {
		return inspection, err
	}

	inspection.Kind = stream.kind()
	in := &inspector{inspection: &inspection, home: map[string]*ForeignHomeEntry{}}
	reader := ctxReader{ctx: ctx, r: stream.reader}

	if inspection.Kind == KindDirectAdmin {
		err = readDirectAdmin(reader, in)
	} else {
		err = readCpanel(reader, in)
	}

	if closed := stream.close(err == nil); err == nil {
		err = closed
	}

	// A read stopped by the deadline is reported as such, not as a damaged
	// backup: the inner tar error only carries the context error's text.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return inspection, ctxErr
	}

	if err != nil {
		return inspection, err
	}

	// What it holds says whether it is one, once it was read through.
	if !in.holdsAccount() {
		return inspection, errorf(ReasonInvalid, "the file is not a cPanel or DirectAdmin backup")
	}

	for _, entry := range in.home {
		inspection.Home = append(inspection.Home, *entry)
	}

	slices.SortFunc(inspection.Home, func(a, b ForeignHomeEntry) int { return strings.Compare(a.Name, b.Name) })

	return inspection, nil
}

// CopyForeignDumps hands the dumps named in want (the database names) of a
// backup of kind to add, in the order the backup holds them. It stops once
// it found them all, and fails when one is missing.
func CopyForeignDumps(backup io.Reader, kind, top string, want []string, add func(name string, content io.Reader) error) error {
	left := map[string]bool{}
	for _, name := range want {
		left[name] = true
	}

	if len(left) == 0 {
		return nil
	}

	stream, err := openArchive(backup)
	if err != nil {
		return err
	}

	errFound := errors.New("found")
	visit := func(name string, header *tar.Header, content io.Reader) error {
		database, ok := cpanelDump(name, header)
		if kind == KindDirectAdmin {
			database, ok = directAdminDump(name, header)
		}

		if !ok || !left[database] {
			return nil
		}

		delete(left, database)

		if err := add(database, content); err != nil {
			return err
		}

		if len(left) == 0 {
			return errFound
		}

		return nil
	}

	if kind == KindDirectAdmin {
		err = walkTar(stream.reader, nil, visit)
	} else {
		err = walkCpanel(stream.reader, &top, nil, visit)
	}

	found := errors.Is(err, errFound)
	if closed := stream.close(err == nil); err == nil {
		err = closed
	}

	switch {
	case found:
		return nil
	case err != nil:
		return err
	case len(left) > 0:
		return errorf(ReasonNotFound, "the backup holds no dump of %s", strings.Join(slices.Sorted(maps.Keys(left)), ", "))
	}

	return nil
}

// AddForeignHome copies the home of a backup of kind into the backup, each
// file where rules put it. A folder a rule moves elsewhere is not copied
// itself, so the one here keeps its own permissions.
func (b *BackupWriter) AddForeignHome(backup io.Reader, kind, top string, rules []HomeRule) (HomeCopied, error) {
	var copied HomeCopied

	stream, err := openArchive(backup)
	if err != nil {
		return copied, err
	}

	var dropped droppedNames

	if kind == KindDirectAdmin {
		err = b.addDirectAdminHome(stream.reader, rules, &copied, &dropped)
	} else {
		err = b.addCpanelHome(stream.reader, top, rules, &copied, &dropped)
	}

	if closed := stream.close(err == nil); err == nil {
		err = closed
	}

	copied.Refused, copied.RefusedNames = dropped.count, dropped.names

	return copied, err
}

// addHomeEntry copies one entry of the home, at path inner, where rules
// put it.
func (b *BackupWriter) addHomeEntry(rules []HomeRule, inner string, header *tar.Header, content io.Reader, copied *HomeCopied) error {
	rule, found := homeRuleFor(rules, inner)
	if !found || rule.To == "" || (rule.Mail && mailCache(path.Base(inner))) {
		copied.Dropped++

		return nil
	}

	if inner == rule.From && rule.From != rule.To && header.Typeflag == tar.TypeDir {
		return nil
	}

	target, ok := entryName(rule.To + strings.TrimPrefix(inner, rule.From))
	if first, _, _ := strings.Cut(target, "/"); !ok || first == BackupFolder {
		copied.Dropped++

		return nil
	}

	entry := &tar.Header{Name: target, Mode: header.Mode & 0o777, ModTime: header.ModTime}

	switch header.Typeflag {
	case tar.TypeDir:
		entry.Typeflag, entry.Name = tar.TypeDir, target+"/"
	case tar.TypeReg:
		entry.Typeflag, entry.Size = tar.TypeReg, header.Size
	case tar.TypeSymlink:
		entry.Typeflag, entry.Linkname = tar.TypeSymlink, header.Linkname
	default:
		copied.Dropped++

		return nil
	}

	copied.Entries++

	return b.write(entry, content)
}

// inspector gathers what ReadForeignBackup reports.
type inspector struct {
	inspection *ForeignInspection
	home       map[string]*ForeignHomeEntry
	read       int
}

// homeEntry counts the entry at path inner of the home.
func (i *inspector) homeEntry(inner string, header *tar.Header) {
	i.inspection.Entries++

	top, _, nested := strings.Cut(inner, "/")
	entry := i.home[top]

	if entry == nil && len(i.home) < maxForeignHomeNames {
		entry = &ForeignHomeEntry{Name: top, Type: TypeDirectory}
		i.home[top] = entry
	}

	if entry != nil && !nested {
		entry.Type = foreignEntryType(header.Typeflag)
	}

	if header.Typeflag == tar.TypeReg {
		i.inspection.Bytes += header.Size

		if entry != nil {
			entry.Bytes += header.Size
		}
	}
}

// holdsAccount reports whether the backup read holds what its panel
// knows of an account: cp/<user> for cPanel, backup/user.conf for
// DirectAdmin.
func (i *inspector) holdsAccount() bool {
	if i.inspection.Kind == KindDirectAdmin {
		_, ok := i.inspection.Files[directAdminMeta+"/user.conf"]

		return ok
	}

	for name := range i.inspection.Files {
		if strings.HasPrefix(name, "cp/") {
			return true
		}
	}

	return false
}

func (i *inspector) dump(name string, size int64) {
	i.inspection.Databases = append(i.inspection.Databases, ForeignDump{Name: name, Size: size})
}

// file keeps the small file name, unless it or all of them are too large.
func (i *inspector) file(name string, header *tar.Header, content io.Reader) error {
	if header.Typeflag != tar.TypeReg || header.Size > maxForeignFile || i.read+int(header.Size) > maxForeignFiles {
		return nil
	}

	data, err := io.ReadAll(io.LimitReader(content, maxForeignFile))
	if err != nil {
		return errorf(ReasonInvalid, "the backup is damaged: %v", err)
	}

	i.read += len(data)
	i.inspection.Files[name] = string(data)

	return nil
}

// archive is the tar stream of a backup, decompressed.
type archive struct {
	reader *bufio.Reader
	// close ends the decompression. complete says the stream was read to
	// its end, so a damaged one fails now.
	close func(complete bool) error
}

// openArchive decompresses backup by what it starts with.
func openArchive(backup io.Reader) (*archive, error) {
	buffered := bufio.NewReaderSize(backup, 64<<10)
	magic, _ := buffered.Peek(4)

	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		compressed, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, errorf(ReasonInvalid, "the backup is damaged: %v", err)
		}

		return &archive{reader: bufio.NewReaderSize(compressed, 64<<10), close: func(bool) error { return compressed.Close() }}, nil
	case bytes.Equal(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		return zstdArchive(buffered)
	}

	return &archive{reader: buffered, close: func(bool) error { return nil }}, nil
}

// zstdArchive decompresses source with the zstd program.
func zstdArchive(source io.Reader) (*archive, error) {
	command := exec.Command(ZstdProgram, "-d", "-c", "-q")
	command.Stdin = source

	var stderr headBuffer
	command.Stderr = &stderr

	out, err := command.StdoutPipe()
	if err == nil {
		err = command.Start()
	}

	if err != nil {
		return nil, errorf(ReasonInvalid, "the backup is compressed with zstd, which cannot run here: %v", err)
	}

	return &archive{
		reader: bufio.NewReaderSize(out, 64<<10),
		close: func(complete bool) error {
			if !complete {
				_ = command.Process.Kill()
				_ = command.Wait()

				return nil
			}

			// A tar stream may end before the compressed one does.
			_, _ = io.Copy(io.Discard, out)

			if err := command.Wait(); err != nil {
				return errorf(ReasonInvalid, "the backup is damaged: %s", strings.TrimSpace(stderr.String()))
			}

			return nil
		},
	}, nil
}

// kind is the panel that made the backup, from its first entry: those of
// DirectAdmin start with its folder backup/, those of cPanel have one top
// folder of another name.
func (a *archive) kind() string {
	block, _ := a.reader.Peek(64 << 10)

	header, err := tar.NewReader(bytes.NewReader(block)).Next()
	if err != nil {
		return KindCpanel
	}

	if name, ok := entryName(header.Name); ok && (name == directAdminMeta || strings.HasPrefix(name, directAdminMeta+"/")) {
		return KindDirectAdmin
	}

	return KindCpanel
}

// maxDroppedNames bounds how many names of dropped entries are kept to
// report; the rest are only counted.
const maxDroppedNames = 100

// droppedNames collects the names of archive entries dropped because their
// names could lead out of the target (non-UTF-8, traversal, absolute). It
// keeps at most maxDroppedNames, each quoted with strconv.Quote so non-UTF-8
// bytes are escaped safely, and counts all of them. A nil *droppedNames
// collects nothing, so callers that do not report them pass nil.
type droppedNames struct {
	names []string
	count int
}

func (d *droppedNames) add(name string) {
	if d == nil {
		return
	}

	d.count++

	if len(d.names) < maxDroppedNames {
		d.names = append(d.names, strconv.Quote(name))
	}
}

// walkTar hands every entry of the tar stream to visit, by its cleaned
// name. Entries whose names could lead out are skipped, and their original
// names are collected in dropped (which may be nil).
func walkTar(stream io.Reader, dropped *droppedNames, visit func(name string, header *tar.Header, content io.Reader) error) error {
	reader := tar.NewReader(stream)

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return errorf(ReasonInvalid, "the backup is damaged: %v", err)
		}

		name, ok := entryName(header.Name)
		if !ok {
			dropped.add(header.Name)

			continue
		}

		if err := visit(name, header, reader); err != nil {
			return err
		}
	}
}

// homeRuleFor is the rule with the longest From that holds inner.
func homeRuleFor(rules []HomeRule, inner string) (HomeRule, bool) {
	var best HomeRule

	found := false

	for _, rule := range rules {
		if (inner == rule.From || strings.HasPrefix(inner, rule.From+"/")) && (!found || len(rule.From) > len(best.From)) {
			best, found = rule, true
		}
	}

	return best, found
}

// mailCache reports whether name is a cache Dovecot keeps in a mailbox
// and makes again by itself.
func mailCache(name string) bool {
	return strings.HasPrefix(name, "dovecot.index") || strings.HasPrefix(name, "dovecot.list.index") ||
		name == "dovecot.mailbox.log" || name == "dovecot-quota" || name == "maildirsize"
}

func foreignEntryType(flag byte) string {
	switch flag {
	case tar.TypeDir:
		return TypeDirectory
	case tar.TypeReg:
		return TypeFile
	case tar.TypeSymlink:
		return TypeLink
	}

	return TypeOther
}

// headBuffer keeps the start of what is written to it.
type headBuffer struct {
	bytes.Buffer
}

func (h *headBuffer) Write(p []byte) (int, error) {
	if room := maxZstdErrors - h.Len(); room > 0 {
		h.Buffer.Write(p[:min(len(p), room)])
	}

	return len(p), nil
}
