import SwiftUI

/// The mini player floating above the tab bar. Tapping it opens Now Playing.
struct NowPlayingBar: View {
    @EnvironmentObject private var player: Player
    @State private var expanded = false

    var body: some View {
        Group {
            if let track = player.current, player.queue?.finished != true {
                bar(track)
            }
        }
        .fullScreenCover(isPresented: $expanded) { NowPlayingView() }
    }

    private func bar(_ track: PlayerTrack) -> some View {
        HStack(spacing: 10) {
            HStack(spacing: 10) {
                Group {
                    if let image = player.artworkImage {
                        Image(uiImage: image).resizable().scaledToFill()
                    } else {
                        Artwork(source: .init(seed: track.album ?? track.title ?? ""))
                    }
                }
                .frame(width: 40, height: 40)
                .clipShape(RoundedRectangle(cornerRadius: 4))
                VStack(alignment: .leading, spacing: 2) {
                    Text(track.title ?? "")
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(.white)
                        .lineLimit(1)
                        .accessibilityIdentifier("nowPlaying.title")
                    Text(player.lastError ?? track.artist ?? "")
                        .font(.caption)
                        .foregroundStyle(player.lastError == nil ? Theme.secondaryText : .red)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
            }
            .contentShape(Rectangle())
            .onTapGesture { expanded = true }
            .accessibilityElement(children: .contain)
            .accessibilityAction(named: "Open Now Playing") { expanded = true }

            Button { player.togglePlayPause() } label: {
                Image(systemName: player.wantsToPlay ? "pause.fill" : "play.fill")
                    .font(.title2)
                    .foregroundStyle(.white)
                    .frame(width: 40, height: 40)
            }
            .buttonStyle(PressableStyle())
            .accessibilityLabel(player.wantsToPlay ? "Pause" : "Play")
            .accessibilityIdentifier("nowPlaying.playPause")
            .accessibilityValue(player.playbackValue)
        }
        .padding(.leading, 8)
        .padding(.trailing, 6)
        .padding(.vertical, 8)
        .background(alignment: .bottom) {
            ZStack(alignment: .bottom) {
                (player.artworkImage?.averageColor ?? Theme.elevated)
                ProgressLine(fraction: player.duration > 0 ? player.elapsed / player.duration : 0)
                    .padding(.horizontal, 8)
            }
            .clipShape(RoundedRectangle(cornerRadius: 8))
        }
        .padding(.horizontal, 8)
        .padding(.bottom, 4)
        .animation(.easeInOut(duration: 0.4), value: player.artworkImage)
    }
}

/// The thin played-so-far line under the mini player.
private struct ProgressLine: View {
    let fraction: Double

    var body: some View {
        GeometryReader { proxy in
            ZStack(alignment: .leading) {
                Capsule().fill(.white.opacity(0.25))
                Capsule().fill(.white).frame(width: proxy.size.width * min(max(fraction, 0), 1))
            }
        }
        .frame(height: 2)
        .accessibilityHidden(true)
    }
}

/// Now Playing: the full-screen player.
struct NowPlayingView: View {
    @EnvironmentObject private var player: Player
    @Environment(\.dismiss) private var dismiss
    @State private var scrubbing: Double?
    @State private var dragOffset: CGFloat = 0

    var body: some View {
        let track = player.current
        VStack(spacing: 0) {
            header(track)
            Spacer(minLength: 16)
            artwork(track)
            Spacer(minLength: 24)
            VStack(alignment: .leading, spacing: 4) {
                Text(track?.title ?? "")
                    .font(.title2.weight(.bold))
                    .foregroundStyle(.white)
                    .lineLimit(1)
                    .accessibilityIdentifier("fullPlayer.title")
                Text(track?.artist ?? "")
                    .font(.body)
                    .foregroundStyle(Theme.secondaryText)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            scrubber
                .padding(.top, 18)
            controls
                .padding(.top, 8)
            if let error = player.lastError {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .multilineTextAlignment(.center)
                    .padding(.top, 8)
                    .accessibilityIdentifier("nowPlaying.error")
            }
            Spacer(minLength: 24)
        }
        .padding(.horizontal, 24)
        .background {
            LinearGradient(colors: [player.artworkImage?.averageColor ?? Theme.highlight, Theme.background],
                           startPoint: .top, endPoint: .bottom)
                .ignoresSafeArea()
                .animation(.easeInOut(duration: 0.5), value: player.artworkImage)
        }
        .offset(y: dragOffset)
        .gesture(
            DragGesture()
                .onChanged { dragOffset = max(0, $0.translation.height) }
                .onEnded { value in
                    if value.translation.height > 140 { dismiss() }
                    withAnimation(.spring) { dragOffset = 0 }
                }
        )
        .preferredColorScheme(.dark)
        .onChange(of: player.queue?.playId) { _, _ in scrubbing = nil }
        .onChange(of: track == nil || player.queue?.finished == true) { _, gone in
            if gone { dismiss() }
        }
    }

    private func header(_ track: PlayerTrack?) -> some View {
        HStack {
            Button { dismiss() } label: {
                Image(systemName: "chevron.down").font(.title3.weight(.semibold))
            }
            .accessibilityLabel("Close")
            .accessibilityIdentifier("fullPlayer.close")
            Spacer()
            VStack(spacing: 2) {
                Text(player.queue?.radio == true ? "PLAYING RADIO" : "PLAYING FROM")
                    .font(.caption2.weight(.medium))
                    .tracking(1)
                    .foregroundStyle(Theme.secondaryText)
                Text(player.queue?.radio == true ? (track?.artist ?? "Radio") : (track?.album ?? "Your queue"))
                    .font(.caption.weight(.bold))
                    .lineLimit(1)
            }
            Spacer()
            Menu {
                if let track {
                    Button("Start Radio", systemImage: "dot.radiowaves.left.and.right") {
                        Task { await player.startRadio(tracks: [track]) }
                    }
                }
            } label: {
                Image(systemName: "ellipsis").font(.title3.weight(.semibold)).frame(width: 30, height: 30)
            }
            .accessibilityLabel("More")
        }
        .foregroundStyle(.white)
        .padding(.top, 8)
    }

    private func artwork(_ track: PlayerTrack?) -> some View {
        Group {
            if let image = player.artworkImage {
                Image(uiImage: image).resizable().scaledToFill()
            } else {
                Artwork(source: .init(seed: track?.album ?? track?.title ?? ""), cornerRadius: 8)
            }
        }
        .aspectRatio(1, contentMode: .fit)
        .clipShape(RoundedRectangle(cornerRadius: 8))
        .shadow(color: .black.opacity(0.45), radius: 24, y: 12)
        .scaleEffect(player.wantsToPlay ? 1 : 0.9)
        .animation(.spring(response: 0.45, dampingFraction: 0.75), value: player.wantsToPlay)
    }

    private var scrubber: some View {
        VStack(spacing: 2) {
            Slider(
                value: Binding(get: { min(max(0, scrubbing ?? player.elapsed), max(player.duration, 0.01)) }, set: { scrubbing = $0 }),
                in: 0...max(player.duration, 0.01)
            ) { editing in
                if !editing, let target = scrubbing {
                    player.seek(to: target)
                    scrubbing = nil
                }
            }
            .tint(.white)
            .disabled(player.duration <= 0)
            .accessibilityLabel("Position")
            .accessibilityIdentifier("nowPlaying.position")
            HStack {
                Text(formatTimestamp(scrubbing ?? player.elapsed))
                    .accessibilityIdentifier("nowPlaying.elapsed")
                Spacer()
                Text(formatTimestamp(player.duration))
                    .accessibilityIdentifier("nowPlaying.duration")
            }
            .font(.caption2.monospacedDigit())
            .foregroundStyle(Theme.secondaryText)
        }
    }

    private var controls: some View {
        HStack {
            Button { Task { await player.setShuffle(player.queue?.shuffle != true) } } label: {
                Image(systemName: "shuffle")
                    .font(.title3)
                    .foregroundStyle(player.queue?.shuffle == true ? Theme.green : .white)
                    .overlay(alignment: .bottom) { activeDot(player.queue?.shuffle == true) }
            }
            .accessibilityLabel("Shuffle")
            .accessibilityValue(player.queue?.shuffle == true ? "on" : "off")
            .accessibilityIdentifier("fullPlayer.shuffle")
            Spacer()
            Button { Task { await player.previous() } } label: {
                Image(systemName: "backward.end.fill").font(.system(size: 30))
            }
            .accessibilityLabel("Previous")
            Spacer()
            Button { player.togglePlayPause() } label: {
                Image(systemName: player.wantsToPlay ? "pause.fill" : "play.fill")
                    .font(.system(size: 28, weight: .bold))
                    .foregroundStyle(.black)
                    .offset(x: player.wantsToPlay ? 0 : 2)
                    .frame(width: 66, height: 66)
                    .background(.white, in: Circle())
            }
            .accessibilityLabel(player.wantsToPlay ? "Pause" : "Play")
            .accessibilityIdentifier("fullPlayer.playPause")
            .accessibilityValue(player.playbackValue)
            Spacer()
            Button { Task { await player.next() } } label: {
                Image(systemName: "forward.end.fill").font(.system(size: 30))
            }
            .accessibilityLabel("Next")
            Spacer()
            Button { Task { await player.cycleRepeat() } } label: {
                Image(systemName: player.queue?._repeat == .one ? "repeat.1" : "repeat")
                    .font(.title3)
                    .foregroundStyle(repeatOn ? Theme.green : .white)
                    .overlay(alignment: .bottom) { activeDot(repeatOn) }
            }
            .accessibilityLabel("Repeat")
            .accessibilityValue(player.queue?._repeat.rawValue ?? "off")
        }
        .foregroundStyle(.white)
        .buttonStyle(PressableStyle())
    }

    private var repeatOn: Bool {
        guard let mode = player.queue?._repeat else { return false }
        return mode != .off
    }

    private func activeDot(_ on: Bool) -> some View {
        Circle().fill(Theme.green).frame(width: 4, height: 4).offset(y: 10).opacity(on ? 1 : 0)
    }
}

extension Player {
    /// What the play button reports to accessibility and the UI tests.
    var playbackValue: String { isPlaying ? "playing" : (wantsToPlay ? "buffering" : "paused") }
}
