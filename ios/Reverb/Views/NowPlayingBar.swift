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
                        Image(systemName: player.isPlaying ? "pause.fill" : "play.fill").font(.title2)
                    }
                    .accessibilityLabel(player.isPlaying ? "Pause" : "Play")
                    .accessibilityIdentifier("nowPlaying.playPause")
                    .accessibilityValue(player.isPlaying ? "playing" : "paused")
                    Button { Task { await player.next() } } label: { Image(systemName: "forward.fill") }
                        .accessibilityLabel("Next")
                }
                .buttonStyle(.plain)
                .imageScale(.large)
                if player.duration > 0 {
                    Slider(
                        value: Binding(get: { scrubbing ?? player.elapsed }, set: { scrubbing = $0 }),
                        in: 0...player.duration
                    ) { editing in
                        if !editing, let target = scrubbing {
                            player.seek(to: target)
                            scrubbing = nil
                        }
                    }
                    .accessibilityLabel("Position")
                }
            }
            .padding(.horizontal)
            .padding(.vertical, 8)
            .background(.bar)
        }
    }
}
