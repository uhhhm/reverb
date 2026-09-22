import AVFoundation
import Foundation
import MediaPlayer
import ReverbAPI
import UIKit

typealias QueueState = Components.Schemas.QueueState
typealias PlayerTrack = Components.Schemas.PlayerTrack
typealias LibraryTrack = Components.Schemas.LibraryTrack

/// A thin player over the core's queue (ADR 0003). The core decides what is
/// current and what comes next; this plays it through AVPlayer, reports ends
/// and skips back, and owns what only iOS can do: the audio session, the lock
/// screen, Control Center and headphone controls.
@MainActor
final class Player: ObservableObject {
    @Published private(set) var queue: QueueState?
    @Published private(set) var isPlaying = false
    @Published private(set) var elapsed: Double = 0
    @Published private(set) var duration: Double = 0
    @Published var lastError: String?

    /// The phone's own queue; every player names its session.
    private let session = "phone"
    private let core: CoreHost
    private let avPlayer = AVPlayer()
    private var loadedPlayID: Int64?
    /// The core port the current item streams from.
    private var loadedPort: Int?
    private var endObserver: NSObjectProtocol?
    private var timeObserver: Any?
    private var statusObservation: NSKeyValueObservation?
    /// Whether the listener wants sound: set by play and resume, cleared by
    /// pause. An interruption pauses without clearing it, so playback resumes
    /// after a call only when it was playing before.
    private var wantsToPlay = false
    private var tracker = PlayTracker()
    private var artworkTask: Task<Void, Never>?

    init(core: CoreHost) {
        self.core = core
        avPlayer.automaticallyWaitsToMinimizeStalling = false
        configureAudioSession()
        observePlayback()
        configureRemoteCommands()
    }

    var currentEntry: Components.Schemas.QueueEntry? {
        guard let queue, queue.index >= 0, queue.index < queue.entries.count else { return nil }
        return queue.entries[queue.index]
    }

    var current: PlayerTrack? { currentEntry?.track }

    // MARK: Listener actions

    /// Replaces the queue with tracks and starts the one at start.
    func play(_ tracks: [LibraryTrack], startAt start: Int) async {
        let body = Components.Schemas.PlayerTracksRequest(tracks: tracks.map(Self.playerTrack), start: start)
        await change { client, session in
            try await client.playTracks(path: .init(session: session), body: .json(body)).ok.body.json
        }
    }

    func togglePlayPause() {
        isPlaying ? pause() : resume()
    }

    func pause() {
        wantsToPlay = false
        avPlayer.pause()
    }

    func resume() {
        guard avPlayer.currentItem != nil else { return }
        // A core started again while the app was away listens on a new port,
        // so the loaded stream would no longer answer.
        if let track = current, let port = core.core?.port, port != loadedPort {
            load(track, at: elapsed)
            return
        }
        wantsToPlay = true
        activateSession()
        avPlayer.play()
    }

    func next() async {
        finishListening()
        let entry = entryRequest()
        await change { client, session in
            try await client.nextInQueue(path: .init(session: session), body: .json(entry)).ok.body.json
        }
    }

    /// Restarts a track well under way, as players do; otherwise goes back.
    func previous() async {
        if elapsed > 3 {
            seek(to: 0)
            return
        }
        finishListening()
        let entry = entryRequest()
        await change { client, session in
            try await client.previousInQueue(path: .init(session: session), body: .json(entry)).ok.body.json
        }
    }

    func seek(to seconds: Double) {
        tracker.seeked(to: seconds)
        avPlayer.seek(to: CMTime(seconds: seconds, preferredTimescale: 600))
        elapsed = seconds
        updateNowPlaying()
    }

    // MARK: Core queue

    private func entryRequest() -> Components.Schemas.PlayerEntryRequest {
        .init(entryId: currentEntry?.id)
    }

    /// Sends one queue action and applies the core's answer.
    private func change(_ action: (Client, String) async throws -> QueueState) async {
        guard let client = core.client else { return }
        do {
            apply(try await action(client, session))
        } catch {
            lastError = "Playback: \(error.localizedDescription)"
        }
    }

    /// Plays whatever the core says is current, loading it only when the
    /// core's playId says it starts over.
    private func apply(_ state: QueueState) {
        queue = state
        guard let entry = currentEntry, !state.finished else {
            wantsToPlay = false
            avPlayer.pause()
            if state.finished { seek(to: 0) }
            updateNowPlaying()
            return
        }
        if loadedPlayID != state.playId {
            loadedPlayID = state.playId
            load(entry.track)
        }
    }

    private func load(_ track: PlayerTrack, at seconds: Double = 0) {
        guard let id = track.id, let local = core.core else { return }
        let url = local.streamURL(trackID: id)
        loadedPort = local.port
        let item = AVPlayerItem(url: url)
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        endObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.didPlayToEndTimeNotification, object: item, queue: .main
        ) { [weak self] _ in
            Task { @MainActor in await self?.ended() }
        }
        avPlayer.replaceCurrentItem(with: item)
        elapsed = seconds
        if seconds > 0 {
            avPlayer.seek(to: CMTime(seconds: seconds, preferredTimescale: 600))
        }
        duration = Double(track.durationMs ?? 0) / 1000
        if seconds == 0 {
            tracker.start(track)
        } else {
            tracker.seeked(to: seconds)
        }
        wantsToPlay = true
        activateSession()
        avPlayer.play()
        updateNowPlaying()
        loadArtwork(for: track)
    }

    private func ended() async {
        finishListening(completed: true)
        let entry = entryRequest()
        await change { client, session in
            try await client.endedInQueue(path: .init(session: session), body: .json(entry)).ok.body.json
        }
    }

    // MARK: Plays

    /// Reports the track just left as a play when it was listened to enough,
    /// as the desktop does, so the phone's listening shapes the taste profile.
    private func finishListening(completed: Bool = false) {
        guard let play = tracker.finish(durationMs: Int(duration * 1000), completed: completed),
              let client = core.client else { return }
        Task {
            _ = try? await client.recordPlay(body: .json(play))
        }
    }

    // MARK: Audio session

    private func configureAudioSession() {
        let session = AVAudioSession.sharedInstance()
        try? session.setCategory(.playback, mode: .default)
        let center = NotificationCenter.default
        center.addObserver(forName: AVAudioSession.interruptionNotification, object: session, queue: .main) { [weak self] note in
            Task { @MainActor in self?.handleInterruption(note) }
        }
        center.addObserver(forName: AVAudioSession.routeChangeNotification, object: session, queue: .main) { [weak self] note in
            Task { @MainActor in self?.handleRouteChange(note) }
        }
    }

    private func activateSession() {
        try? AVAudioSession.sharedInstance().setActive(true)
    }

    /// A call pauses playback; it resumes afterwards when iOS says it should.
    private func handleInterruption(_ note: Notification) {
        guard let raw = note.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt,
              let type = AVAudioSession.InterruptionType(rawValue: raw) else { return }
        switch type {
        case .began:
            avPlayer.pause()
        case .ended:
            let options = (note.userInfo?[AVAudioSessionInterruptionOptionKey] as? UInt).map(AVAudioSession.InterruptionOptions.init) ?? []
            if wantsToPlay && options.contains(.shouldResume) {
                resume()
            }
        @unknown default:
            break
        }
    }

    /// Unplugging headphones pauses rather than playing out of the speaker.
    private func handleRouteChange(_ note: Notification) {
        guard let raw = note.userInfo?[AVAudioSessionRouteChangeReasonKey] as? UInt,
              AVAudioSession.RouteChangeReason(rawValue: raw) == .oldDeviceUnavailable else { return }
        pause()
    }

    private func observePlayback() {
        statusObservation = avPlayer.observe(\.timeControlStatus, options: [.new]) { [weak self] player, _ in
            let playing = player.timeControlStatus != .paused
            Task { @MainActor in
                guard let self, self.isPlaying != playing else { return }
                self.isPlaying = playing
                self.updateNowPlaying()
            }
        }
        timeObserver = avPlayer.addPeriodicTimeObserver(
            forInterval: CMTime(seconds: 0.25, preferredTimescale: 600), queue: .main
        ) { [weak self] time in
            Task { @MainActor in self?.tick(time) }
        }
    }

    private func tick(_ time: CMTime) {
        let seconds = time.seconds.isFinite ? time.seconds : 0
        if let itemDuration = avPlayer.currentItem?.duration.seconds, itemDuration.isFinite, itemDuration > 0,
           abs(itemDuration - duration) > 0.5 {
            // The folder library does not know durations; the file does.
            duration = itemDuration
            updateNowPlaying()
        }
        if isPlaying {
            tracker.advance(to: seconds)
        }
        elapsed = seconds
    }

    // MARK: Lock screen, Control Center, headphones

    private func configureRemoteCommands() {
        let center = MPRemoteCommandCenter.shared()
        center.playCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.resume() }
            return .success
        }
        center.pauseCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.pause() }
            return .success
        }
        center.togglePlayPauseCommand.addTarget { [weak self] _ in
            Task { @MainActor in self?.togglePlayPause() }
            return .success
        }
        center.nextTrackCommand.addTarget { [weak self] _ in
            Task { @MainActor in await self?.next() }
            return .success
        }
        center.previousTrackCommand.addTarget { [weak self] _ in
            Task { @MainActor in await self?.previous() }
            return .success
        }
        center.changePlaybackPositionCommand.addTarget { [weak self] event in
            guard let position = (event as? MPChangePlaybackPositionCommandEvent)?.positionTime else { return .commandFailed }
            Task { @MainActor in self?.seek(to: position) }
            return .success
        }
        center.skipForwardCommand.isEnabled = false
        center.skipBackwardCommand.isEnabled = false
    }

    private var artwork: MPMediaItemArtwork?

    private func updateNowPlaying() {
        let info = MPNowPlayingInfoCenter.default()
        guard let track = current, queue?.finished != true else {
            info.nowPlayingInfo = nil
            return
        }
        var values: [String: Any] = [
            MPMediaItemPropertyTitle: track.title ?? "",
            MPMediaItemPropertyArtist: track.artist ?? "",
            MPMediaItemPropertyAlbumTitle: track.album ?? "",
            MPMediaItemPropertyPlaybackDuration: duration,
            MPNowPlayingInfoPropertyElapsedPlaybackTime: elapsed,
            MPNowPlayingInfoPropertyPlaybackRate: isPlaying ? 1.0 : 0.0,
        ]
        if let artwork { values[MPMediaItemPropertyArtwork] = artwork }
        info.nowPlayingInfo = values
    }

    private func loadArtwork(for track: PlayerTrack) {
        artwork = nil
        artworkTask?.cancel()
        guard let id = Self.coverArtID(track), let url = core.core?.coverURL(id: id) else { return }
        artworkTask = Task {
            guard let (data, _) = try? await URLSession.shared.data(from: url),
                  let image = UIImage(data: data), !Task.isCancelled else { return }
            artwork = MPMediaItemArtwork(boundsSize: image.size) { _ in image }
            updateNowPlaying()
        }
    }

    // MARK: Track conversion

    /// The core stores a player's track as sent and hands it back; the phone
    /// sends what it needs to play and show it.
    static func playerTrack(_ t: LibraryTrack) -> PlayerTrack {
        var extra: [String: (any Sendable)?] = ["coverArtId": t.coverArtId]
        if let isrc = t.isrc { extra["isrc"] = isrc }
        return PlayerTrack(
            id: t.id, title: t.title, artist: t.artist, album: t.album, durationMs: t.durationMs,
            additionalProperties: (try? OpenAPIObjectContainer(unvalidatedValue: extra)) ?? .init()
        )
    }

    static func coverArtID(_ t: PlayerTrack) -> String? {
        ((t.additionalProperties.value["coverArtId"] ?? nil) as? String).flatMap { $0.isEmpty ? nil : $0 }
    }
}
