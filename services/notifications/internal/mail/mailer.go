package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/karmarat/platform/packages/goshared/idgen"
)

// Config describes the relay and the sender identity.
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	// UseTLS upgrades the connection with STARTTLS. Development runs against
	// Mailpit, which speaks neither TLS nor AUTH, so both are opt-in rather
	// than assumed.
	UseTLS      bool
	FromAddress string
	FromName    string
	Timeout     time.Duration
	ProductName string
}

// Message is one email to send.
type Message struct {
	ToAddress   string
	ToName      string
	Subject     string
	Template    string
	Title       string
	Body        string
	ActionLabel string
	ActionURL   string
}

// ErrNotConfigured means no relay host was set, so nothing can be sent.
var ErrNotConfigured = errors.New("mail: no SMTP host configured")

// ErrUndeliverable marks a failure that retrying cannot fix — a malformed
// address, a missing template. The worker dead-letters these immediately
// instead of spending five attempts on them.
var ErrUndeliverable = errors.New("mail: message cannot be delivered")

// Mailer sends messages over SMTP.
type Mailer struct {
	cfg      Config
	renderer *renderer
}

// New builds a mailer and parses the templates once at boot, so a broken
// template is a startup failure rather than a message that silently never
// sends.
func New(cfg Config) (*Mailer, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.ProductName == "" {
		cfg.ProductName = "Karmarat"
	}
	if cfg.FromName == "" {
		cfg.FromName = cfg.ProductName
	}

	renderer, err := newRenderer()
	if err != nil {
		return nil, err
	}

	return &Mailer{cfg: cfg, renderer: renderer}, nil
}

// Configured reports whether a relay is available.
func (m *Mailer) Configured() bool { return m.cfg.Host != "" && m.cfg.FromAddress != "" }

// Send renders and delivers one message.
func (m *Mailer) Send(ctx context.Context, msg Message) error {
	if !m.Configured() {
		return ErrNotConfigured
	}

	to, err := parseAddress(msg.ToAddress)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUndeliverable, err)
	}

	html, text, err := m.renderer.Render(msg.Template, Content{
		Title:       msg.Title,
		Body:        msg.Body,
		ActionLabel: msg.ActionLabel,
		ActionURL:   msg.ActionURL,
		ProductName: m.cfg.ProductName,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUndeliverable, err)
	}

	body, err := m.compose(msg, to, html, text)
	if err != nil {
		return err
	}

	return m.deliver(ctx, to, body)
}

/* -------------------------------------------------------------------------- */
/* Composition                                                                */
/* -------------------------------------------------------------------------- */

// compose builds a multipart/alternative message.
//
// Every header value is sanitised before it is written. A subject line is built
// from event data — a job title, a candidate's name — and a bare newline inside
// one would end the header block early and let the rest be read as headers of
// its own, which is how a forged Bcc gets into an outgoing message.
func (m *Mailer) compose(msg Message, to *mail.Address, html, text string) ([]byte, error) {
	from := mail.Address{Name: sanitizeHeader(m.cfg.FromName), Address: m.cfg.FromAddress}
	if _, err := parseAddress(m.cfg.FromAddress); err != nil {
		return nil, fmt.Errorf("%w: sender address: %v", ErrUndeliverable, err)
	}
	recipient := mail.Address{Name: sanitizeHeader(msg.ToName), Address: to.Address}

	var out strings.Builder
	boundary := "rb_" + idgen.NewRaw()

	writeHeader(&out, "From", from.String())
	writeHeader(&out, "To", recipient.String())
	writeHeader(&out, "Subject", mime.QEncoding.Encode("utf-8", sanitizeHeader(msg.Subject)))
	writeHeader(&out, "Date", time.Now().UTC().Format(time.RFC1123Z))
	writeHeader(&out, "Message-ID", "<"+idgen.NewRaw()+"@"+messageIDDomain(m.cfg.FromAddress)+">")
	writeHeader(&out, "MIME-Version", "1.0")
	// Notifications are machine-generated. The headers below keep an
	// out-of-office reply from bouncing back into the queue and stop a mailing
	// list from being inferred from our volume.
	writeHeader(&out, "Auto-Submitted", "auto-generated")
	writeHeader(&out, "X-Auto-Response-Suppress", "All")
	writeHeader(&out, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	out.WriteString("\r\n")

	writer := multipart.NewWriter(&partWriter{builder: &out})
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, fmt.Errorf("mail: set boundary: %w", err)
	}

	plain, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"8bit"},
	})
	if err != nil {
		return nil, fmt.Errorf("mail: text part: %w", err)
	}
	if _, err := plain.Write([]byte(normalizeNewlines(text))); err != nil {
		return nil, fmt.Errorf("mail: text part: %w", err)
	}

	rich, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/html; charset=utf-8"},
		"Content-Transfer-Encoding": {"8bit"},
	})
	if err != nil {
		return nil, fmt.Errorf("mail: html part: %w", err)
	}
	if _, err := rich.Write([]byte(normalizeNewlines(html))); err != nil {
		return nil, fmt.Errorf("mail: html part: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("mail: close multipart: %w", err)
	}

	return []byte(out.String()), nil
}

// partWriter adapts a strings.Builder to io.Writer for multipart.
type partWriter struct{ builder *strings.Builder }

func (w *partWriter) Write(p []byte) (int, error) { return w.builder.Write(p) }

/* -------------------------------------------------------------------------- */
/* Delivery                                                                   */
/* -------------------------------------------------------------------------- */

// deliver runs the SMTP conversation.
//
// net/smtp's one-shot SendMail cannot be told about a context or made to skip
// AUTH on a server that does not offer it, and Mailpit offers neither AUTH nor
// TLS. Driving the client directly is a few more lines and makes both
// conditional, so the same code path serves a development relay and a
// production one.
func (m *Mailer) deliver(ctx context.Context, to *mail.Address, body []byte) error {
	dialer := net.Dialer{Timeout: m.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(m.cfg.Host, m.cfg.Port))
	if err != nil {
		return fmt.Errorf("mail: dial relay: %w", err)
	}
	// The deadline covers the whole conversation: an SMTP server that accepts
	// the connection and then stops answering would otherwise hold the worker
	// forever.
	deadline := time.Now().Add(m.cfg.Timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: open smtp session: %w", err)
	}
	defer func() { _ = client.Close() }()

	if m.cfg.UseTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("mail: relay does not offer STARTTLS but TLS is required")
		}
		if err := client.StartTLS(&tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: start tls: %w", err)
		}
	}

	// Credentials are only offered once the connection is encrypted. Sending a
	// password in the clear to a relay that happens not to advertise STARTTLS
	// would leak it to anything on the path.
	if m.cfg.Username != "" {
		if !m.cfg.UseTLS {
			return errors.New("mail: refusing to authenticate over an unencrypted connection")
		}
		auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mail: authenticate: %w", err)
		}
	}

	if err := client.Mail(m.cfg.FromAddress); err != nil {
		return fmt.Errorf("mail: sender rejected: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("mail: recipient rejected: %w", err)
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: open data: %w", err)
	}
	if _, err := writer.Write(body); err != nil {
		return fmt.Errorf("mail: write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("mail: finish body: %w", err)
	}

	return client.Quit()
}

/* -------------------------------------------------------------------------- */
/* Header hygiene                                                             */
/* -------------------------------------------------------------------------- */

// sanitizeHeader removes anything that could end a header line early.
//
// CR and LF are the injection vector; the other control characters are removed
// because no legitimate header value contains them and they confuse relays.
func sanitizeHeader(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}

func writeHeader(out *strings.Builder, name, value string) {
	out.WriteString(sanitizeHeader(name))
	out.WriteString(": ")
	out.WriteString(sanitizeHeader(value))
	out.WriteString("\r\n")
}

// parseAddress validates a recipient before it is put into an envelope.
func parseAddress(value string) (*mail.Address, error) {
	trimmed := sanitizeHeader(value)
	if trimmed == "" {
		return nil, errors.New("empty address")
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%q is not a valid address", trimmed)
	}
	return parsed, nil
}

// normalizeNewlines converts to CRLF, which SMTP requires, without doubling the
// ones that are already correct.
func normalizeNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\n", "\r\n")
}

// messageIDDomain takes the domain from the sender so the Message-ID matches
// the envelope, which several spam filters check.
func messageIDDomain(from string) string {
	if _, domain, found := strings.Cut(from, "@"); found && domain != "" {
		return sanitizeHeader(domain)
	}
	return "karmarat.local"
}
