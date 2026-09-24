import AVFoundation
import MediaToolbox
import XCTest
import ReverbAPI
@testable import Reverb

/// Checks what is heard against what the player's clock says. The fixture is
/// a chirp whose pitch is 300 + 20t Hz, so any window of rendered audio names
/// the moment of the file it came from. A player that maps time onto bytes by
/// an assumed bitrate passes every clock check and still plays the wrong part.
@MainActor
final class SeekAccuracyTests: XCTestCase {
    private func waitUntil(_ predicate: () -> Bool) async throws {
        for _ in 0..<200 {
            if predicate() { return }
            try await Task.sleep(for: .milliseconds(25))
        }
        XCTFail("Playback condition did not become true within five seconds")
    }

    private func state() throws -> QueueState {
        let json = """
        {"entries":[{"id":"entry","origin":"listener","track":{"id":"chirp","durationMs":0}}],
         "index":0,"shuffle":false,"repeat":"off","upNext":[],"playId":1,"finished":false,"revision":1}
        """
        return try JSONDecoder().decode(QueueState.self, from: Data(json.utf8))
    }

    func testHeardAudioMatchesClockAfterSeeks() async throws {
        let url = try XCTUnwrap(Bundle(for: Self.self).url(forResource: "chirp", withExtension: "mp3"))
        let av = AVPlayer()
        let player = Player(core: CoreHost(), avPlayer: av, streamURL: { _ in url })
        player.apply(try state())
        try await waitUntil { av.currentItem?.status == .readyToPlay }
        let item = try XCTUnwrap(av.currentItem)
        let capture = RenderedAudio()
        let tracks = try await item.asset.loadTracks(withMediaType: .audio)
        let track = try XCTUnwrap(tracks.first)
        let parameters = AVMutableAudioMixInputParameters(track: track)
        parameters.audioTapProcessor = try XCTUnwrap(capture.makeTap())
        let mix = AVMutableAudioMix()
        mix.inputParameters = [parameters]
        item.audioMix = mix

        try await waitUntil { player.elapsed > 0.5 }
        // The file has no duration index; its bitrate would suggest 53.6s.
        XCTAssertEqual(player.duration, 60, accuracy: 0.1)
        for target in [45.0, 7.5, 30.2, 58.0] {
            player.seek(to: target)
            capture.reset()
            try await waitUntil { player.elapsed > target + 0.5 }
            let (claimed, samples) = try XCTUnwrap(capture.latest(8192), "no audio rendered after seeking to \(target)")
            let heard = try XCTUnwrap(Self.chirpTime(samples, sampleRate: capture.sampleRate))
            XCTAssertEqual(claimed, target + 0.5, accuracy: 0.5, "the rendered audio is not near the seek target")
            XCTAssertEqual(heard, claimed, accuracy: 0.15, "after seeking to \(target) the audio heard is not where the clock says")
        }
        player.pause()
    }

    /// The moment of the chirp a window of samples comes from.
    private static func chirpTime(_ samples: [Float], sampleRate: Double) -> Double? {
        // An 8-sample boxcar nulls the fixture's 11025 Hz side tone at 44.1 kHz.
        guard sampleRate == 44100, samples.count > 16 else { return nil }
        var filtered = [Float](repeating: 0, count: samples.count - 8)
        for i in filtered.indices { filtered[i] = samples[i..<(i + 8)].reduce(0, +) }
        var crossings: [Int] = []
        for i in 1..<filtered.count where filtered[i - 1] < 0 && filtered[i] >= 0 { crossings.append(i) }
        guard let first = crossings.first, let last = crossings.last, crossings.count > 2 else { return nil }
        let frequency = Double(crossings.count - 1) / (Double(last - first) / sampleRate)
        return (frequency - 300) / 20
    }
}

/// Collects the first channel AVPlayer renders, with the source time of each
/// buffer as the player reports it.
private final class RenderedAudio: @unchecked Sendable {
    private let lock = NSLock()
    private var buffers: [(start: Double, samples: [Float])] = []
    private(set) var sampleRate: Double = 0

    func reset() {
        lock.withLock { buffers.removeAll() }
    }

    /// The latest n contiguous samples, and the source time of their middle.
    func latest(_ n: Int) -> (Double, [Float])? {
        lock.withLock {
            var samples: [Float] = []
            var start = 0.0
            for buffer in buffers.reversed() {
                samples.insert(contentsOf: buffer.samples, at: 0)
                start = buffer.start
                if samples.count >= n { break }
            }
            guard samples.count >= n, sampleRate > 0 else { return nil }
            let drop = samples.count - n
            return (start + (Double(drop) + Double(n) / 2) / sampleRate, Array(samples[drop...]))
        }
    }

    fileprivate func append(_ start: Double, _ samples: [Float]) {
        lock.withLock {
            buffers.append((start, samples))
            if buffers.count > 200 { buffers.removeFirst(buffers.count - 200) }
        }
    }

    fileprivate func prepared(sampleRate: Double) {
        lock.withLock { self.sampleRate = sampleRate }
    }

    func makeTap() -> MTAudioProcessingTap? {
        var callbacks = MTAudioProcessingTapCallbacks(
            version: kMTAudioProcessingTapCallbacksVersion_0,
            clientInfo: Unmanaged.passRetained(self).toOpaque(),
            init: { _, clientInfo, storage in storage.pointee = clientInfo },
            finalize: { tap in Unmanaged<RenderedAudio>.fromOpaque(MTAudioProcessingTapGetStorage(tap)).release() },
            prepare: { tap, _, format in
                Unmanaged<RenderedAudio>.fromOpaque(MTAudioProcessingTapGetStorage(tap)).takeUnretainedValue()
                    .prepared(sampleRate: format.pointee.mSampleRate)
            },
            unprepare: nil,
            process: { tap, frames, _, buffers, framesOut, flagsOut in
                var range = CMTimeRange()
                guard MTAudioProcessingTapGetSourceAudio(tap, frames, buffers, flagsOut, &range, framesOut) == noErr else { return }
                let list = UnsafeMutableAudioBufferListPointer(buffers)
                let count = Int(framesOut.pointee)
                guard count > 0, let data = list[0].mData, range.start.seconds.isFinite else { return }
                let samples = Array(UnsafeBufferPointer(start: data.assumingMemoryBound(to: Float.self), count: count))
                Unmanaged<RenderedAudio>.fromOpaque(MTAudioProcessingTapGetStorage(tap)).takeUnretainedValue()
                    .append(range.start.seconds, samples)
            }
        )
        var tap: MTAudioProcessingTap?
        guard MTAudioProcessingTapCreate(kCFAllocatorDefault, &callbacks, kMTAudioProcessingTapCreationFlag_PostEffects, &tap) == noErr else {
            Unmanaged<RenderedAudio>.fromOpaque(callbacks.clientInfo!).release()
            return nil
        }
        return tap
    }
}
