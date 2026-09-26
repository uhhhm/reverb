import Foundation
import Reverbcore
import ReverbAPI
import UIKit

/// Runs the Go core inside the app (ADR 0003). The core is the phone's whole
/// Device: its database, sync, pairing and offline files. gomobile exposes
/// start, port, the loopback API's launch secret and stop, plus the Spotify
/// credential copy that stays off loopback HTTP (PairTarget also reads pairing
/// links through it);
/// everything else goes through the API client, which sends the secret.
@MainActor
final class CoreHost: ObservableObject {
    enum State: Equatable {
        case starting
        case running(LocalCore)
        case failed(String)
    }

    @Published private(set) var state: State = .starting
    @Published private(set) var ytDlpVersion = "Loading…"
    @Published private(set) var checkingYtDlp = false
    @Published private(set) var ytDlpUpdateError: String?
    /// This build's version, the newest published IPA and each paired
    /// device's protocol compatibility, as the core last reported them.
    @Published private(set) var version: Components.Schemas.VersionInfo?

    func refreshVersion() async {
        guard let client else { return }
        if let info = try? await client.getVersion().ok.body.json { version = info }
    }

    func checkYtDlpUpdate(force: Bool = false) async {
        guard core != nil, !checkingYtDlp else { return }
        checkingYtDlp = true
        ytDlpUpdateError = nil
        let result = await Task.detached(priority: .utility) {
            var error: NSError?
            ReverbcoreCheckYtDlpUpdate(force, &error)
            return (ReverbcoreYtDlpVersion(), error?.localizedDescription)
        }.value
        ytDlpVersion = result.0.isEmpty ? "Unavailable" : result.0
        ytDlpUpdateError = result.1
        checkingYtDlp = false
    }

    /// The core's data directory. Application Support is backed up, so the
    /// database (plays, playlists, pairing) survives a restore; the offline
    /// files under music/ are excluded, since a peer holds them anyway.
    let dataDirectory: URL

    private var terminateObserver: NSObjectProtocol?
    /// The latest credential refresh. Each waits for the one before it, so an
    /// older refresh cannot overwrite what a newer one stored.
    private var credentialRefresh: Task<Void, Never>?

    init() {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        dataDirectory = support.appendingPathComponent("Reverb", isDirectory: true)
        // UI tests start from nothing so each run pairs afresh.
        if ProcessInfo.processInfo.arguments.contains("--reset-data") {
            try? FileManager.default.removeItem(at: dataDirectory)
            SearchCredentialStore.clear()
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
                case let .success(core):
                    self.state = .running(core)
                    self.ytDlpVersion = ReverbcoreYtDlpVersion()
                    Task { await self.checkYtDlpUpdate() }
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
        if (try? await client.getHealth().ok) != nil {
            Task { await checkYtDlpUpdate() }
            return
        }
        let dir = dataDirectory
        state = .starting
        let result = await Task.detached(priority: .userInitiated) {
            ReverbcoreStop()
            return Self.startCore(dataDirectory: dir)
        }.value
        switch result {
        case let .success(core): state = .running(core)
        case let .failure(error): state = .failed(error.localizedDescription)
        }
    }

    /// Copies Spotify credentials from a paired desktop after pairing, unpairing
    /// and each foreground sync. The secret crosses the Go/Swift binding rather
    /// than the loopback API and is kept in this device's Keychain. When a
    /// paired device answers without credentials, or none is paired, the copy
    /// is forgotten; when none is reachable, the current copy is kept. The core
    /// reloads its search sources in place, so playback is not interrupted.
    func refreshSearchCredentials() async {
        guard core != nil else { return }
        let previous = credentialRefresh
        let refresh = Task {
            await previous?.value
            await Self.copySearchCredentials()
        }
        credentialRefresh = refresh
        await refresh.value
    }

    nonisolated private static func copySearchCredentials() async {
        await Task.detached(priority: .utility) {
            var error: NSError?
            let json = ReverbcoreCopySpotifyCredentials(&error)
            guard error == nil else { return }
            if json.isEmpty {
                SearchCredentialStore.clear()
                ReverbcoreSetSpotifyCredentials("", "")
                return
            }
            guard let data = json.data(using: .utf8),
                  let credentials = try? JSONDecoder().decode(SpotifyCredentials.self, from: data),
                  (try? SearchCredentialStore.save(credentials)) == true else { return }
            ReverbcoreSetSpotifyCredentials(credentials.clientId, credentials.clientSecret)
        }.value
    }

    nonisolated private static func startCore(dataDirectory: URL) -> Result<LocalCore, Error> {
        if let credentials = SearchCredentialStore.load() {
            ReverbcoreSetSpotifyCredentials(credentials.clientId, credentials.clientSecret)
        } else {
            ReverbcoreSetSpotifyCredentials("", "")
        }
        // The install phase puts the standard library and yt-dlp in the bundle.
        let bundle = Bundle.main.bundleURL
        ReverbcoreConfigurePython(
            bundle.appendingPathComponent("python").path,
            bundle.appendingPathComponent("app_packages").path
        )
        var port = 0
        var error: NSError?
        let started = ReverbcoreStart(dataDirectory.path, &port, &error)
        if let error { return .failure(error) }
        let secret = ReverbcoreSecret()
        if !started || secret.isEmpty { return .failure(CoreError.notStarted) }
        excludeFromBackup(dataDirectory.appendingPathComponent("music", isDirectory: true))
        return .success(LocalCore(port: port, secret: secret))
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
