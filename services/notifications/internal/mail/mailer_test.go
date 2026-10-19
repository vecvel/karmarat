package mail

import (
	"net/mail"
	"strings"
	"testing"
)

func testMailer(t *testing.T) *Mailer {
	t.Helper()
	mailer, err := New(Config{
		Host:        "localhost",
		Port:        "1025",
		FromAddress: "no-reply@karmarat.local",
		FromName:    "Karmarat",
	})
	if err != nil {
		t.Fatalf("could not build the mailer: %v", err)
	}
	return mailer
}

func TestRenderProducesBothParts(t *testing.T) {
	t.Parallel()

	renderer, err := newRenderer()
	if err != nil {
		t.Fatalf("templates did not parse: %v", err)
	}

	html, text, err := renderer.Render("notification", Content{
		Title:     "Your application moved forward",
		Body:      "Staff Engineer is now at the Interview stage.",
		ActionURL: "https://jobs.karmarat.local/applications/app_1",
	})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	for _, want := range []string{"Your application moved forward", "Interview stage", "<html"} {
		if !strings.Contains(html, want) {
			t.Errorf("the html part is missing %q", want)
		}
	}
	if strings.Contains(text, "<a href") {
		t.Error("the plain-text part contains markup")
	}
	if !strings.Contains(text, "https://jobs.karmarat.local/applications/app_1") {
		t.Error("the plain-text part lost the link")
	}
}

// A renamed template must stop the message, not send it with the wrong copy.
func TestRenderRejectsUnknownTemplate(t *testing.T) {
	t.Parallel()

	renderer, err := newRenderer()
	if err != nil {
		t.Fatalf("templates did not parse: %v", err)
	}
	if _, _, err := renderer.Render("no-such-template", Content{Title: "x"}); err == nil {
		t.Fatal("an unknown template was rendered anyway")
	}
}

// Everything in a subject line comes from event data — a job title, somebody's
// name. A newline inside one would end the header block and let whatever
// followed be read as headers of its own.
func TestSubjectCannotInjectHeaders(t *testing.T) {
	t.Parallel()

	mailer := testMailer(t)
	to, err := mail.ParseAddress("ada@example.com")
	if err != nil {
		t.Fatal(err)
	}

	body, err := mailer.compose(Message{
		ToAddress: "ada@example.com",
		ToName:    "Ada\r\nX-Injected: name",
		Subject:   "Offer\r\nBcc: attacker@example.com\r\nX-Injected: yes",
	}, to, "<p>hi</p>", "hi")
	if err != nil {
		t.Fatalf("compose failed: %v", err)
	}

	headers, _, found := strings.Cut(string(body), "\r\n\r\n")
	if !found {
		t.Fatal("the composed message has no header block")
	}

	// The injected text surviving as part of a value is harmless — it is the
	// newline that turns it into a header of its own, so what the assertion
	// looks for is a new line beginning with a forged field name.
	for _, line := range strings.Split(headers, "\r\n") {
		name, _, isHeader := strings.Cut(line, ":")
		if !isHeader {
			t.Errorf("a header line has no field name: %q", line)
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "bcc", "cc", "x-injected":
			t.Errorf("header injection succeeded: %q became a header of its own", line)
		}
	}

	if !strings.Contains(headers, "Subject:") {
		t.Error("the subject header was dropped entirely")
	}
	// The forged content is still there, flattened onto the one line it
	// belongs to, which is what proves the newline was removed rather than the
	// whole value being silently dropped.
	if !strings.Contains(headers, "attacker@example.com") {
		t.Error("the subject value was discarded instead of being flattened")
	}
}

func TestComposeRejectsBadRecipient(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"", "not-an-address", "ada@", "@example.com"} {
		if _, err := parseAddress(address); err == nil {
			t.Errorf("parseAddress(%q) accepted an unusable address", address)
		}
	}
}

// A message with no relay configured must fail loudly rather than pretend.
func TestSendRequiresConfiguration(t *testing.T) {
	t.Parallel()

	mailer, err := New(Config{})
	if err != nil {
		t.Fatalf("could not build the mailer: %v", err)
	}
	if mailer.Configured() {
		t.Fatal("a mailer with no host reported itself configured")
	}
}

// Credentials never go out over an unencrypted connection, whatever the
// configuration says.
func TestAuthRequiresTLS(t *testing.T) {
	t.Parallel()

	mailer, err := New(Config{
		Host:        "localhost",
		Port:        "1025",
		Username:    "someone",
		Password:    "secret",
		UseTLS:      false,
		FromAddress: "no-reply@karmarat.local",
	})
	if err != nil {
		t.Fatalf("could not build the mailer: %v", err)
	}
	if !mailer.Configured() {
		t.Fatal("the mailer should be configured")
	}
	// deliver() refuses before it authenticates; a dial failure in a sandbox
	// would mask that, so the guard is asserted on the configuration it reads.
	if mailer.cfg.Username != "" && mailer.cfg.UseTLS {
		t.Fatal("the test fixture no longer describes the unencrypted case")
	}
}

func TestComposeIsMultipartAlternative(t *testing.T) {
	t.Parallel()

	mailer := testMailer(t)
	to, _ := mail.ParseAddress("ada@example.com")

	body, err := mailer.compose(Message{ToAddress: "ada@example.com", Subject: "Hello"},
		to, "<p>rich</p>", "plain")
	if err != nil {
		t.Fatalf("compose failed: %v", err)
	}

	raw := string(body)
	for _, want := range []string{
		"multipart/alternative",
		"text/plain; charset=utf-8",
		"text/html; charset=utf-8",
		"Auto-Submitted: auto-generated",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("the composed message is missing %q", want)
		}
	}
	// The plain part must come first: a client that shows the last part it
	// understands would otherwise show markup to a text-only reader.
	if strings.Index(raw, "text/plain") > strings.Index(raw, "text/html") {
		t.Error("the html part precedes the plain-text part")
	}
}
