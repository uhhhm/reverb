import Foundation
import ReverbAPI

/// Resolving a track outside the library to a playable source takes the
/// core's yt-dlp a few seconds, on the play path where the listener waits.
/// Prewarming resolves it ahead of time, as the desktop does
/// (web/src/lib/extstreamPrewarm.ts): the top few search results, and the next
/// tracks in the queue. A track is asked for once per launch.
@MainActor
final class ExternalPrewarm {
    static let shared = ExternalPrewarm()

    /// Only the first few results are worth the resolve each one costs.
    static let resultLimit = 4
    /// How far ahead of the current track the queue is prewarmed.
    static let queueLookahead = 2

    private var seen = Set<String>()

    func prewarm(_ track: PlayerTrack, core: LocalCore?) {
        guard let core, let external = Player.externalStream(track) else { return }
        let key = external.source + ":" + external.externalId
        guard seen.insert(key).inserted else { return }
        var request = core.request(core.externalPrewarmURL(
            source: external.source, externalId: external.externalId, artist: track.artist, title: track.title
        ))
        request.httpMethod = "POST"
        Task.detached(priority: .utility) {
            _ = try? await URLSession.shared.data(for: request)
        }
    }

    func prewarm(results tracks: [PlayerTrack], core: LocalCore?) {
        for track in tracks.filter({ Player.externalStream($0) != nil }).prefix(Self.resultLimit) {
            prewarm(track, core: core)
        }
    }
}
