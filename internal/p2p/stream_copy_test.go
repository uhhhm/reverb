package p2p

import (
	"bytes"
	"io"
	"testing"
	"time"
)

type deadlineRecorder struct {
	set []time.Time
}

func (d *deadlineRecorder) SetDeadline(t time.Time) error {
	d.set = append(d.set, t)
	return nil
}

// trickle hands back one byte per Read, standing in for a slow link.
type trickle struct{ left int }

func (r *trickle) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	r.left--
	p[0] = 'x'
	return 1, nil
}

// A transfer must be bounded by silence, not by total elapsed time: one fixed
// deadline cannot cover both a small MP3 and the multi-gigabyte file the
// protocol allows, and a cut-off copy discards the partial write and is
// retried identically every round.
func TestCopyStreamIdleRefreshesTheDeadlinePerChunk(t *testing.T) {
	rec := &deadlineRecorder{}
	var dst bytes.Buffer
	n, err := copyStreamIdle(&dst, &trickle{left: 4}, rec)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 || dst.String() != "xxxx" {
		t.Fatalf("copied %d bytes (%q), want 4", n, dst.String())
	}
	// One before each read plus one before each write, so a stalled chunk is
	// what expires the deadline rather than the size of the file.
	if len(rec.set) < 8 {
		t.Fatalf("set the deadline %d time(s), want it re-armed around every chunk", len(rec.set))
	}
	for i := 1; i < len(rec.set); i++ {
		if rec.set[i].Before(rec.set[i-1]) {
			t.Fatal("deadline moved backwards")
		}
	}
}
