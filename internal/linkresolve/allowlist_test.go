package linkresolve

import "testing"

func TestIsAllowedSourceURL(t *testing.T) {
	allowed := []string{
		"https://www.youtube.com/watch?v=abc",
		"https://music.youtube.com/watch?v=abc",
		"https://youtu.be/abc",
		"https://www.youtube-nocookie.com/watch?v=abc", // ParseYouTubeURL accepts it
		"https://open.spotify.com/track/abc",
		"https://soundcloud.com/artist/track",
		"https://artist.bandcamp.com/track/x",
		"youtube.com/watch?v=abc", // scheme-less input is read as https
		"HTTPS://WWW.YOUTUBE.COM/watch?v=abc",
	}
	for _, u := range allowed {
		if !IsAllowedSourceURL(u) {
			t.Errorf("IsAllowedSourceURL(%q) = false, want true", u)
		}
	}

	refused := []string{
		"",
		"   ",
		"file:///etc/passwd",
		"ftp://youtube.com/x",
		"gopher://youtube.com/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8090/api/v1/settings",
		"http://localhost/admin",
		"http://[::1]:8090/",
		"http://192.168.1.10/",
		"https://evil.com/",
		"https://youtube.com.evil.com/", // suffix must be a label boundary
		"https://notyoutube.com/",
		"https://spotify.com.evil.com/",
		"https://evil.com/?u=youtube.com", // allowlisted host only in the query
	}
	for _, u := range refused {
		if IsAllowedSourceURL(u) {
			t.Errorf("IsAllowedSourceURL(%q) = true, want false", u)
		}
	}
}
