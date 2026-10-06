package release_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uhhhm/reverb/internal/release"
)

// A desktop-only or unpublished release must not prompt an iPhone to install
// an IPA that does not exist; only a newer, complete stable release qualifies,
// and its IPA is the one SideStore is asked to install.
func TestPublishedPhoneRelease(t *testing.T) {
	const ipa = `{"name":"Reverb.ipa","browser_download_url":"https://github.com/owner/repo/releases/download/v1.2.0/Reverb.ipa"}`
	for _, tc := range []struct {
		body    string
		want    string
		wantIPA string
	}{
		{`{"tag_name":"v1.2.0","assets":[]}`, "", ""},
		{`{"tag_name":"v1.2.0","prerelease":true,"assets":[` + ipa + `]}`, "", ""},
		{`{"tag_name":"v1.2.0","assets":[{"name":"Reverb-desktop.zip","browser_download_url":"https://example.com/d.zip"},` + ipa + `]}`, "v1.2.0", "https://github.com/owner/repo/releases/download/v1.2.0/Reverb.ipa"},
		{`{"tag_name":"v1.2.0","assets":[{"name":"Reverb.ipa","browser_download_url":"http://evil.example/Reverb.ipa"}]}`, "", ""},
		{`{"tag_name":"v1.0.0","assets":[` + ipa + `]}`, "", ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
		tracker := release.New("owner/repo", "v1.1.0")
		tracker.Client = &http.Client{Transport: rewrite{url: srv.URL}}
		status := tracker.Status(context.Background())
		srv.Close()
		if status.LatestVersion != tc.want || status.IPAURL != tc.wantIPA {
			t.Fatalf("%s: %+v", tc.body, status)
		}
	}
}

type rewrite struct{ url string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	other, _ := http.NewRequestWithContext(req.Context(), req.Method, r.url, nil)
	return http.DefaultTransport.RoundTrip(other)
}
