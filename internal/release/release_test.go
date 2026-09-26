package release_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uhhhm/reverb/internal/release"
)

// A desktop-only or unpublished release must not prompt an iPhone to install
// an IPA that does not exist; only a newer, complete stable release qualifies.
func TestPublishedPhoneRelease(t *testing.T) {
	for _, tc := range []struct {
		body string
		want string
	}{
		{`{"tag_name":"v1.2.0","assets":[]}`, ""},
		{`{"tag_name":"v1.2.0","prerelease":true,"assets":[{"name":"Reverb.ipa"}]}`, ""},
		{`{"tag_name":"v1.2.0","assets":[{"name":"Reverb.ipa"}]}`, "v1.2.0"},
		{`{"tag_name":"v1.0.0","assets":[{"name":"Reverb.ipa"}]}`, ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) }))
		tracker := release.New("owner/repo", "v1.1.0")
		tracker.Client = &http.Client{Transport: rewrite{url: srv.URL}}
		status := tracker.Status(context.Background())
		srv.Close()
		if status.LatestVersion != tc.want {
			t.Fatalf("%s: %+v", tc.body, status)
		}
	}
}

type rewrite struct{ url string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	other, _ := http.NewRequestWithContext(req.Context(), req.Method, r.url, nil)
	return http.DefaultTransport.RoundTrip(other)
}
