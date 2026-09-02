package p2p

import (
	"io"
	"time"
)

// idleTransferTimeout bounds a file transfer by silence rather than by total
// elapsed time. One fixed deadline cannot cover both a 3 MiB MP3 and the 8 GiB
// the protocol allows: a large file over a slow link would be cut off
// mid-copy, the partial write discarded, and the same file retried identically
// every round.
const idleTransferTimeout = 60 * time.Second

// deadlineSetter is the part of network.Stream a transfer needs.
type deadlineSetter interface {
	SetDeadline(time.Time) error
}

// copyStreamIdle copies src to dst, refreshing the stream deadline around
// every chunk so the transfer fails only when it actually stalls.
func copyStreamIdle(dst io.Writer, src io.Reader, s deadlineSetter) (int64, error) {
	buf := make([]byte, 256<<10)
	var total int64
	for {
		_ = s.SetDeadline(time.Now().Add(idleTransferTimeout))
		n, rerr := src.Read(buf)
		if n > 0 {
			_ = s.SetDeadline(time.Now().Add(idleTransferTimeout))
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}
