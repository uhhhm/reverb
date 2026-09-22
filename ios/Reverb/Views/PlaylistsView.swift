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
    @State private var creating = false
    @State private var renaming: Components.Schemas.SyncedPlaylist?
    @State private var draftName = ""

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
                    .swipeActions {
                        Button("Delete", role: .destructive) { Task { await delete(playlist) } }
                        Button("Rename") {
                            draftName = playlist.name
                            renaming = playlist
                        }.tint(.blue)
                    }
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
        .toolbar {
            Button { draftName = ""; creating = true } label: { Image(systemName: "plus") }
                .accessibilityLabel("New playlist")
        }
        .alert("New playlist", isPresented: $creating) {
            TextField("Name", text: $draftName)
            Button("Create") { Task { await create() } }
            Button("Cancel", role: .cancel) {}
        }
        .alert("Rename playlist", isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
            TextField("Name", text: $draftName)
            Button("Rename") { Task { await rename() } }
            Button("Cancel", role: .cancel) { renaming = nil }
        }
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

    private func create() async {
        guard let client = core.client, !draftName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        _ = try? await client.createPlaylist(body: .json(.init(name: draftName))).created
        await load()
    }

    private func rename() async {
        guard let playlist = renaming, let client = core.client else { return }
        _ = try? await client.renamePlaylist(path: .init(id: playlist.id), body: .json(.init(name: draftName))).ok
        renaming = nil
        await load()
    }

    private func delete(_ playlist: Components.Schemas.SyncedPlaylist) async {
        guard let client = core.client else { return }
        _ = try? await client.deletePlaylist(path: .init(id: playlist.id)).ok
        await load()
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
