package app

import (
	"context"
	"encoding/json"
	"log"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/store/db"
)

// SeedBundledDownloader ensures the bundled spotDL downloader is present as a
// configured instance, so downloads work with zero manual setup (the image ships
// spotDL + ffmpeg). It runs only when NO downloader instance exists yet, so a
// user who has configured their own downloader is respected. output_dir comes
// from REVERB_DOWNLOAD_DIR (the Docker image sets it to /music), defaulting to
// ./downloads for bare runs. Best-effort: any error is logged, never fatal.
func SeedBundledDownloader(ctx context.Context, q *db.Queries, getenv func(string) string) {
	instances, err := q.ListAdapterInstances(ctx)
	if err != nil {
		return
	}
	for _, inst := range instances {
		if inst.Type == "downloader" {
			return // already have a downloader — nothing to seed
		}
	}

	dir := getenv("REVERB_DOWNLOAD_DIR")
	if dir == "" {
		dir = "./downloads"
	}
	cfg, _ := json.Marshal(map[string]any{"output_dir": dir})

	if err := q.CreateAdapterInstance(ctx, db.CreateAdapterInstanceParams{
		ID:         uuid.NewString(),
		Type:       "downloader",
		Name:       "spotdl",
		Enabled:    1,
		Priority:   0,
		ConfigJson: string(cfg),
	}); err != nil {
		log.Printf("could not seed bundled spotdl downloader: %v", err)
		return
	}
	log.Printf("seeded bundled spotdl downloader (output_dir=%s)", dir)
}

// SeedPhoneSearchSources gives a fresh phone a keyless search source. Existing
// rows, including sources the owner deliberately disabled, are left alone.
// Spotify is available when credentials have been provisioned for this phone.
func SeedPhoneSearchSources(ctx context.Context, q *db.Queries, getenv func(string) string) {
	instances, err := q.ListAdapterInstances(ctx)
	if err != nil {
		log.Printf("could not list phone search sources: %v", err)
		return
	}
	seen := make(map[string]bool)
	for _, inst := range instances {
		if inst.Type == "search" {
			seen[inst.Name] = true
		}
	}
	seed := func(name string, cfg map[string]string, priority int64) {
		if seen[name] {
			return
		}
		data, err := json.Marshal(cfg)
		if err != nil {
			return
		}
		if err := q.CreateAdapterInstance(ctx, db.CreateAdapterInstanceParams{
			ID: uuid.NewString(), Type: "search", Name: name, Enabled: 1,
			Priority: priority, ConfigJson: string(data),
		}); err != nil {
			log.Printf("could not seed phone %s search: %v", name, err)
		}
	}
	seed("deezer", map[string]string{}, 0)
	if id := getenv("REVERB_SPOTIFY_CLIENT_ID"); id != "" && getenv("REVERB_SPOTIFY_CLIENT_SECRET") != "" {
		seed("spotify", map[string]string{"client_id": id}, 1)
	}
}
