import ReverbAPI
import SwiftUI

struct DevicesView: View {
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var pairing: PairingModel
    @State private var devices: [Device] = []

    struct Device: Identifiable {
        let id: String
        let name: String
        let lastSeen: Date?
    }

    var body: some View {
        List {
            Section {
                ForEach(devices) { device in
                    VStack(alignment: .leading) {
                        Text(device.name)
                        Text(device.lastSeen.map { "Last reached \($0.formatted(.relative(presentation: .named)))" } ?? "Not reached yet")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    .accessibilityIdentifier("device.\(device.name)")
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
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }

    private func load() async {
        guard let client = core.client,
              let rows = try? await client.listPairedDevices().ok.body.json else { return }
        devices = rows.filter { !$0.thisDevice }.map {
            Device(id: $0.id, name: $0.name, lastSeen: $0.lastSeen > 0 ? Date(timeIntervalSince1970: TimeInterval($0.lastSeen)) : nil)
        }
    }
}
