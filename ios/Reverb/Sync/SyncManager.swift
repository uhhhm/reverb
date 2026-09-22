import BackgroundTasks
import Foundation
import ReverbAPI

/// Coordinates the short sync opportunities iOS gives the embedded core.
/// It never keeps the app alive: foreground/audio timers stop with their task,
/// and background work runs only inside a system-granted refresh window.
@MainActor
final class SyncManager: ObservableObject {
    static let refreshIdentifier = "io.github.uhhhm.reverb.sync"

    @Published private(set) var status: Components.Schemas.SyncRound?
    @Published private(set) var lastSync: Date?
    @Published private(set) var lastError: String?

    private let core: CoreHost
    private var playbackTask: Task<Void, Never>?

    init(core: CoreHost) {
        self.core = core
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.refreshIdentifier, using: nil) { [weak self] task in
            guard let refresh = task as? BGAppRefreshTask else {
                task.setTaskCompleted(success: false)
                return
            }
            Task { @MainActor in self?.handle(refresh) }
        }
    }

    func foregrounded() async {
        await core.ensureRunning()
        await waitForCore()
        await syncNow()
        scheduleBackgroundRefresh()
    }

    func setPlaybackActive(_ active: Bool) {
        playbackTask?.cancel()
        playbackTask = nil
        guard active else { return }
        playbackTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.syncNow()
                try? await Task.sleep(for: .seconds(300))
            }
        }
    }

    func refreshStatus() async {
        guard let client = core.client else { return }
        do {
            let snapshot = try await client.getSyncStatus().ok.body.json
            let round = snapshot.round?.value1
            status = round
            if let round, round.finishedAt > 0 {
                lastSync = Date(timeIntervalSince1970: TimeInterval(round.finishedAt) / 1000)
            }
            if let errors = round?.errors, !errors.isEmpty {
                lastError = errors.joined(separator: "\n")
            }
        } catch {
            lastError = error.localizedDescription
        }
    }

    func syncNow(timeout: Duration = .seconds(25)) async {
        guard let client = core.client else { return }
        do {
            _ = try await client.triggerSync().accepted
            let clock = ContinuousClock()
            let deadline = clock.now.advanced(by: timeout)
            repeat {
                await refreshStatus()
                guard status?.state == .pending || status?.state == .running else { break }
                try? await Task.sleep(for: .milliseconds(350))
            } while clock.now < deadline && !Task.isCancelled
            if status?.state == .completed || status?.state == .no_peers {
                lastError = nil
            }
        } catch {
            lastError = error.localizedDescription
        }
    }

    func scheduleBackgroundRefresh() {
        let request = BGAppRefreshTaskRequest(identifier: Self.refreshIdentifier)
        request.earliestBeginDate = Date(timeIntervalSinceNow: 15 * 60)
        try? BGTaskScheduler.shared.submit(request)
    }

    private func handle(_ task: BGAppRefreshTask) {
        scheduleBackgroundRefresh()
        let work = Task { [weak self] in
            guard let self else { return false }
            await self.core.ensureRunning()
            await self.waitForCore()
            await self.syncNow(timeout: .seconds(20))
            return !Task.isCancelled && self.lastError == nil
        }
        task.expirationHandler = { work.cancel() }
        Task { task.setTaskCompleted(success: await work.value) }
    }

    private func waitForCore() async {
        for _ in 0..<100 where core.client == nil && !Task.isCancelled {
            try? await Task.sleep(for: .milliseconds(50))
        }
    }
}
