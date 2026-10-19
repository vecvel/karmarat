// Package mail renders and sends this service's outbound email.
//
// What goes in a message is as much a security decision as a design one. A
// notification email carries a title, one line of context and a link to the
// place in the product where the thing happened — never a token, a password, a
// signed URL or the contents of a conversation. Mail is forwarded, quoted,
// archived by third parties and read on borrowed screens; anything in it that
// grants access has effectively been published.
package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
	"time"
)

//go:embed templates/*.html templates/*.txt
var templateFS embed.FS

// Content is what a template renders.
//
// The fields are deliberately few. A template that could reach into an
// arbitrary payload would eventually render whatever a future event happened to
// carry, which is how a candidate's phone number ends up in a recruiter's
// mailbox.
type Content struct {
	Title       string
	Body        string
	ActionLabel string
	ActionURL   string
	ProductName string
	// Content is the rendered body, injected into the layout. It is
	// template.HTML because it is our own output, never anything from a
	// request or an event.
	Content htmltemplate.HTML
	Year    int
}

// renderer holds the parsed template sets.
//
// HTML and plain text are parsed separately: html/template escapes for an HTML
// context, and running the text part through it would leave a recruiter reading
// "O&#39;Brien" in their plain-text client.
type renderer struct {
	html *htmltemplate.Template
	text *texttemplate.Template
}

func newRenderer() (*renderer, error) {
	html, err := htmltemplate.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("mail: parse html templates: %w", err)
	}
	text, err := texttemplate.ParseFS(templateFS, "templates/*.txt")
	if err != nil {
		return nil, fmt.Errorf("mail: parse text templates: %w", err)
	}
	return &renderer{html: html, text: text}, nil
}

// Render produces the HTML and plain-text bodies for a template.
//
// An unknown template name is an error rather than a fallback: a message whose
// template was renamed should stay in the queue and be visible as a failure,
// not go out with the wrong copy.
func (r *renderer) Render(name string, content Content) (html string, text string, err error) {
	if content.ProductName == "" {
		content.ProductName = "Karmarat"
	}
	if content.ActionLabel == "" {
		content.ActionLabel = "Open in Karmarat"
	}
	content.Year = time.Now().UTC().Year()

	if r.html.Lookup(name) == nil {
		return "", "", fmt.Errorf("mail: no html template named %q", name)
	}

	var body bytes.Buffer
	if err := r.html.ExecuteTemplate(&body, name, content); err != nil {
		return "", "", fmt.Errorf("mail: render %q: %w", name, err)
	}

	content.Content = htmltemplate.HTML(body.String()) //nolint:gosec // our own rendered output
	var page bytes.Buffer
	if err := r.html.ExecuteTemplate(&page, "layout", content); err != nil {
		return "", "", fmt.Errorf("mail: render layout: %w", err)
	}

	var plain bytes.Buffer
	if r.text.Lookup(name) != nil {
		if err := r.text.ExecuteTemplate(&plain, name, content); err != nil {
			return "", "", fmt.Errorf("mail: render text %q: %w", name, err)
		}
	} else {
		// A template without a plain-text twin still gets a readable
		// alternative; a message with no text part is a spam-score penalty.
		plain.WriteString(content.Title + "\n\n" + content.Body)
		if content.ActionURL != "" {
			plain.WriteString("\n\n" + content.ActionLabel + ": " + content.ActionURL)
		}
	}

	return page.String(), strings.TrimSpace(plain.String()) + "\n", nil
}
