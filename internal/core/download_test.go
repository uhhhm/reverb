package core

import "testing"

func TestDownloadStatusConstants(t *testing.T) {
	if DownloadQueued != "queued" || DownloadRunning != "running" || DownloadCompleted != "completed" ||
		DownloadFailed != "failed" || DownloadCanceled != "canceled" {
		t.Fatal("download status constant drift")
	}
}
