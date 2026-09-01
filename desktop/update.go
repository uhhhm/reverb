package main

import (
	"context"
	"log"
	"os"

	"github.com/uhhhm/reverb/desktop/updater"
)

// updateAdapter presents the desktop updater through the api.UpdateService
// interface. The API package deliberately knows nothing about the desktop tree,
// so the concrete state crosses the boundary as JSON.
type updateAdapter struct{ svc *updater.Service }

func (u updateAdapter) Status() any               { return u.svc.Status() }
func (u updateAdapter) Check(ctx context.Context) { u.svc.CheckNow(ctx) }
func (u updateAdapter) Install() error            { return u.svc.InstallAndRestart() }
func (u updateAdapter) Dismiss()                  { u.svc.Dismiss() }

// newUpdater builds the self-update service for this instance. quit is called
// once the successor process has been spawned, so this one shuts down cleanly
// instead of racing the new window for the database and the bundled Navidrome
// port.
func newUpdater(repo, dataDir string, bus updater.Publisher, quit func()) *updater.Service {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("updater: cannot locate the running binary, updates disabled: %v", err)
		return nil
	}
	return updater.New(updater.Options{
		Repo:           repo,
		CurrentVersion: version,
		DataDir:        dataDir,
		ExePath:        exe,
		Bus:            bus,
		Quit:           quit,
	})
}
