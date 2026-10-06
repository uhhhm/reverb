import ReverbAPI
import SwiftUI
import UIKit

typealias LibraryAlbum = Components.Schemas.LibraryAlbum
typealias LibraryArtist = Components.Schemas.LibraryArtist
typealias CatalogTrack = Components.Schemas.CatalogLibraryTrack

struct LibraryView: View {
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player
    @State private var artists: [LibraryArtist] = []
    @State private var albums: [LibraryAlbum] = []
    @State private var tracks: [LibraryTrack] = []
    @State private var catalog: [CatalogTrack] = []
    @State private var selection = 0

    var body: some View {
        List {
            HStack(spacing: 8) {
                ForEach(Array(["Artists", "Albums", "Tracks"].enumerated()), id: \.offset) { index, name in
                    FilterChip(title: name, selected: selection == index) {
                        withAnimation(.easeInOut(duration: 0.15)) { selection = index }
                    }
                    .accessibilityIdentifier("library.filter.\(name)")
                }
            }
            .darkRow()

            switch selection {
            case 0:
                ForEach(artists, id: \.id) { artist in
                    NavigationLink { ArtistView(artistID: artist.id) } label: {
                        MediaRow(title: artist.name, subtitle: "Artist · \(artist.albumCount) albums", coverArtID: artist.coverArtId, round: true)
                    }
                    .darkRow()
                }
                if !remoteArtists.isEmpty {
                    Section {
                        ForEach(remoteArtists, id: \.self) { artist in
                            NavigationLink { CatalogArtistView(artist: artist) } label: {
                                MediaRow(title: artist, subtitle: "Artist · Household library", coverArtID: remoteTracks.first { $0.artist == artist }?.coverArtId ?? "", round: true)
                            }
                            .darkRow()
                        }
                    } header: { LibrarySectionHeader(title: "On paired devices") }
                }
            case 1:
                ForEach(albums, id: \.id) { album in
                    NavigationLink { AlbumView(albumID: album.id) } label: {
                        MediaRow(title: album.name, subtitle: "Album · \(album.artist)", coverArtID: album.coverArtId)
                    }
                    .darkRow()
                }
                if !remoteAlbums.isEmpty {
                    Section {
                        ForEach(remoteAlbums, id: \.self) { key in
                            NavigationLink { CatalogAlbumView(artist: key.artist, album: key.album) } label: {
                                MediaRow(title: key.album, subtitle: "Album · \(key.artist)", coverArtID: remoteTracks.first { $0.artist == key.artist && $0.album == key.album }?.coverArtId ?? "")
                            }
                            .darkRow()
                        }
                    } header: { LibrarySectionHeader(title: "On paired devices") }
                }
            default:
                ForEach(Array(tracks.enumerated()), id: \.element.id) { index, track in
                    LibraryTrackRow(track: track)
                        .contentShape(Rectangle())
                        .onTapGesture { Task { await player.play(tracks, startAt: index) } }
                        .darkRow()
                }
                if !remoteTracks.isEmpty {
                    Section {
                        ForEach(remoteTracks, id: \.id) { track in CatalogTrackRow(track: track).darkRow() }
                    } header: { LibrarySectionHeader(title: "On paired devices") }
                }
            }
        }
        .darkList()
        .navigationTitle("Your Library")
        .refreshable {
            _ = try? await core.client?.triggerSync()
            await load()
        }
        // Incoming syncs change household catalog membership. Keep this in
        // step with Playlists so a view opened immediately after pairing does
        // not remain stale for a full minute.
        .task {
            while !Task.isCancelled {
                await load()
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }

    private func load() async {
        guard let client = core.client else { return }
        async let artistRows = try? client.listLibraryArtists().ok.body.json
        async let albumRows = try? client.listLibraryAlbums(query: .init()).ok.body.json
        async let trackRows = try? client.listLibraryTracks(query: .init()).ok.body.json
        artists = await artistRows ?? []
        albums = await albumRows ?? []
        tracks = await trackRows ?? []
        catalog = await CatalogLibrary.load(client: client)
    }

    private var remoteTracks: [CatalogTrack] { catalog.filter { $0.playback != .local } }
    private var remoteArtists: [String] { Array(Set(remoteTracks.map(\.artist))).sorted() }
    private var remoteAlbums: [CatalogAlbumKey] {
        Array(Set(remoteTracks.map { CatalogAlbumKey(artist: $0.artist, album: $0.album) }))
            .sorted { ($0.artist, $0.album) < ($1.artist, $1.album) }
    }
}

private struct LibrarySectionHeader: View {
    let title: String

    var body: some View {
        Text(title)
            .font(.headline.weight(.bold))
            .foregroundStyle(.white)
            .padding(.top, 12)
            .textCase(nil)
    }
}

private struct CatalogAlbumKey: Hashable {
    let artist: String
    let album: String
}

enum CatalogLibrary {
    static func load(client: Client, query: String? = nil) async -> [CatalogTrack] {
        var rows: [CatalogTrack] = []
        var offset = 0
        while !Task.isCancelled {
            guard let page = try? await client.listCatalogTracks(query: .init(q: query, limit: 100, offset: offset)).ok.body.json else { break }
            rows.append(contentsOf: page)
            if page.count < 100 { break }
            offset += page.count
        }
        return rows
    }
}

struct CatalogArtistView: View {
    @EnvironmentObject private var player: Player
    let artist: String
    @EnvironmentObject private var core: CoreHost
    @State private var tracks: [CatalogTrack] = []

    var body: some View {
        List {
            ArtistHeader(name: artist, coverArtID: tracks.first { $0.artist == artist }?.coverArtId) {
                Task { await player.startRadio(artist: artist) }
            }
            .listRowInsets(EdgeInsets())
            .darkRow()
            ForEach(albums, id: \.self) { album in
                NavigationLink { CatalogAlbumView(artist: artist, album: album) } label: {
                    MediaRow(title: album, subtitle: "Album · \(artist)", coverArtID: tracks.first { $0.artist == artist && $0.album == album }?.coverArtId ?? "")
                }
                .darkRow()
            }
        }
        .darkList()
        .navigationTitle(artist)
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await load() }
        .task {
            while !Task.isCancelled {
                await load()
                try? await Task.sleep(for: .seconds(60))
            }
        }
    }

    private var albums: [String] { Array(Set(tracks.filter { $0.artist == artist }.map(\.album))).sorted() }
    private func load() async {
        guard let client = core.client else { return }
        tracks = await CatalogLibrary.load(client: client, query: artist)
    }
}

struct CatalogAlbumView: View {
    @EnvironmentObject private var player: Player
    let artist: String
    let album: String
    @EnvironmentObject private var core: CoreHost
    @State private var tracks: [CatalogTrack] = []

    var body: some View {
        let albumTracks = tracks.filter { $0.artist == artist && $0.album == album }
        List {
            AlbumHeader(title: album, artist: artist, coverArtID: albumTracks.first?.coverArtId, detail: songsLabel(albumTracks.count),
                        tracks: albumTracks.filter { $0.playback != .unavailable }.map(\.playerTrack))
                .listRowInsets(EdgeInsets())
                .darkRow()
            ForEach(albumTracks, id: \.id) { track in
                CatalogTrackRow(track: track).darkRow()
            }
        }
        .darkList()
        .navigationTitle(album)
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await load() }
        .task {
            while !Task.isCancelled {
                await load()
                try? await Task.sleep(for: .seconds(60))
            }
        }
    }

    private func load() async {
        guard let client = core.client else { return }
        tracks = await CatalogLibrary.load(client: client, query: album)
    }
}

struct CatalogTrackRow: View {
    let track: CatalogTrack
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player
    @State private var playlists: [Components.Schemas.SyncedPlaylist] = []

    var body: some View {
        TrackLine(title: track.title, subtitle: track.artist, art: .init(coverArtID: track.coverArtId, seed: track.album),
                  isCurrent: player.current?.id == (track.localTrackId ?? track.id)) {
            if track.playback == .delegated { Image(systemName: "wifi").foregroundStyle(Theme.secondaryText) }
            if track.playback == .unavailable { Image(systemName: "icloud.slash").foregroundStyle(Theme.secondaryText) }
        }
        .opacity(track.playback == .unavailable ? 0.5 : 1)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("catalog.track.\(track.title)")
        .contentShape(Rectangle())
        .onTapGesture { Task { await play() } }
        .contextMenu {
            Button("Start Radio", systemImage: "dot.radiowaves.left.and.right") { Task { await player.startRadio(tracks: [track.playerTrack]) } }
            Menu("Add to playlist") {
                ForEach(playlists, id: \.id) { playlist in
                    Button(playlist.name) { Task { await add(to: playlist.id) } }
                }
            }
        }
        .task {
            guard let client = core.client else { return }
            playlists = (try? await client.listPlaylists().ok.body.json) ?? []
        }
    }

    private func play() async {
        guard track.playback != .unavailable else { return }
        await player.play([track.playerTrack], startAt: 0)
    }

    private func add(to playlistID: String) async {
        guard let client = core.client else { return }
        _ = try? await client.addPlaylistTrack(path: .init(id: playlistID), body: .json(.init(
            // A library entry's external id is a backend id where the phone
            // holds one; otherwise the catalog id is the only stable handle.
            source: "library", externalId: track.localTrackId ?? track.id, title: track.title, artist: track.artist,
            album: track.album, durationMs: track.durationMs, download: false
        ))).ok
    }
}

struct ArtistView: View {
    @EnvironmentObject private var player: Player
    let artistID: String
    @EnvironmentObject private var core: CoreHost
    @State private var artist: LibraryArtist?

    var body: some View {
        List {
            ArtistHeader(name: artist?.name ?? "", coverArtID: artist?.coverArtId) {
                Task { if let artist { await player.startRadio(artist: artist.name) } }
            }
            .listRowInsets(EdgeInsets())
            .darkRow()
            if !(artist?.albums ?? []).isEmpty {
                LibrarySectionHeader(title: "Albums").darkRow()
            }
            ForEach(artist?.albums ?? [], id: \.id) { album in
                NavigationLink { AlbumView(albumID: album.id) } label: {
                    MediaRow(title: album.name, subtitle: album.summary(leading: nil), coverArtID: album.coverArtId, size: 64)
                }
                .darkRow()
            }
        }
        .darkList()
        .navigationTitle(artist?.name ?? "Artist")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            while !Task.isCancelled {
                if let client = core.client {
                    artist = try? await client.getLibraryArtist(path: .init(id: artistID)).ok.body.json
                }
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }
}

struct AlbumView: View {
    let albumID: String
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player
    @State private var album: LibraryAlbum?

    var body: some View {
        List {
            if let album {
                AlbumHeader(title: album.name, artist: album.artist, coverArtID: album.coverArtId,
                            detail: album.summary(leading: "Album"),
                            tracks: (album.tracks ?? []).map(Player.playerTrack))
                    .listRowInsets(EdgeInsets())
                    .darkRow()
                ForEach(Array((album.tracks ?? []).enumerated()), id: \.element.id) { index, track in
                    LibraryTrackRow(track: track, showsArt: false)
                        .contentShape(Rectangle())
                        .onTapGesture { Task { await player.play(album.tracks ?? [], startAt: index) } }
                        .darkRow()
                }
            }
        }
        .darkList()
        .navigationTitle(album?.name ?? "Album")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            while !Task.isCancelled {
                if let client = core.client {
                    album = try? await client.getLibraryAlbum(path: .init(id: albumID)).ok.body.json
                }
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }
}

struct LibraryTrackRow: View {
    @EnvironmentObject private var player: Player
    let track: LibraryTrack
    var showsArt = true
    @EnvironmentObject private var core: CoreHost
    @State private var playlists: [Components.Schemas.SyncedPlaylist] = []

    var body: some View {
        TrackLine(title: track.title, subtitle: track.artist,
                  art: showsArt ? .init(coverArtID: track.coverArtId, seed: track.album) : nil,
                  isCurrent: player.current?.id == track.id)
            .contextMenu {
                Button("Start Radio", systemImage: "dot.radiowaves.left.and.right") { Task { await player.startRadio(tracks: [Player.playerTrack(track)]) } }
                Menu("Add to playlist") {
                    ForEach(playlists, id: \.id) { playlist in
                        Button(playlist.name) { Task { await add(to: playlist.id) } }
                    }
                }
            }
            .task {
                guard let client = core.client else { return }
                playlists = (try? await client.listPlaylists().ok.body.json) ?? []
            }
    }

    private func add(to playlistID: String) async {
        guard let client = core.client else { return }
        let body = Components.Schemas.AddSyncedTrackRequest(
            source: "library", externalId: track.id, title: track.title, artist: track.artist,
            album: track.album, isrc: track.isrc, durationMs: track.durationMs,
            coverArtId: track.coverArtId, download: false
        )
        _ = try? await client.addPlaylistTrack(path: .init(id: playlistID), body: .json(body)).ok
    }
}

struct MediaRow: View {
    let title: String
    let subtitle: String
    let coverArtID: String
    var size: CGFloat = 56
    /// Artists show in circles, as on Spotify.
    var round = false

    var body: some View {
        TrackLine(title: title, subtitle: subtitle,
                  art: .init(coverArtID: coverArtID, seed: title),
                  artSize: size, artCornerRadius: round ? size / 2 : 4, artSymbol: round ? "person.fill" : "music.note")
    }
}

/// An album's header: cover, title, artist, and play, shuffle and Radio.
struct AlbumHeader: View {
    let title: String
    let artist: String
    let coverArtID: String?
    let detail: String
    let tracks: [PlayerTrack]

    var body: some View {
        CollectionHeader(title: title, subtitle: artist, detail: detail, tint: Theme.color(for: title)) {
            Artwork(source: .init(coverArtID: coverArtID, seed: title))
        } actions: {
            CollectionControls(tracks: tracks)
        }
    }
}

/// Radio, shuffle and the green play button, under a collection's header.
struct CollectionControls: View {
    let tracks: [PlayerTrack]
    @EnvironmentObject private var player: Player

    private var playingHere: Bool { tracks.contains { $0.id == player.current?.id } }

    var body: some View {
        HStack(spacing: 20) {
            Button { Task { await player.startRadio(tracks: tracks) } } label: {
                Image(systemName: "dot.radiowaves.left.and.right").font(.title2).foregroundStyle(Theme.secondaryText)
            }
            .accessibilityLabel("Start Radio")
            Spacer()
            Button { Task { await player.play(tracks.shuffled(), startAt: 0) } } label: {
                Image(systemName: "shuffle").font(.title2).foregroundStyle(Theme.secondaryText)
            }
            .accessibilityLabel("Shuffle play")
            PlayCircleButton(isPlaying: playingHere && player.wantsToPlay) {
                if playingHere { player.togglePlayPause() } else { Task { await player.play(tracks, startAt: 0) } }
            }
        }
        .buttonStyle(.plain)
        .disabled(tracks.isEmpty)
    }
}

/// An artist's header: a round portrait, the name, and Radio.
struct ArtistHeader: View {
    let name: String
    let coverArtID: String?
    let startRadio: () -> Void

    var body: some View {
        VStack(spacing: 16) {
            Artwork(source: .init(coverArtID: coverArtID, seed: name), size: 200, cornerRadius: 100, symbol: "person.fill")
                .shadow(color: .black.opacity(0.5), radius: 18, y: 8)
                .padding(.top, 12)
            Text(name)
                .font(.largeTitle.weight(.heavy))
                .foregroundStyle(.white)
                .multilineTextAlignment(.center)
            HStack {
                Spacer()
                Button(action: startRadio) {
                    Label("Radio", systemImage: "dot.radiowaves.left.and.right")
                        .font(.subheadline.weight(.bold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 8)
                        .overlay(Capsule().stroke(Theme.secondaryText, lineWidth: 1))
                }
                .buttonStyle(PressableStyle())
                .accessibilityLabel("Start Radio")
                Spacer()
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.horizontal)
        .padding(.bottom, 8)
        .background(alignment: .top) {
            LinearGradient(colors: [Theme.color(for: name).opacity(0.75), Theme.background], startPoint: .top, endPoint: .bottom)
                .frame(height: 520)
                .offset(y: -200)
        }
    }
}

extension LibraryAlbum {
    /// "Album · 2019 · 12 songs", leaving out a year the tags lack.
    func summary(leading: String?) -> String {
        var parts: [String] = []
        if let leading { parts.append(leading) }
        if year > 0 { parts.append(String(year)) }
        parts.append(songsLabel(songCount))
        return parts.joined(separator: " · ")
    }
}

extension CatalogTrack {
    var playerTrack: PlayerTrack {
        PlayerTrack(
            id: localTrackId ?? id, title: title, artist: artist, album: album, durationMs: durationMs,
            cropStartMs: cropStartMs, cropEndMs: cropEndMs
        )
    }
}
