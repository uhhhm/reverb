// Package childproc is the seam between Reverb and the operating system's
// process model. Every child Reverb starts — the bundled Navidrome, ffmpeg,
// spotDL, yt-dlp, the background runtime, the successor after an update — goes
// through it, so the per-OS mechanics live in one place instead of being spread
// across the packages that happen to spawn something.
//
// Most of what follows is implemented per OS; [Command] and [CommandContext] are
// portable shells over the per-OS configuration. Each entry documents the
// behaviour a port must reproduce, because the callers depend on the guarantee,
// not on the mechanism:
//
//   - [Command] and [CommandContext] must return a child that leaves no console
//     window on screen. Reverb is a GUI app; a child that flashes a terminal is
//     a user-visible defect, not a cosmetic one. They must not alter the child's
//     stdio: callers attach pipes to parse progress output.
//   - [GracefulCommandContext] has the same no-visible-window and stdio contract,
//     but the child must remain reachable by [Terminate]. Use it for a long-lived
//     managed child whose on-disk state needs an orderly shutdown, not one-shot
//     download tools.
//   - [Detach] must let the child outlive this process and survive whatever the
//     OS does when the parent's session, terminal or window goes away. The
//     background runtime and the post-update successor both depend on it: the
//     parent quits moments after starting them.
//   - [LowPriorityCommand] must reduce the scheduling priority of the child and
//     everything it goes on to spawn, and the reduction must be in force before
//     the child creates its own threads. Background sync competes with whatever
//     the household is actually doing; it is expected to lose.
//   - [Terminate] must request an orderly shutdown, giving the child a chance to
//     flush and close. Navidrome corrupts its index if it is cut off mid-write,
//     so this is the only sanctioned first move. It must not block.
//   - [Kill] ends the process immediately, with no chance to clean up. It is the
//     escalation after [Terminate] has been given a grace period.
//   - [Alive] must report whether the pid names a live process, without
//     disturbing it, and must be false for a pid that has exited.
//   - [IsNamed] must report whether the pid is currently running the named
//     program. Operating systems reuse pids, and Reverb reads pids out of files
//     written by earlier runs; without this check a stale file could aim
//     [Terminate] at an unrelated process the household cares about. A port that
//     cannot answer the question — or that, like Linux, only sees a truncated
//     name — must return false rather than guess: refusing to signal is always
//     the safe answer.
//   - [ShutdownSignals] must name every asynchronous notification the host uses
//     to mean "exit", so a headless process can unwind instead of dying where it
//     stands. This is the seam least likely to survive a port intact: an OS with
//     no signals at all has to deliver the same meaning by another route, and
//     the runtime's shutdown path — closing the database, releasing the lock,
//     stopping the child — is what must end up running either way.
//
// Processes are addressed by pid throughout, because that is what Reverb has
// after reading a pid file written by a previous run. Callers holding an
// *os.Process pass its Pid.
package childproc

import (
	"context"
	"os/exec"
	"time"
)

// Command and CommandContext replace exec.Command and exec.CommandContext
// everywhere Reverb starts a child. They are constructors rather than a
// "configure this cmd" helper on purpose: a spawn site that forgot to call such
// a helper would work fine on the developer's machine and regress on the one
// platform the seam exists for, and nothing would catch it. Going through here
// is what makes the guarantee structural.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	hide(cmd)
	return cmd
}

func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hide(cmd)
	return cmd
}

// GracefulCommandContext constructs a hidden child that Terminate can ask to
// shut down cleanly. This differs from CommandContext only on platforms where
// fully suppressing a console also removes the orderly control channel.
func GracefulCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hideGracefully(cmd)
	return cmd
}

// InstanceIdentity returns an OS-backed identity for the process instance, or
// an empty identity on platforms that do not provide one here. Unlike a pid or
// executable name, a non-empty identity changes when the OS reuses a pid.
func InstanceIdentity(pid int) (string, error) { return instanceIdentity(pid) }

// StopInstance gracefully stops the process represented by expected. On
// platforms that provide a non-empty InstanceIdentity, it keeps a stable
// process reference open through validation, the grace period, and hard-kill
// escalation so PID reuse cannot redirect either signal. Platforms without an
// instance identity retain their existing pid-based behavior. matched is false
// when the recorded process instance no longer exists.
func StopInstance(pid int, expected string, grace time.Duration) (matched bool, err error) {
	return stopInstance(pid, expected, grace)
}
