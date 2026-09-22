import Foundation
import Reverbcore
import ReverbAPI
import UIKit

/// Runs the Go core inside the app (ADR 0003). The core is the phone's whole
/// Device: its database, sync, pairing and offline files. gomobile exposes only
/// start, port and stop; everything else goes through the API client.
@MainActor
final class CoreHost: ObservableObject {
    enum State: Equatable {
        case starting
        case running(LocalCore)
        case failed(String)
    }

    @Published private(set) var state: State = .starting

    /// The core's data directory. Application Support is backed up, so the
    /// database (plays, playlists, pairing) survives a restore; the offline
    /// files under music/ are excluded, since a peer holds them anyway.
    let dataDirectory: URL

    private var terminateObserver: NSObjectProtocol?

    init() {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        dataDirectory = support.appendingPathComponent("Reverb", isDirectory: true)
        // UI tests start from nothing so each run pairs afresh.
        if ProcessInfo.processInfo.arguments.contains("--reset-data") {
            try? FileManager.default.removeItem(at: dataDirectory)
        }
        terminateObserver = NotificationCenter.default.addObserver(
            forName: UIApplication.willTerminateNotification, object: nil, queue: .main
        ) { _ in
            ReverbcoreStop()
        }
    }

    var core: LocalCore? {
        if case let .running(core) = state { return core }
        return nil
    }

    var client: Client? { core?.client() }

    /// Starts the core. Opening and migrating the database takes a moment, so
    /// it runs off the main thread.
    func start() {
        state = .starting
        let dir = dataDirectory
        Task.detached(priority: .userInitiated) {
            let result = Self.startCore(dataDirectory: dir)
            await MainActor.run {
                switch result {
                case let .success(port): self.state = .running(LocalCore(port: port))
                case let .failure(error): self.state = .failed(error.localizedDescription)
                }
            }
        }
    }

    /// Checks the core still answers after the app was suspended, and starts
    /// it again if not: iOS can reclaim a suspended app's listening socket.
    func ensureRunning() async {
        guard let client else {
            if case .failed = state { start() }
            return
        }
        if (try? await client.getHealth().ok) != nil { return }
        let dir = dataDirectory
        state = .starting
        let result = await Task.detached(priority: .userInitiated) {
            ReverbcoreStop()
            return Self.startCore(dataDirectory: dir)
        }.value
        switch result {
        case let .success(port): state = .running(LocalCore(port: port))
        case let .failure(error): state = .failed(error.localizedDescription)
        }
    }

    nonisolated private static func startCore(dataDirectory: URL) -> Result<Int, Error> {
        var port = 0
        var error: NSError?
        let started = ReverbcoreStart(dataDirectory.path, &port, &error)
        if let error { return .failure(error) }
        if !started { return .failure(CoreError.notStarted) }
        excludeFromBackup(dataDirectory.appendingPathComponent("music", isDirectory: true))
        return .success(port)
    }

    /// Keeps offline files out of iCloud and device backups. Excluding the
    /// folder excludes everything in it, including files fetched later.
    nonisolated private static func excludeFromBackup(_ folder: URL) {
        var url = folder
        try? FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try? url.setResourceValues(values)
    }

    enum CoreError: LocalizedError {
        case notStarted
        var errorDescription: String? { "The Reverb core did not start." }
    }
}
