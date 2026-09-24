import SwiftUI

struct NowPlayingBar: View {
    @EnvironmentObject private var player: Player
    @State private var scrubbing: Double?

    var body: some View {
        if let track = player.current, player.queue?.finished != true {
            VStack(spacing: 6) {
                HStack {
                    VStack(alignment: .leading) {
                        Text(track.title ?? "")
                            .font(.headline)
                            .lineLimit(1)
                            .accessibilityIdentifier("nowPlaying.title")
                        Text(track.artist ?? "")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    Spacer()
                    Button { Task { await player.previous() } } label: { Image(systemName: "backward.fill") }
                        .accessibilityLabel("Previous")
                    Button { player.togglePlayPause() } label: {
                        Image(systemName: player.wantsToPlay ? "pause.fill" : "play.fill").font(.title2)
                    }
                    .accessibilityLabel(player.wantsToPlay ? "Pause" : "Play")
                    .accessibilityIdentifier("nowPlaying.playPause")
                    .accessibilityValue(player.isPlaying ? "playing" : (player.wantsToPlay ? "buffering" : "paused"))
                    Button { Task { await player.next() } } label: { Image(systemName: "forward.fill") }
                        .accessibilityLabel("Next")
                }
                .buttonStyle(.plain)
                .imageScale(.large)
                if player.duration > 0 {
                    Slider(
                        value: Binding(get: { min(max(0, scrubbing ?? player.elapsed), player.duration) }, set: { scrubbing = $0 }),
                        in: 0...player.duration
                    ) { editing in
                        if !editing, let target = scrubbing {
                            player.seek(to: target)
                            scrubbing = nil
                        }
                    }
                    .accessibilityLabel("Position")
                    .accessibilityIdentifier("nowPlaying.position")
                    HStack {
                        Text(Self.timestamp(scrubbing ?? player.elapsed))
                            .accessibilityIdentifier("nowPlaying.elapsed")
                        Spacer()
                        Text(Self.timestamp(player.duration))
                            .accessibilityIdentifier("nowPlaying.duration")
                    }
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                }
                if let error = player.lastError {
                    Text(error)
                        .font(.caption)
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("nowPlaying.error")
                }
            }
            .padding(.horizontal)
            .padding(.vertical, 8)
            .background(.bar)
            .onChange(of: player.queue?.playId) { _, _ in scrubbing = nil }
        }
    }
    private static func timestamp(_ seconds: Double) -> String {
        let total = seconds.isFinite ? max(0, Int(seconds)) : 0
        return String(format: "%d:%02d", total / 60, total % 60)
    }
}
