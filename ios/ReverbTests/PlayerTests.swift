import AVFoundation
import XCTest
import ReverbAPI
@testable import Reverb

@MainActor
final class PlayerTests: XCTestCase {
    private func state(revision: Int64, durationMs: Int = 2400, finished: Bool = false) throws -> QueueState {
        let json = """
        {"entries":[{"id":"entry-\(revision)","origin":"listener","track":{"id":"track-\(revision)","durationMs":\(durationMs)}}],
         "index":0,"shuffle":false,"repeat":"off","upNext":[],"playId":\(revision),"finished":\(finished),"revision":\(revision)}
        """
        return try JSONDecoder().decode(QueueState.self, from: Data(json.utf8))
    }

    private func fixture() throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".wav")
        let format = AVAudioFormat(standardFormatWithSampleRate: 44100, channels: 1)!
        let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: 88200)!
        buffer.frameLength = buffer.frameCapacity
        for i in 0..<Int(buffer.frameLength) {
            buffer.floatChannelData![0][i] = Float(sin(Double(i) * 440 * 2 * .pi / 44100)) * 0.1
        }
        let file = try AVAudioFile(forWriting: url, settings: format.settings)
        try file.write(from: buffer)
        addTeardownBlock { try? FileManager.default.removeItem(at: url) }
        return url
    }

    private func waitUntil(_ predicate: () -> Bool) async throws {
        for _ in 0..<200 {
            if predicate() { return }
            try await Task.sleep(for: .milliseconds(25))
        }
        XCTFail("Playback condition did not become true within five seconds")
    }

    func testDecodedDurationReplacesNearbyCatalogEstimate() async throws {
        let url = try fixture()
        let av = AVPlayer()
        let player = Player(core: CoreHost(), avPlayer: av, streamURL: { _ in url })
        player.apply(try state(revision: 1))
        try await waitUntil { player.elapsed > 0.5 }
        XCTAssertEqual(player.duration, 2, accuracy: 0.02, "The progress bar must end when the audio ends, even when metadata is only 400ms wrong")
        player.pause()
    }

    func testCropStartsAtOffsetAndEndsBeforeFileFinishes() async throws {
        let url = try fixture()
        let core = CoreHost()
        core.start()
        try await waitUntil { core.client != nil }
        let av = AVPlayer()
        let player = Player(core: core, avPlayer: av, streamURL: { _ in url })
        let track = PlayerTrack(id: "clip", title: "Clip", durationMs: 2000,
                                cropStartMs: 500, cropEndMs: 1300)
        await player.play([track], startAt: 0)
        try await waitUntil { av.currentTime().seconds >= 0.6 }
        XCTAssertEqual(player.duration, 0.8, accuracy: 0.05)
        XCTAssertLessThan(player.elapsed, 0.5)
        try await waitUntil { player.queue?.finished == true }
        XCTAssertNil(av.currentItem)
    }

    func testDelayedQueueResponseCannotReplaceNewerTrack() throws {
        let url = try fixture()
        let player = Player(core: CoreHost(), streamURL: { _ in url })
        player.apply(try state(revision: 2))
        player.apply(try state(revision: 1))
        XCTAssertEqual(player.current?.id, "track-2")
        player.pause()
    }

    func testFinishedQueueReleasesMedia() throws {
        let url = try fixture()
        let av = AVPlayer()
        let player = Player(core: CoreHost(), avPlayer: av, streamURL: { _ in url })
        player.apply(try state(revision: 1))
        player.apply(try state(revision: 2, finished: true))
        XCTAssertNil(av.currentItem)
        XCTAssertFalse(player.isPlaying)
    }
    func testSeekClampsToAudioAndStaysPaused() async throws {
        let url = try fixture()
        let av = AVPlayer()
        let player = Player(core: CoreHost(), avPlayer: av, streamURL: { _ in url })
        player.apply(try state(revision: 1, durationMs: 2000))
        try await waitUntil { player.elapsed > 0.25 }
        player.pause()
        player.seek(to: -10)
        XCTAssertEqual(player.elapsed, 0)
        player.seek(to: 100)
        XCTAssertEqual(player.elapsed, 2)
        player.seek(to: 0.75)
        try await waitUntil { abs(av.currentTime().seconds - 0.75) < 0.02 }
        XCTAssertEqual(player.elapsed, 0.75, accuracy: 0.02)
        XCTAssertEqual(av.rate, 0)
    }

    func testNaturalEndCompletesProgressBeforeQueueResponse() async throws {
        let url = try fixture()
        let player = Player(core: CoreHost(), streamURL: { _ in url })
        player.apply(try state(revision: 1))
        try await waitUntil { player.elapsed > 1.8 && !player.isPlaying }
        XCTAssertEqual(player.elapsed, player.duration, accuracy: 0.02)
        player.pause()
    }

    func testFailedMediaReportsErrorAndStops() async throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".wav")
        let player = Player(core: CoreHost(), streamURL: { _ in url })
        player.apply(try state(revision: 1))
        try await waitUntil { player.lastError != nil }
        XCTAssertFalse(player.isPlaying)
        player.pause()
    }

    func testVariableBitrateMP3DurationAndSeeking() async throws {
        let url = try XCTUnwrap(Bundle(for: Self.self).url(forResource: "variable-bitrate", withExtension: "mp3"))
        let av = AVPlayer()
        let player = Player(core: CoreHost(), avPlayer: av, streamURL: { _ in url })
        player.apply(try state(revision: 1, durationMs: 9000))
        try await waitUntil { player.elapsed > 0.25 }
        XCTAssertEqual(player.duration, 4.05, accuracy: 0.08)
        player.seek(to: 3)
        try await waitUntil { player.elapsed >= 3 && !player.isPlaying }
        XCTAssertEqual(player.elapsed, player.duration, accuracy: 0.02)
        player.pause()
    }

    func testRealCoreQueueAdvancesOnceAndStopsAtEnd() async throws {
        let url = try fixture()
        let core = CoreHost()
        core.start()
        try await waitUntil { core.client != nil }
        let av = AVPlayer()
        let player = Player(core: core, avPlayer: av, streamURL: { _ in url })
        let tracks = ["first", "second"].map { id in
            LibraryTrack(id: id, title: id, albumId: "", album: "", artistId: "", artist: "",
                         coverArtId: "", trackNumber: 1, discNumber: 1, durationMs: 9000,
                         bitRate: 0, suffix: "wav", contentType: "audio/wav")
        }
        await player.play(tracks, startAt: 0)
        let oldItem = try XCTUnwrap(av.currentItem)
        try await waitUntil { player.current?.id == "second" && player.elapsed > 0.1 }
        NotificationCenter.default.post(name: AVPlayerItem.didPlayToEndTimeNotification, object: oldItem)
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(player.current?.id, "second")
        XCTAssertNotEqual(player.queue?.finished, true)
        try await waitUntil { player.queue?.finished == true }
        XCTAssertNil(av.currentItem)
        XCTAssertFalse(player.wantsToPlay)
    }

    func testReplacedTrackIgnoresAlreadyQueuedEndCallback() async throws {
        let url = try fixture()
        let av = AVPlayer()
        let player = Player(core: CoreHost(), avPlayer: av, streamURL: { _ in url })
        player.apply(try state(revision: 1))
        let item = try XCTUnwrap(av.currentItem)
        NotificationCenter.default.post(name: AVPlayerItem.didPlayToEndTimeNotification, object: item)
        player.apply(try state(revision: 2))
        try await waitUntil { player.elapsed > 0.2 }
        XCTAssertLessThan(player.elapsed, 1)
        XCTAssertEqual(player.current?.id, "track-2")
        player.pause()
    }

    func testRapidSeeksAndNonfiniteInputDoNotRewindOrResume() async throws {
        let url = try fixture()
        let av = AVPlayer()
        let player = Player(core: CoreHost(), avPlayer: av, streamURL: { _ in url })
        player.apply(try state(revision: 1, durationMs: 2000))
        try await waitUntil { player.elapsed > 0.1 }
        player.pause()
        for value in [1.5, 0.2, 1.0, 0.6] { player.seek(to: value) }
        player.seek(to: .nan)
        player.seek(to: .infinity)
        try await waitUntil { abs(av.currentTime().seconds - 0.6) < 0.02 }
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertEqual(player.elapsed, 0.6, accuracy: 0.02)
        XCTAssertFalse(player.isPlaying)
        XCTAssertEqual(av.rate, 0)
    }

    func testSeekingToEndAdvancesQueue() async throws {
        let url = try fixture()
        let core = CoreHost()
        core.start()
        try await waitUntil { core.client != nil }
        let player = Player(core: core, streamURL: { _ in url })
        let tracks = ["seek-first", "seek-second"].map { id in
            LibraryTrack(id: id, title: id, albumId: "", album: "", artistId: "", artist: "",
                         coverArtId: "", trackNumber: 1, discNumber: 1, durationMs: 2000,
                         bitRate: 0, suffix: "wav", contentType: "audio/wav")
        }
        await player.play(tracks, startAt: 0)
        try await waitUntil { player.elapsed > 0.1 }
        player.seek(to: player.duration)
        try await waitUntil { player.current?.id == "seek-second" }
        player.pause()
    }

    private func unplayable() throws -> URL {
        // An EBML header and nothing AVFoundation can demux, as a WebM is to it.
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".webm")
        try Data([0x1A, 0x45, 0xDF, 0xA3] + [UInt8](repeating: 0x42, count: 4096)).write(to: url)
        addTeardownBlock { try? FileManager.default.removeItem(at: url) }
        return url
    }

    private func startedCore() async throws -> CoreHost {
        let core = CoreHost()
        core.start()
        try await waitUntil { core.client != nil }
        return core
    }

    private func libraryTracks(_ ids: [String]) -> [LibraryTrack] {
        ids.map { id in
            LibraryTrack(id: id, title: id, albumId: "", album: "", artistId: "", artist: "",
                         coverArtId: "", trackNumber: 1, discNumber: 1, durationMs: 2000,
                         bitRate: 0, suffix: "wav", contentType: "audio/wav")
        }
    }

    func testFailedLoadIsRetriedOnceAndRecovers() async throws {
        let good = try fixture()
        let missing = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".wav")
        var calls = 0
        let player = Player(core: CoreHost(), streamURL: { _ in
            calls += 1
            return calls == 1 ? missing : good
        })
        player.apply(try state(revision: 1))
        try await waitUntil { player.isPlaying && player.elapsed > 0.2 }
        XCTAssertEqual(calls, 2)
        XCTAssertNil(player.lastError)
        player.pause()
    }

    func testUnplayableTrackIsSkipped() async throws {
        let good = try fixture()
        let bad = try unplayable()
        let player = Player(core: try await startedCore(), streamURL: { $0 == "skip-bad" ? bad : good })
        await player.play(libraryTracks(["skip-bad", "skip-good"]), startAt: 0)
        try await waitUntil { player.current?.id == "skip-good" && player.isPlaying }
        XCTAssertTrue(player.lastError?.contains("Skipped skip-bad") == true, player.lastError ?? "no error")
        player.pause()
    }

    func testQueueOfUnplayableTracksStopsInsteadOfSpinning() async throws {
        let bad = try unplayable()
        let player = Player(core: try await startedCore(), streamURL: { _ in bad })
        await player.play(libraryTracks((1...6).map { "cap-\($0)" }), startAt: 0)
        try await waitUntil { !player.wantsToPlay && player.lastError != nil && player.current?.id == "cap-3" }
        try await Task.sleep(for: .milliseconds(300))
        XCTAssertEqual(player.current?.id, "cap-3", "skipping stops after three failures in a row")
        XCTAssertFalse(player.wantsToPlay)
    }

    func testInterruptionPausesAndResumesOnlyWhenAsked() async throws {
        let url = try fixture()
        let session = AVAudioSession.sharedInstance()
        let player = Player(core: CoreHost(), streamURL: { _ in url })
        player.apply(try state(revision: 1))
        try await waitUntil { player.isPlaying }
        func interrupt(_ type: AVAudioSession.InterruptionType, resume: Bool = false) async {
            var info: [AnyHashable: Any] = [AVAudioSessionInterruptionTypeKey: type.rawValue]
            if resume { info[AVAudioSessionInterruptionOptionKey] = AVAudioSession.InterruptionOptions.shouldResume.rawValue }
            NotificationCenter.default.post(name: AVAudioSession.interruptionNotification, object: session, userInfo: info)
            try? await Task.sleep(for: .milliseconds(50))
        }
        await interrupt(.began)
        XCTAssertFalse(player.wantsToPlay, "the controls must show paused during a call")
        await interrupt(.ended, resume: true)
        try await waitUntil { player.isPlaying }
        await interrupt(.began)
        await interrupt(.ended)
        try await Task.sleep(for: .milliseconds(200))
        XCTAssertFalse(player.wantsToPlay)
        XCTAssertFalse(player.isPlaying)
    }

}
