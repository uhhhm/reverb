import ReverbAPI
import SwiftUI

typealias PlaylistTrack = Components.Schemas.PlaylistTrack
typealias OfflineTrackStatus = Components.Schemas.OfflineTrackStatus

struct PlaylistView: View {
    let playlistID: String

    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player
    @State private var detail: Components.Schemas.SyncedPlaylistDetail?
    @State private var isOffline = false
    @State private var offlineStatus: Components.Schemas.OfflinePlaylistStatus?
    @State private var storageFull = false
    @State private var error: String?

    private var tracks: [PlaylistTrack] { detail?.value2.tracks ?? [] }

    private var playable: [PlayerTrack] { tracks.compactMap(Self.playerTrack) }

    var body: some View {
        List {
            Section {
                Toggle("Keep on this iPhone", isOn: Binding(get: { isOffline }, set: { on in
                    Task { await setOffline(on) }
                }))
                .accessibilityIdentifier("playlist.offlineToggle")
                if isOffline, let status = offlineStatus {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("\(status.readyCount) of \(status.trackCount) on this iPhone · \(formatBytes(status.bytes))")
                            .font(.callout)
                            .accessibilityIdentifier("playlist.offlineProgress")
                        if storageFull {
                            StorageFullNotice()
                        }
                    }
                }
            } footer: {
                Text("Offline playlists play with no connection. Each device chooses its own.")
            }
            Section {
                ForEach(Array(tracks.enumerated()), id: \.offset) { index, track in
                    TrackRow(track: track, offline: isOffline ? trackStatus(at: index, track) : nil)
                        .contentShape(Rectangle())
                        .onTapGesture { Task { await play(track) } }
                        .disabled(track.playback == .unavailable)
                        .swipeActions {
                            Button("Remove", role: .destructive) { Task { await remove(track) } }
                        }
                }
                .onMove { from, to in Task { await reorder(from: from, to: to) } }
            }
            if let error {
                Text(error).foregroundStyle(.red)
            }
        }
        .navigationTitle(detail?.value1.name ?? "Playlist")
        .toolbar { EditButton() }
        .task {
            while !Task.isCancelled {
                await load()
                // Faster while files are arriving, so progress moves.
                let fetching = offlineStatus.map { $0.readyCount < $0.trackCount } ?? false
                try? await Task.sleep(for: .seconds(isOffline && fetching ? 1 : 5))
            }
        }
    }

    private func trackStatus(at index: Int, _ track: PlaylistTrack) -> OfflineTrackStatus? {
        guard let rows = offlineStatus?.tracks else { return nil }
        if rows.count == tracks.count { return rows[index] }
        return rows.first { $0.title == track.title && $0.artist == track.artist }
    }

    private func load() async {
        guard let client = core.client else { return }
        do {
            detail = try await client.getPlaylist(path: .init(id: playlistID)).ok.body.json
            error = nil
        } catch {
            self.error = "Could not load the playlist."
        }
        if let entries = try? await client.listOfflineSet().ok.body.json {
            isOffline = entries.contains { $0.playlistId == playlistID && $0.enabled }
        }
        if let status = try? await client.getOfflineStatus().ok.body.json {
            offlineStatus = status.playlists.first { $0.playlistId == playlistID }
            storageFull = status.full
        }
    }

    private func setOffline(_ on: Bool) async {
        guard let client = core.client else { return }
        isOffline = on
        do {
            if on {
                _ = try await client.setPlaylistOffline(path: .init(playlistId: playlistID), body: .json(.init(enabled: true))).ok
            } else {
                _ = try await client.removePlaylistOffline(path: .init(playlistId: playlistID)).ok
            }
        } catch {
            self.error = "Could not change the offline setting."
        }
        await load()
    }

    private func play(_ track: PlaylistTrack) async {
        guard let chosen = Self.playerTrack(track),
              let start = playable.firstIndex(where: { $0.id == chosen.id }) else { return }
        await player.play(playable, startAt: start)
    }

    private func remove(_ track: PlaylistTrack) async {
        guard let client = core.client, let key = track.key else { return }
        _ = try? await client.removePlaylistTrack(path: .init(id: playlistID), query: .init(source: key.source, externalId: key.externalId)).ok
        await load()
    }

    private func reorder(from: IndexSet, to: Int) async {
        guard let client = core.client else { return }
        var reordered = tracks
        reordered.move(fromOffsets: from, toOffset: to)
        let keys = reordered.compactMap(\.key)
        guard keys.count == reordered.count else { return }
        _ = try? await client.reorderPlaylistTracks(path: .init(id: playlistID), body: .json(.init(tracks: keys))).ok
        await load()
    }

    static func playerTrack(_ track: PlaylistTrack) -> PlayerTrack? {
        if let local = track.libraryTrack { return Player.playerTrack(local) }
        guard track.playback == .delegated, let id = track.canonicalId else { return nil }
        let extra: [String: (any Sendable)?] = ["coverUrl": track.coverUrl]
        return PlayerTrack(
            id: id, title: track.title, artist: track.artist, album: track.album,
            durationMs: track.durationMs, cropStartMs: track.cropStartMs, cropEndMs: track.cropEndMs,
            additionalProperties: (try? OpenAPIObjectContainer(unvalidatedValue: extra)) ?? .init()
        )
    }
}

struct TrackRow: View {
    let track: PlaylistTrack
    let offline: OfflineTrackStatus?

    /// A file counts as on this iPhone once it lands, but it plays only after
    /// the library's next scan indexes it; until then it still shows as copying.
    private var shownOffline: OfflineTrackStatus? {
        guard var status = offline, status.state == .ready, track.playback != .local else { return offline }
        status.state = .fetching
        return status
    }

    var body: some View {
        HStack {
            VStack(alignment: .leading) {
                Text(track.title)
                Text(track.artist)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if let offline = shownOffline {
                OfflineBadge(status: offline)
            } else if track.playback == .delegated {
                Image(systemName: "wifi")
                    .foregroundStyle(.secondary)
                    .accessibilityLabel("Streams from a paired device")
            } else if track.playback == .unavailable {
                Image(systemName: "icloud.slash")
                    .foregroundStyle(.secondary)
                    .accessibilityLabel("Not on this iPhone")
            }
        }
        // Tracks that cannot play now are marked, not hidden.
        .opacity(track.playback == .unavailable ? 0.45 : 1)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("track.\(track.title)")
        .accessibilityValue(shownOffline?.state.rawValue ?? track.playback.rawValue)
    }
}

struct OfflineBadge: View {
    let status: OfflineTrackStatus

    var body: some View {
        switch status.state {
        case .ready:
            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                .accessibilityLabel("On this iPhone")
        case .fetching:
            if status.sizeBytes > 0 {
                ProgressView(value: Double(status.fetchedBytes), total: Double(status.sizeBytes))
                    .frame(width: 48)
                    .accessibilityLabel("Copying to this iPhone")
            } else {
                ProgressView().accessibilityLabel("Copying to this iPhone")
            }
        case .queued:
            Image(systemName: "arrow.down.circle").foregroundStyle(.secondary)
                .accessibilityLabel("Waiting to copy")
        case .noSpace:
            Image(systemName: "externaldrive.badge.exclamationmark").foregroundStyle(.orange)
                .accessibilityLabel("Does not fit")
        case .unavailable:
            Image(systemName: "icloud.slash").foregroundStyle(.secondary)
                .accessibilityLabel("No paired device has it")
        }
    }
}
