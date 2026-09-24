import ReverbAPI
import SwiftUI

struct DevicesView: View {
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var pairing: PairingModel
    @EnvironmentObject private var sync: SyncManager
    @State private var devices: [Device] = []
    @State private var pending: Components.Schemas.PendingUploads?

    struct Device: Identifiable {
        let id: String
        let name: String
        let lastSeen: Date?
        let isServer: Bool
    }

    var body: some View {
        List {
            Section("Sync") {
                LabeledContent("Status", value: sync.statusText)
                if let lastSync = sync.lastSync {
                    LabeledContent("Last sync", value: lastSync.formatted(.relative(presentation: .named)))
                }
                if let error = sync.lastError {
                    Text(error).font(.caption).foregroundStyle(.red)
                }
                Button {
                    Task { await sync.syncNow() }
                } label: {
                    if sync.isRunning { ProgressView() } else { Label("Sync now", systemImage: "arrow.triangle.2.circlepath") }
                }
                .disabled(sync.isRunning)
                .accessibilityIdentifier("sync.now")
            }
            if let pending, !pending.files.isEmpty {
                PendingUploadsSection(pending: pending)
            }
            Section {
                ForEach(devices) { device in
                    VStack(alignment: .leading) {
                        Text(device.name)
                        Text(device.lastSeen.map { "Last reached \($0.formatted(.relative(presentation: .named)))" } ?? "Not reached yet")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    .accessibilityIdentifier("device.\(device.name)")
                    .swipeActions {
                        if !device.isServer {
                            Button("Unpair", role: .destructive) { Task { await unpair(device) } }
                        }
                    }
                }
            } header: {
                Text("Paired devices")
            } footer: {
                Text("This iPhone syncs with these devices on your network, or over your own VPN.")
            }
            Section {
                Button("Pair a device") { pairing.isPresented = true }
                    .accessibilityIdentifier("devices.pair")
            }
        }
        .navigationTitle("Devices")
        .task(id: pairing.isPresented) {
            while !Task.isCancelled {
                await load()
                await sync.refreshStatus()
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }

    private func load() async {
        guard let client = core.client else { return }
        pending = try? await client.listPendingUploads().ok.body.json
        guard let rows = try? await client.listPairedDevices().ok.body.json else { return }
        devices = rows.filter { !$0.thisDevice }.map {
            Device(id: $0.id, name: $0.name, lastSeen: $0.lastSeen > 0 ? Date(timeIntervalSince1970: TimeInterval($0.lastSeen)) : nil, isServer: $0.isServer)
        }
    }

    private func unpair(_ device: Device) async {
        guard let client = core.client else { return }
        guard (try? await client.unpairDevice(path: .init(id: device.id)).ok) != nil else { return }
        await core.refreshSearchCredentials()
        await load()
    }
}

extension SyncManager {
    var isRunning: Bool { status?.state == .pending || status?.state == .running }
    var statusText: String {
        switch status?.state {
        case .pending: "Waiting"
        case .running: "Syncing"
        case .completed: "Current"
        case .failed: "Failed"
        case .no_peers: "No device reached"
        default: "Not synced yet"
        }
    }
}
