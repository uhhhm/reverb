// reverb-ytdlp-sign creates the three assets consumed by the phone updater.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/uhhhm/reverb/internal/ytdlpupdate"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	keygen := flag.Bool("keygen", false, "create a private environment file and public key")
	output := flag.String("out", "dist/ytdlp", "asset output directory (keygen: private environment file)")
	public := flag.String("public-key", "mobile/reverbcore/ytdlp-public-key.txt", "verification key file")
	archive := flag.String("package", "", "pure-Python yt-dlp wheel")
	version := flag.String("version", "", "yt_dlp.version.__version__")
	sequence := flag.Int64("sequence", 0, "strictly increasing release sequence")
	flag.Parse()
	if *keygen {
		pub, key, err := ed25519.GenerateKey(nil)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(f, "YTDLP_SIGNING_KEY=%s\n", base64.StdEncoding.EncodeToString(key))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		return os.WriteFile(*public, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0644)
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("YTDLP_SIGNING_KEY"))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("YTDLP_SIGNING_KEY must be a base64 Ed25519 private key")
	}
	pubText, err := os.ReadFile(*public)
	if err != nil {
		return err
	}
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(pubText)))
	if err != nil || !ed25519.PublicKey(pub).Equal(ed25519.PrivateKey(key).Public()) {
		return fmt.Errorf("signing key does not match the app's public key")
	}
	if *version == "" || *sequence <= 0 {
		return fmt.Errorf("version and positive sequence are required")
	}
	b, err := os.ReadFile(*archive)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	m, err := json.Marshal(ytdlpupdate.Manifest{Format: 1, Sequence: *sequence, Version: *version, SHA256: hex.EncodeToString(sum[:])})
	if err != nil {
		return err
	}
	if err = os.MkdirAll(*output, 0700); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"package.zip": b, "manifest.json": m, "manifest.sig": ed25519.Sign(ed25519.PrivateKey(key), m)} {
		if err = os.WriteFile(filepath.Join(*output, name), data, 0644); err != nil {
			return err
		}
	}
	return nil
}
