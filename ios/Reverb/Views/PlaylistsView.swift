import ReverbAPI
import SwiftUI

typealias OfflineStatus = Components.Schemas.OfflineStatus

struct PlaylistsView: View {
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var pairing: PairingModel
    @State private var playlists: [Components.Schemas.SyncedPlaylist] = []
    @State private var offline: Set<String> = []
    @State private var storage: OfflineStatus?
    @State private var loaded = false

    var body: some View {
        List {
            if let storage, !offline.isEmpty {
                Section {
                    StorageSummary(status: storage)
                }
            }
            Section {
                ForEach(playlists, id: \.id) { playlist in
                    NavigationLink(value: playlist.id) {
                        HStack {
                            VStack(alignment: .leading) {
                                Text(playlist.name)
                                Text("\(playlist.trackCount) tracks")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            if offline.contains(playlist.id) {
                                Image(systemName: "arrow.down.circle.fill")
                                    .foregroundStyle(.tint)
                                    .accessibilityLabel("Kept on this iPhone")
                            }
                        }
                    }
                    .accessibilityIdentifier("playlist.\(playlist.name)")
                }
            }
        }
        .overlay {
            if loaded && playlists.isEmpty {
                ContentUnavailableView {
                    Label("No playlists yet", systemImage: "music.note.list")
                } description: {
                    Text("Pair this iPhone with Reverb on your computer and its playlists appear here.")
                } actions: {
                    Button("Pair a device") { pairing.isPresented = true }
                }
            }
        }
        .navigationTitle("Playlists")
        .navigationDestination(for: String.self) { id in
            PlaylistView(playlistID: id)
        }
        .refreshable {
            _ = try? await core.client?.triggerSync()
            await load()
        }
        // Incoming syncs change playlists; poll while the list is on screen.
        .task {
            while !Task.isCancelled {
                await load()
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }

    private func load() async {
        guard let client = core.client else { return }
        if let list = try? await client.listPlaylists().ok.body.json {
            playlists = list.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        }
        if let entries = try? await client.listOfflineSet().ok.body.json {
            offline = Set(entries.filter(\.enabled).map(\.playlistId))
        }
        storage = try? await client.getOfflineStatus().ok.body.json
        loaded = true
    }
}

/// Said wherever the offline set's storage shows, when a track does not fit.
struct StorageFullNotice: View {
    var body: some View {
        Label("Storage is full. Copying to this iPhone has stopped; nothing was removed.", systemImage: "externaldrive.badge.exclamationmark")
            .font(.callout)
            .foregroundStyle(.orange)
            .accessibilityIdentifier("storage.full")
    }
}

struct StorageSummary: View {
    let status: OfflineStatus

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("On this iPhone: \(formatBytes(status.usedBytes))")
                .accessibilityIdentifier("storage.used")
            if status.full {
                StorageFullNotice()
            } else if status.spaceUnknown {
                Text("Copying to this iPhone is paused: its free space cannot be read.")
                    .font(.caption)
                    .foregroundStyle(.orange)
            } else {
                Text("\(formatBytes(status.availableBytes)) free for offline playlists")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }
}
