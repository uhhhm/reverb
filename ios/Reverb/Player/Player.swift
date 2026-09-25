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
    private let avPlayer: AVPlayer
    private let streamURL: ((String) -> URL)?
    private let prewarm: (PlayerTrack, LocalCore?) -> Void
    private var loadedPlayID: Int64?
    /// The core the current item streams from.
    private var loadedCore: LocalCore?
    private var endObserver: NSObjectProtocol?
    private var timeObserver: Any?
    private var statusObservation: NSKeyValueObservation?
    private var durationObservation: NSKeyValueObservation?
    private var itemStatusObservation: NSKeyValueObservation?
    private var failureObserver: NSObjectProtocol?
    private var sessionObservers: [NSObjectProtocol] = []
    private var remoteTargets: [(MPRemoteCommand, Any)] = []
    private var seekVersion = 0
    private var seeking = false
    private var handledEnd = false
    private var cropStart: Double = 0
    private var cropEnd: Double = 0
    /// The play already reloaded once after a failure; a second failure skips it.
    private var reloadedPlayID: Int64?
    private weak var failedItem: AVPlayerItem?
    /// Tracks skipped in a row for failing, so a queue of nothing playable stops.
    private var consecutiveFailures = 0
    private static let maxConsecutiveFailures = 3
    /// Whether to resume when an interruption such as a call ends.
    private var resumeAfterInterruption = false
    /// Whether the listener wants sound: set by play and resume, cleared by
    /// pause, including the pause an interruption makes.
    @Published private(set) var wantsToPlay = false
    private var tracker = PlayTracker()
    private var artworkTask: Task<Void, Never>?

    init(
        core: CoreHost,
        avPlayer: AVPlayer = AVPlayer(),
        streamURL: ((String) -> URL)? = nil,
        prewarm: ((PlayerTrack, LocalCore?) -> Void)? = nil
    ) {
        self.core = core
        self.avPlayer = avPlayer
        self.streamURL = streamURL
        self.prewarm = prewarm ?? { track, core in
            ExternalPrewarm.shared.prewarm(track, core: core)
        }
        avPlayer.automaticallyWaitsToMinimizeStalling = true
        configureAudioSession()
        observePlayback()
        configureRemoteCommands()
    }

    deinit {
        if let timeObserver { avPlayer.removeTimeObserver(timeObserver) }
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        if let failureObserver { NotificationCenter.default.removeObserver(failureObserver) }
        for observer in sessionObservers { NotificationCenter.default.removeObserver(observer) }
        for (command, target) in remoteTargets { command.removeTarget(target) }
    }

    var currentEntry: Components.Schemas.QueueEntry? {
        guard let queue, queue.index >= 0, queue.index < queue.entries.count else { return nil }
        return queue.entries[queue.index]
    }

    var current: PlayerTrack? { currentEntry?.track }

    // MARK: Listener actions

    /// Replaces the queue with tracks and starts the one at start.
    func play(_ tracks: [LibraryTrack], startAt start: Int) async {
        await play(tracks.map(Self.playerTrack), startAt: start)
    }

    /// Plays already-resolved queue tracks. Playlist and recommendation views
    /// use the stable catalog id here when playback is delegated to a peer.
    func play(_ tracks: [PlayerTrack], startAt start: Int) async {
        let body = Components.Schemas.PlayerTracksRequest(tracks: tracks, start: start)
        await change { client, session in
            try await client.playTracks(path: .init(session: session), body: .json(body)).ok.body.json
        }
    }

    func togglePlayPause() {
        wantsToPlay ? pause() : resume()
    }

    func pause() {
        wantsToPlay = false
        avPlayer.pause()
        isPlaying = false
        updateNowPlaying()
    }

    func resume() {
        guard let item = avPlayer.currentItem, queue?.finished != true else { return }
        resumeAfterInterruption = false
        // A core started again while the app was away listens on a new port
        // with a new secret, so the loaded stream would no longer answer.
        if let track = current, item.status == .failed || core.core != loadedCore {
            load(track, at: elapsed, continuing: true)
            return
        }
        wantsToPlay = true
        // The track ended but the core never heard: ask again for what is next.
        if handledEnd {
            let entryID = currentEntry?.id
            Task { await reportEnd(entryID: entryID) }
            return
        }
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
        guard seconds.isFinite, let item = avPlayer.currentItem else { return }
        let target = max(0, duration > 0 ? min(seconds, duration) : seconds)
        seekVersion += 1
        let version = seekVersion
        seeking = true
        handledEnd = false
        item.cancelPendingSeeks()
        tracker.seeked(to: target)
        elapsed = target
        updateNowPlaying()
        avPlayer.seek(to: CMTime(seconds: cropStart + target, preferredTimescale: 600), toleranceBefore: .zero, toleranceAfter: .zero) { [weak self, weak item] finished in
            Task { @MainActor in
                guard let self, let item, self.avPlayer.currentItem === item,
                      self.seekVersion == version else { return }
                self.seeking = false
                let actual = self.avPlayer.currentTime().seconds
                if actual.isFinite {
                    self.elapsed = max(0, actual - self.cropStart)
                    self.tracker.seeked(to: self.elapsed)
                }
                if !finished { self.lastError = "Playback: Could not seek to that position." }
                self.updateNowPlaying()
            }
        }
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
    func apply(_ state: QueueState) {
        // Queue actions can finish out of order over HTTP.
        if let queue, state.revision < queue.revision { return }
        queue = state
        guard let entry = currentEntry, !state.finished else {
            wantsToPlay = false
            avPlayer.pause()
            seekVersion += 1
            seeking = false
            loadedPlayID = nil
            clearItemObservers()
            avPlayer.replaceCurrentItem(with: nil)
            isPlaying = false
            elapsed = 0
            duration = 0
            cropStart = 0
            cropEnd = 0
            artworkTask?.cancel()
            artwork = nil
            updateNowPlaying()
            return
        }
        if loadedPlayID != state.playId {
            loadedPlayID = state.playId
            load(entry.track)
        }
        let upcoming = state.entries.dropFirst(state.index + 1).prefix(ExternalPrewarm.queueLookahead)
        for next in upcoming { prewarm(next.track, core.core) }
    }

    /// Where AVPlayer reads a track: the core's stream of a library track, or
    /// of a track outside the library that the core resolves from its source.
    private func url(for track: PlayerTrack) -> URL? {
        if let external = Self.externalStream(track) {
            return core.core?.externalStreamURL(
                source: external.source, externalId: external.externalId, artist: track.artist, title: track.title
            )
        }
        guard let id = track.id else { return nil }
        return streamURL?(id) ?? core.core?.streamURL(trackID: id)
    }

    private func load(_ track: PlayerTrack, at seconds: Double = 0, continuing: Bool = false, playing: Bool = true) {
        guard let url = url(for: track) else { return }
        loadedCore = core.core
        var options: [String: Any] = [AVURLAssetPreferPreciseDurationAndTimingKey: true]
        // AVFoundation has no public option for request headers; this key is
        // the one it reads them from. The core refuses a stream without the
        // launch secret.
        if let headers = core.core?.streamHeaders { options["AVURLAssetHTTPHeaderFieldsKey"] = headers }
        let asset = AVURLAsset(url: url, options: options)
        let item = AVPlayerItem(asset: asset)
        clearItemObservers()
        seekVersion += 1
        seeking = false
        handledEnd = false
        lastError = nil
        isPlaying = false
        let entryID = currentEntry?.id
        endObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.didPlayToEndTimeNotification, object: item, queue: .main
        ) { [weak self, weak item] _ in
            MainActor.assumeIsolated {
                guard let self, let item else { return }
                let version = self.seekVersion
                Task { @MainActor in
                    guard self.seekVersion == version else { return }
                    await self.ended(item: item, entryID: entryID)
                }
            }
        }
        failureObserver = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.failedToPlayToEndTimeNotification, object: item, queue: .main
        ) { [weak self, weak item] note in
            let error = note.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? Error
            Task { @MainActor in
                guard let self, let item, self.avPlayer.currentItem === item else { return }
                self.failed(error, item: item)
            }
        }
        durationObservation = item.observe(\.duration, options: [.initial, .new]) { [weak self] item, _ in
            Task { @MainActor in
                guard let self, self.avPlayer.currentItem === item else { return }
                self.refreshDuration(item)
            }
        }
        itemStatusObservation = item.observe(\.status, options: [.initial, .new]) { [weak self] item, _ in
            Task { @MainActor in
                guard let self, self.avPlayer.currentItem === item else { return }
                if item.status == .failed { self.failed(item.error, item: item) }
                else if item.status == .readyToPlay { self.refreshDuration(item) }
            }
        }
        avPlayer.replaceCurrentItem(with: item)
        cropStart = max(0, Double(track.cropStartMs ?? 0) / 1000)
        cropEnd = max(0, Double(track.cropEndMs ?? 0) / 1000)
        elapsed = seconds
        let estimatedEnd = cropEnd > cropStart ? cropEnd : Double(track.durationMs ?? 0) / 1000
        duration = max(0, estimatedEnd - cropStart)
        if !continuing { tracker.start(track) }
        if cropStart > 0 || seconds > 0 { seek(to: seconds) }
        wantsToPlay = playing
        if playing {
            activateSession()
            avPlayer.play()
        }
        loadArtwork(for: track)
        updateNowPlaying()
    }

    private func ended(item: AVPlayerItem, entryID: String?) async {
        guard avPlayer.currentItem === item, !seeking, !handledEnd, wantsToPlay else { return }
        handledEnd = true
        refreshDuration(item)
        let actual = item.currentTime().seconds
        if actual.isFinite { tracker.advance(to: max(0, actual - cropStart)) }
        elapsed = duration
        isPlaying = false
        updateNowPlaying()
        finishListening(completed: true)
        await reportEnd(entryID: entryID)
    }

    /// Tells the core an entry ended; it answers with what plays next. The
    /// entry is the one that ended, never whichever is current by then.
    private func reportEnd(entryID: String?) async {
        let entry = Components.Schemas.PlayerEntryRequest(entryId: entryID)
        await change { client, session in
            try await client.endedInQueue(path: .init(session: session), body: .json(entry)).ok.body.json
        }
    }

    private func clearItemObservers() {
        if let endObserver { NotificationCenter.default.removeObserver(endObserver) }
        if let failureObserver { NotificationCenter.default.removeObserver(failureObserver) }
        endObserver = nil
        failureObserver = nil
        durationObservation = nil
        itemStatusObservation = nil
    }

    /// A track that fails is loaded once more, after making sure the core still
    /// answers: iOS can reclaim a suspended app's listening socket, which a
    /// lock-screen play would otherwise meet. A second failure is the file
    /// itself, such as a format AVPlayer cannot open, so the queue moves on
    /// rather than stopping there.
    private func failed(_ error: Error?, item: AVPlayerItem) {
        // An item reports a failure both by status and by notification.
        guard failedItem !== item else { return }
        failedItem = item
        let reason = error?.localizedDescription ?? "This track could not be played."
        let wanted = wantsToPlay
        avPlayer.pause()
        isPlaying = false
        guard let track = current, let playID = loadedPlayID else {
            pause()
            lastError = "Playback: " + reason
            return
        }
        if reloadedPlayID != playID {
            reloadedPlayID = playID
            let position = elapsed
            Task {
                await core.ensureRunning()
                guard loadedPlayID == playID, queue?.finished != true else { return }
                load(track, at: position, continuing: true, playing: wanted)
            }
            return
        }
        consecutiveFailures += 1
        let message = "Playback: Skipped \(track.title ?? "a track"): \(reason)"
        guard wanted, consecutiveFailures < Self.maxConsecutiveFailures else {
            pause()
            lastError = "Playback: " + reason
            return
        }
        pause()
        let entry = entryRequest()
        Task {
            await change { client, session in
                try await client.nextInQueue(path: .init(session: session), body: .json(entry)).ok.body.json
            }
            if loadedPlayID != playID || queue?.finished == true {
                lastError = message
            } else if lastError == nil {
                lastError = "Playback: " + reason
            }
        }
    }

    private func refreshDuration(_ item: AVPlayerItem) {
        let seconds = item.duration.seconds
        guard seconds.isFinite, seconds > 0 else { return }
        let effectiveEnd = cropEnd > cropStart ? min(cropEnd, seconds) : seconds
        let cropped = max(0, effectiveEnd - cropStart)
        guard cropped != duration else { return }
        duration = cropped
        updateNowPlaying()
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
        sessionObservers.append(center.addObserver(forName: AVAudioSession.interruptionNotification, object: session, queue: .main) { [weak self] note in
            Task { @MainActor in self?.handleInterruption(note) }
        })
        sessionObservers.append(center.addObserver(forName: AVAudioSession.routeChangeNotification, object: session, queue: .main) { [weak self] note in
            Task { @MainActor in self?.handleRouteChange(note) }
        })
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
            // Paused for real, so the controls say so if the call ends
            // without iOS asking to resume.
            let wasPlaying = wantsToPlay
            pause()
            resumeAfterInterruption = wasPlaying
        case .ended:
            let options = (note.userInfo?[AVAudioSessionInterruptionOptionKey] as? UInt).map(AVAudioSession.InterruptionOptions.init) ?? []
            if resumeAfterInterruption && options.contains(.shouldResume) {
                resume()
            }
            resumeAfterInterruption = false
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
        statusObservation = avPlayer.observe(\.timeControlStatus, options: [.new]) { [weak self] _, _ in
            Task { @MainActor in
                guard let self else { return }
                let playing = self.avPlayer.timeControlStatus == .playing
                if playing { self.consecutiveFailures = 0 }
                guard self.isPlaying != playing else { return }
                self.isPlaying = playing
                self.updateNowPlaying()
            }
        }
        timeObserver = avPlayer.addPeriodicTimeObserver(
            forInterval: CMTime(seconds: 0.1, preferredTimescale: 600), queue: .main
        ) { [weak self] _ in
            // Read the current item's clock on this main-queue callback. A
            // queued time from the previous item or seek must not rewind UI.
            MainActor.assumeIsolated { self?.tick() }
        }
    }

    private func tick() {
        guard !seeking, !handledEnd, let item = avPlayer.currentItem else { return }
        refreshDuration(item)
        let seconds = item.currentTime().seconds
        guard seconds.isFinite else { return }
        let position = max(0, seconds - cropStart)
        if avPlayer.timeControlStatus == .playing { tracker.advance(to: position) }
        elapsed = max(0, duration > 0 ? min(position, duration) : position)
        if cropEnd > cropStart, seconds >= cropEnd, wantsToPlay {
            let item = item
            let entryID = currentEntry?.id
            Task { await ended(item: item, entryID: entryID) }
        }
    }

    // MARK: Lock screen, Control Center, headphones

    private func configureRemoteCommands() {
        let center = MPRemoteCommandCenter.shared()
        register(center.playCommand) { [weak self] _ in
            Task { @MainActor in self?.resume() }
            return .success
        }
        register(center.pauseCommand) { [weak self] _ in
            Task { @MainActor in self?.pause() }
            return .success
        }
        register(center.togglePlayPauseCommand) { [weak self] _ in
            Task { @MainActor in self?.togglePlayPause() }
            return .success
        }
        register(center.nextTrackCommand) { [weak self] _ in
            Task { @MainActor in await self?.next() }
            return .success
        }
        register(center.previousTrackCommand) { [weak self] _ in
            Task { @MainActor in await self?.previous() }
            return .success
        }
        register(center.changePlaybackPositionCommand) { [weak self] event in
            guard let position = (event as? MPChangePlaybackPositionCommandEvent)?.positionTime else { return .commandFailed }
            Task { @MainActor in self?.seek(to: position) }
            return .success
        }
        center.skipForwardCommand.isEnabled = false
        center.skipBackwardCommand.isEnabled = false
    }

    private func register(_ command: MPRemoteCommand, handler: @escaping (MPRemoteCommandEvent) -> MPRemoteCommandHandlerStatus) {
        remoteTargets.append((command, command.addTarget(handler: handler)))
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
        let request: URLRequest
        if let id = Self.coverArtID(track), let local = core.core, let url = local.coverURL(id: id) {
            request = local.request(url)
        } else if let url = Self.coverURL(track) {
            // A source's own artwork, for a track outside the library.
            request = URLRequest(url: url)
        } else {
            return
        }
        artworkTask = Task {
            guard let (data, _) = try? await URLSession.shared.data(for: request),
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
            cropStartMs: t.cropStartMs, cropEndMs: t.cropEndMs,
            additionalProperties: (try? OpenAPIObjectContainer(unvalidatedValue: extra)) ?? .init()
        )
    }

    static func coverArtID(_ t: PlayerTrack) -> String? {
        ((t.additionalProperties.value["coverArtId"] ?? nil) as? String).flatMap { $0.isEmpty ? nil : $0 }
    }

    nonisolated static func coverURL(_ t: PlayerTrack) -> URL? {
        ((t.additionalProperties.value["coverUrl"] ?? nil) as? String).flatMap { $0.hasPrefix("https://") ? URL(string: $0) : nil }
    }

    /// A search result or recommendation outside the library, shaped as the
    /// desktop queues one: its id is source:externalId, and externalStream
    /// says where its audio comes from.
    nonisolated static func externalTrack(
        source: String, externalId: String, title: String, artist: String, album: String,
        durationMs: Int, coverUrl: String? = nil
    ) -> PlayerTrack {
        var extra: [String: (any Sendable)?] = [
            "externalStream": ["source": source, "externalId": externalId] as [String: (any Sendable)?],
        ]
        if let coverUrl { extra["coverUrl"] = coverUrl }
        return PlayerTrack(
            id: source + ":" + externalId, title: title, artist: artist, album: album, durationMs: durationMs,
            additionalProperties: (try? OpenAPIObjectContainer(unvalidatedValue: extra)) ?? .init()
        )
    }

    /// Projects either a library match or an external result into the one
    /// queue shape Player understands. Search and recommendations share this
    /// seam so their playback metadata cannot drift.
    nonisolated static func resolvedTrack(
        source: String, externalId: String, title: String, artist: String, album: String,
        durationMs: Int, libraryId: String?, coverArtId: String?, coverUrl: String?
    ) -> PlayerTrack {
        guard let libraryId else {
            return externalTrack(
                source: source, externalId: externalId, title: title, artist: artist,
                album: album, durationMs: durationMs, coverUrl: coverUrl
            )
        }
        var extra: [String: (any Sendable)?] = [:]
        if let coverArtId { extra["coverArtId"] = coverArtId }
        return PlayerTrack(
            id: libraryId, title: title, artist: artist, album: album, durationMs: durationMs,
            additionalProperties: (try? OpenAPIObjectContainer(unvalidatedValue: extra)) ?? .init()
        )
    }

    nonisolated static func externalStream(_ t: PlayerTrack) -> (source: String, externalId: String)? {
        guard let ref = (t.additionalProperties.value["externalStream"] ?? nil) as? [String: (any Sendable)?],
              let source = (ref["source"] ?? nil) as? String, !source.isEmpty,
              let externalId = (ref["externalId"] ?? nil) as? String, !externalId.isEmpty else { return nil }
        return (source, externalId)
    }
}
