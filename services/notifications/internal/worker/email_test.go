package worker

import "testing"

// A link in an email is composed from a portal and a path, and the portal comes
// from the recipient's principal type. A candidate sent to the company portal
// would land on a sign-in page for an account they do not have.
func TestActionURL(t *testing.T) {
	t.Parallel()

	worker := &Email{portals: PortalURLs{
		Company: "https://acme.karmarat.test/",
		Jobs:    "https://jobs.karmarat.test",
		Root:    "https://root.karmarat.test",
	}}

	tests := []struct {
		name string
		data map[string]any
		want string
	}{
		{
			name: "a company recipient goes to the company portal",
			data: map[string]any{"portal": "company", "link": "/applications/app_1"},
			want: "https://acme.karmarat.test/applications/app_1",
		},
		{
			name: "a candidate goes to the jobs portal",
			data: map[string]any{"portal": "candidate", "link": "/offers/ofr_1"},
			want: "https://jobs.karmarat.test/offers/ofr_1",
		},
		{
			name: "platform staff go to the console",
			data: map[string]any{"portal": "platform", "link": "/support/tickets/tkt_1"},
			want: "https://root.karmarat.test/support/tickets/tkt_1",
		},
		{
			name: "a link that is not a path yields no button",
			data: map[string]any{"portal": "candidate", "link": "https://evil.example.com/phish"},
			want: "",
		},
		{
			name: "a missing link yields no button",
			data: map[string]any{"portal": "candidate"},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := worker.actionURL(tc.data); got != tc.want {
				t.Errorf("actionURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// A portal with no configured base produces no button rather than a link to
// nowhere: a mail with a dead link is worse than one that says to open the app.
func TestActionURLWithoutAConfiguredPortal(t *testing.T) {
	t.Parallel()

	worker := &Email{portals: PortalURLs{}}
	if got := worker.actionURL(map[string]any{"portal": "candidate", "link": "/x"}); got != "" {
		t.Errorf("actionURL = %q, want empty", got)
	}
}
