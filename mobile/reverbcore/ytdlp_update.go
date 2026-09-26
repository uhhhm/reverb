package reverbcore

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/ytdlpupdate"
)

//go:embed ytdlp-public-key.txt
var ytdlpPublicKey string

const ytdlpFeed = "https://github.com/uhhhm/reverb/releases/download/ytdlp-channel"

var phoneUpdates struct {
	sync.Mutex
	updater        *ytdlpupdate.Updater
	bundledVersion string
	lastCheck      time.Time
}

func updateKey() []byte {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(ytdlpPublicKey))
	return key
}

// YtDlpVersion reports the package actually loaded by the interpreter.
func YtDlpVersion() string {
	phoneUpdates.Lock()
	defer phoneUpdates.Unlock()
	if phoneUpdates.updater != nil {
		if v := phoneUpdates.updater.Version(); v != "" {
			return v
		}
	}
	return phoneUpdates.bundledVersion
}

// CheckYtDlpUpdate checks the release channel. Automatic foreground checks are
// daily; an explicit retry bypasses that throttle. No peer supplies executable
// packages or trust keys. The binding keeps this lifecycle operation off HTTP.
func CheckYtDlpUpdate(force bool) error {
	phoneUpdates.Lock()
	defer phoneUpdates.Unlock()
	if phoneUpdates.updater == nil {
		return errors.New("embedded yt-dlp updates are unavailable")
	}
	if !force && time.Since(phoneUpdates.lastCheck) < 24*time.Hour {
		return nil
	}
	phoneUpdates.lastCheck = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("yt-dlp update requires HTTPS")
		}
		if len(via) >= 10 {
			return errors.New("too many update redirects")
		}
		return nil
	}}
	return phoneUpdates.updater.Check(ctx, client, ytdlpFeed)
}
