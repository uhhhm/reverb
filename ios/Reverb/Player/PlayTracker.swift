import Foundation
import ReverbAPI

/// Decides when a track counts as a play, by the desktop's rules
/// (web/src/lib/playTracker.ts): a track over 30 seconds long counts once half
/// of it, or four minutes, has actually been listened to. Only forward play
/// accrues; a seek counts neither the span skipped nor a span heard twice.
struct PlayTracker {
    private static let minimumDurationMs = 30_000
    private static let thresholdMs = 240_000
    private static let maxStepMs = 5_000

    private var track: PlayerTrack?
    private var lastSeconds: Double = 0
    private var playedMs = 0

    mutating func start(_ track: PlayerTrack) {
        self.track = track
        lastSeconds = 0
        playedMs = 0
    }

    mutating func advance(to seconds: Double) {
        let deltaMs = Int((seconds - lastSeconds) * 1000)
        if deltaMs > 0 && deltaMs < Self.maxStepMs {
            playedMs += deltaMs
        }
        lastSeconds = seconds
    }

    mutating func seeked(to seconds: Double) {
        lastSeconds = seconds
    }

    /// The play to record for the track being left, if it qualified; the
    /// tracker then waits for the next track.
    mutating func finish(durationMs: Int, completed: Bool) -> Components.Schemas.PlayRequest? {
        defer { track = nil }
        guard let track, durationMs > Self.minimumDurationMs,
              playedMs >= min(durationMs / 2, Self.thresholdMs) else { return nil }
        return Components.Schemas.PlayRequest(
            libraryTrackId: track.id,
            title: track.title,
            artist: track.artist,
            album: track.album,
            durationMs: durationMs,
            msPlayed: playedMs,
            completed: completed
        )
    }
}
