package bandwidth

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type logs struct {
	t       *testing.T
	counter *Counter
	web     string
	mail    string
	ftp     string
}

func newLogs(t *testing.T, budget int64) *logs {
	t.Helper()

	dir := t.TempDir()
	webDir := filepath.Join(dir, "domains")

	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}

	counter := New(Config{
		WebLogDir: webDir,
		MailLog:   filepath.Join(dir, "mail.log"),
		FTPLog:    filepath.Join(dir, "transfer.log"),
		Budget:    budget,
		Location:  time.UTC,
		// A stub user database, so the tests never read the host's and a
		// pickup uid resolves to a known login.
		LookupID: func(uid string) (string, error) {
			switch uid {
			case "1001":
				return "budi", nil
			case "1002":
				return "siti", nil
			default:
				return "", os.ErrNotExist
			}
		},
	})

	return &logs{
		t:       t,
		counter: counter,
		web:     filepath.Join(webDir, "example.com.access.log"),
		mail:    filepath.Join(dir, "mail.log"),
		ftp:     filepath.Join(dir, "transfer.log"),
	}
}

func (l *logs) append(path string, lines ...string) {
	l.t.Helper()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		l.t.Fatal(err)
	}
	defer file.Close()

	if _, err := file.WriteString(strings.Join(lines, "")); err != nil {
		l.t.Fatal(err)
	}
}

func (l *logs) count(cursor Cursor) Report {
	l.t.Helper()

	report, err := l.counter.Count(cursor)
	if err != nil {
		l.t.Fatalf("count: %v", err)
	}

	return report
}

func webHit(day string, bytes string) string {
	return `203.0.113.7 - - [` + day + `/Nov/2026:10:00:00 +0000] "GET / HTTP/1.1" 200 ` + bytes + ` "-" "curl/8"` + "\n"
}

func TestCountsWhatWebsitesMailAndFTPMoved(t *testing.T) {
	l := newLogs(t, 1<<20)

	l.append(l.web,
		webHit("11", "1000"),
		webHit("12", "500"),
		webHit("12", "-"),
		`203.0.113.7 - - [12/Nov/2026:10:00:00 +0000] "GET / HTTP/1.1" 200 900`, // not finished yet
	)
	l.append(l.mail,
		"2026-11-12T10:00:00.000000+00:00 host postfix/qmgr[1]: AAA111: from=<info@example.com>, size=300, nrcpt=2 (queue active)\n",
		"2026-11-12T10:00:01.000000+00:00 host postfix/smtp[2]: AAA111: to=<a@gmail.com>, relay=gmail[1.2.3.4]:25, delay=1, dsn=2.0.0, status=sent (250 ok)\n",
		"2026-11-12T10:00:01.000000+00:00 host postfix/smtp[2]: AAA111: to=<b@gmail.com>, relay=gmail[1.2.3.4]:25, delay=1, dsn=4.0.0, status=deferred (try later)\n",
		"2026-11-12T10:01:00.000000+00:00 host postfix/qmgr[1]: BBB222: from=<someone@gmail.com>, size=700, nrcpt=1 (queue active)\n",
		"2026-11-12T10:01:01.000000+00:00 host postfix/lmtp[3]: BBB222: to=<Info@Example.ORG>, relay=host[private/dovecot-lmtp], delay=0.1, dsn=2.0.0, status=sent (250 Saved)\n",
	)
	l.append(l.ftp,
		"203.0.113.9 - budi_ftp [12/Nov/2026:09:00:00 +0000] \"GET /public_html/a.zip\" 200 2048\n",
		"203.0.113.9 - budi_ftp [12/Nov/2026:09:05:00 +0000] \"PUT /public_html/b.zip\" 200 4096\n",
		"203.0.113.9 - - [12/Nov/2026:09:06:00 +0000] \"GET /x\" 200 1\n",
	)

	report := l.count(nil)

	want := Report{
		Web:  []DomainBytes{{"example.com", "2026-11-11", 1000}, {"example.com", "2026-11-12", 500}},
		Mail: []DomainBytes{{"example.com", "2026-11-12", 300}, {"example.org", "2026-11-12", 700}},
		FTP:  []UserBytes{{"budi_ftp", "2026-11-12", 6144}},
	}

	if !reflect.DeepEqual(report.Web, want.Web) || !reflect.DeepEqual(report.Mail, want.Mail) || !reflect.DeepEqual(report.FTP, want.FTP) {
		t.Fatalf("unexpected report\n got %+v %+v %+v\nwant %+v %+v %+v", report.Web, report.Mail, report.FTP, want.Web, want.Mail, want.FTP)
	}

	if !report.Complete {
		t.Fatal("a count within its budget is complete")
	}

	// The unfinished line is left for later: it is counted once whole.
	l.append(l.web, "\n")

	again := l.count(report.Cursor)
	if !reflect.DeepEqual(again.Web, []DomainBytes{{"example.com", "2026-11-12", 900}}) || len(again.Mail) != 0 || len(again.FTP) != 0 {
		t.Fatalf("the second count read old lines again: %+v", again)
	}
}

func TestChargesLocalPickupMailToTheSubmittingLogin(t *testing.T) {
	l := newLogs(t, 1<<20)
	l.append(l.mail,
		// Local pickup mail (PHP mail(), cron) from uid 1001 (budi). The
		// envelope sender is forged to a victim's domain, but the message
		// must be charged to budi, not to victim.example.
		"2026-11-12T10:00:00.000000+00:00 host postfix/pickup[1]: AAA111: uid=1001 from=<forged@victim.example>\n",
		"2026-11-12T10:00:00.100000+00:00 host postfix/qmgr[2]: AAA111: from=<forged@victim.example>, size=500, nrcpt=1 (queue active)\n",
		"2026-11-12T10:00:01.000000+00:00 host postfix/smtp[3]: AAA111: to=<out@gmail.com>, relay=gmail[1.2.3.4]:25, delay=1, dsn=2.0.0, status=sent (250 ok)\n",
		// A multi-recipient local message: every delivered recipient is
		// charged to the same login (the pickup entry is not consumed on
		// the first delivery).
		"2026-11-12T10:01:00.000000+00:00 host postfix/pickup[1]: BBB222: uid=1002 from=<forged2@victim.example>\n",
		"2026-11-12T10:01:00.100000+00:00 host postfix/qmgr[2]: BBB222: from=<forged2@victim.example>, size=100, nrcpt=2 (queue active)\n",
		"2026-11-12T10:01:01.000000+00:00 host postfix/smtp[3]: BBB222: to=<a@gmail.com>, relay=gmail[1.2.3.4]:25, delay=1, dsn=2.0.0, status=sent (250 ok)\n",
		"2026-11-12T10:01:02.000000+00:00 host postfix/smtp[3]: BBB222: to=<b@gmail.com>, relay=gmail[1.2.3.4]:25, delay=1, dsn=2.0.0, status=sent (250 ok)\n",
		// Authenticated submission (no pickup line): still charged to the
		// from= domain, which reject_sender_login_mismatch constrains to
		// the login.
		"2026-11-12T10:02:00.000000+00:00 host postfix/qmgr[2]: CCC333: from=<news@example.com>, size=700, nrcpt=1 (queue active)\n",
		"2026-11-12T10:02:01.000000+00:00 host postfix/smtp[3]: CCC333: to=<x@gmail.com>, relay=gmail[1.2.3.4]:25, delay=1, dsn=2.0.0, status=sent (250 ok)\n",
		// Local delivery (lmtp) is charged to the recipient's domain as
		// before, even for a pickup message, not to the sender's login.
		"2026-11-12T10:03:00.000000+00:00 host postfix/pickup[1]: DDD444: uid=1001 from=<forged@victim.example>\n",
		"2026-11-12T10:03:00.100000+00:00 host postfix/qmgr[2]: DDD444: from=<forged@victim.example>, size=400, nrcpt=1 (queue active)\n",
		"2026-11-12T10:03:01.000000+00:00 host postfix/lmtp[3]: DDD444: to=<box@example.org>, relay=host[private/dovecot-lmtp], delay=0.1, dsn=2.0.0, status=sent (250 Saved)\n",
	)

	report := l.count(nil)

	wantUser := []UserBytes{{"budi", "2026-11-12", 500}, {"siti", "2026-11-12", 200}}
	if !reflect.DeepEqual(report.MailUser, wantUser) {
		t.Fatalf("mail per login\n got %+v\nwant %+v", report.MailUser, wantUser)
	}

	// example.com is the authenticated submission; example.org the local
	// delivery. The forged victim.example is charged nothing.
	wantDomain := []DomainBytes{{"example.com", "2026-11-12", 700}, {"example.org", "2026-11-12", 400}}
	if !reflect.DeepEqual(report.Mail, wantDomain) {
		t.Fatalf("mail per domain\n got %+v\nwant %+v", report.Mail, wantDomain)
	}

	for _, entry := range report.Mail {
		if entry.Domain == "victim.example" {
			t.Fatalf("a forged envelope sender was charged: %+v", report.Mail)
		}
	}
}

func TestFollowsARotatedLogToItsEnd(t *testing.T) {
	l := newLogs(t, 1<<20)
	l.append(l.web, webHit("12", "100"))
	cursor := l.count(nil).Cursor

	l.append(l.web, webHit("12", "20"))

	if err := os.Rename(l.web, l.web+".1"); err != nil {
		t.Fatal(err)
	}

	l.append(l.web, webHit("13", "3"))

	report := l.count(cursor)
	want := []DomainBytes{{"example.com", "2026-11-12", 20}, {"example.com", "2026-11-13", 3}}

	if !reflect.DeepEqual(report.Web, want) {
		t.Fatalf("got %+v, want %+v", report.Web, want)
	}
}

func TestReadsATruncatedLogFromItsStart(t *testing.T) {
	l := newLogs(t, 1<<20)
	l.append(l.ftp, "1.2.3.4 - a [12/Nov/2026:09:00:00 +0000] \"GET /x\" 200 5000\n")
	cursor := l.count(nil).Cursor

	if err := os.Truncate(l.ftp, 0); err != nil {
		t.Fatal(err)
	}

	l.append(l.ftp, "1.2.3.4 - a [12/Nov/2026:09:00:00 +0000] \"GET /y\" 200 7\n")

	if got := l.count(cursor).FTP; !reflect.DeepEqual(got, []UserBytes{{"a", "2026-11-12", 7}}) {
		t.Fatalf("got %+v", got)
	}
}

func TestStopsAtItsBudgetAndContinuesLater(t *testing.T) {
	line := webHit("12", "10")
	l := newLogs(t, int64(len(line)*2))
	l.append(l.web, line, line, line)

	first := l.count(nil)
	if first.Complete || !reflect.DeepEqual(first.Web, []DomainBytes{{"example.com", "2026-11-12", 20}}) {
		t.Fatalf("the first count should stop after two lines: %+v", first)
	}

	second := l.count(first.Cursor)
	if !second.Complete || !reflect.DeepEqual(second.Web, []DomainBytes{{"example.com", "2026-11-12", 10}}) {
		t.Fatalf("the second count should read the last line: %+v", second)
	}
}

func TestReadsOnlyItsOwnLogs(t *testing.T) {
	l := newLogs(t, 1<<20)
	secret := filepath.Join(t.TempDir(), "secret")

	if err := os.WriteFile(secret, []byte(webHit("12", "999")), 0o600); err != nil {
		t.Fatal(err)
	}

	l.append(l.web, webHit("12", "1"))

	// A foreign path, and a file in the log folder that is not a website's.
	l.append(filepath.Join(filepath.Dir(l.web), "..evil.access.log"), webHit("12", "5"))

	report := l.count(Cursor{secret: {Inode: 1, Offset: 0}})

	if _, ok := report.Cursor[secret]; ok {
		t.Fatal("the cursor kept a file that is not a log")
	}

	if !reflect.DeepEqual(report.Web, []DomainBytes{{"example.com", "2026-11-12", 1}}) {
		t.Fatalf("got %+v", report.Web)
	}
}

func TestSkipsLinesLongerThanItReads(t *testing.T) {
	l := newLogs(t, 1<<22)
	l.append(l.web, strings.Repeat("x", maxLine+10)+"\n", webHit("12", "4"))

	if got := l.count(nil).Web; !reflect.DeepEqual(got, []DomainBytes{{"example.com", "2026-11-12", 4}}) {
		t.Fatalf("got %+v", got)
	}
}
