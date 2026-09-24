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
            Picker("Browse", selection: $selection) {
                Text("Artists").tag(0)
                Text("Albums").tag(1)
                Text("Tracks").tag(2)
            }.pickerStyle(.segmented)

            switch selection {
            case 0:
                ForEach(artists, id: \.id) { artist in
                    NavigationLink { ArtistView(artistID: artist.id) } label: {
                        MediaRow(title: artist.name, subtitle: "\(artist.albumCount) albums", coverArtID: artist.coverArtId)
                    }
                }
                if !remoteArtists.isEmpty {
                    Section("On paired devices") {
                        ForEach(remoteArtists, id: \.self) { artist in
                            NavigationLink { CatalogArtistView(artist: artist) } label: {
                                MediaRow(title: artist, subtitle: "Household library", coverArtID: remoteTracks.first { $0.artist == artist }?.coverArtId ?? "")
                            }
                        }
                    }
                }
            case 1:
                ForEach(albums, id: \.id) { album in
                    NavigationLink { AlbumView(albumID: album.id) } label: {
                        MediaRow(title: album.name, subtitle: album.artist, coverArtID: album.coverArtId)
                    }
                }
                if !remoteAlbums.isEmpty {
                    Section("On paired devices") {
                        ForEach(remoteAlbums, id: \.self) { key in
                            NavigationLink { CatalogAlbumView(artist: key.artist, album: key.album) } label: {
                                MediaRow(title: key.album, subtitle: key.artist, coverArtID: remoteTracks.first { $0.artist == key.artist && $0.album == key.album }?.coverArtId ?? "")
                            }
                        }
                    }
                }
            default:
                ForEach(Array(tracks.enumerated()), id: \.element.id) { index, track in
                    LibraryTrackRow(track: track)
                        .contentShape(Rectangle())
                        .onTapGesture { Task { await player.play(tracks, startAt: index) } }
                }
                if !remoteTracks.isEmpty {
                    Section("On paired devices") {
                        ForEach(remoteTracks, id: \.id) { track in CatalogTrackRow(track: track) }
                    }
                }
            }
        }
        .navigationTitle("Library")
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
    let artist: String
    @EnvironmentObject private var core: CoreHost
    @State private var tracks: [CatalogTrack] = []

    var body: some View {
        List {
            ForEach(albums, id: \.self) { album in
                NavigationLink { CatalogAlbumView(artist: artist, album: album) } label: {
                    MediaRow(title: album, subtitle: artist, coverArtID: tracks.first { $0.artist == artist && $0.album == album }?.coverArtId ?? "")
                }
            }
        }
        .navigationTitle(artist)
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
    let artist: String
    let album: String
    @EnvironmentObject private var core: CoreHost
    @State private var tracks: [CatalogTrack] = []

    var body: some View {
        List {
            if let artwork = tracks.first(where: { $0.artist == artist && $0.album == album })?.coverArtId {
                MediaRow(title: album, subtitle: artist, coverArtID: artwork, size: 72)
            }
            ForEach(tracks.filter { $0.artist == artist && $0.album == album }, id: \.id) { track in
                CatalogTrackRow(track: track)
            }
        }
        .navigationTitle(album)
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
        HStack {
            MediaRow(title: track.title, subtitle: track.artist, coverArtID: track.coverArtId ?? "")
            Spacer()
            if track.playback == .delegated { Image(systemName: "wifi").foregroundStyle(.secondary) }
            if track.playback == .unavailable { Image(systemName: "icloud.slash").foregroundStyle(.secondary) }
        }
        .opacity(track.playback == .unavailable ? 0.5 : 1)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("catalog.track.\(track.title)")
        .contentShape(Rectangle())
        .onTapGesture { Task { await play() } }
        .contextMenu {
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
        let item = PlayerTrack(
            id: track.localTrackId ?? track.id, title: track.title, artist: track.artist,
            album: track.album, durationMs: track.durationMs,
            cropStartMs: track.cropStartMs, cropEndMs: track.cropEndMs
        )
        await player.play([item], startAt: 0)
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
    let artistID: String
    @EnvironmentObject private var core: CoreHost
    @State private var artist: LibraryArtist?

    var body: some View {
        List(artist?.albums ?? [], id: \.id) { album in
            NavigationLink { AlbumView(albumID: album.id) } label: {
                MediaRow(title: album.name, subtitle: "\(album.songCount) tracks", coverArtID: album.coverArtId)
            }
        }
        .navigationTitle(artist?.name ?? "Artist")
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
                MediaRow(title: album.name, subtitle: album.artist, coverArtID: album.coverArtId, size: 72)
                ForEach(Array((album.tracks ?? []).enumerated()), id: \.element.id) { index, track in
                    LibraryTrackRow(track: track)
                        .contentShape(Rectangle())
                        .onTapGesture { Task { await player.play(album.tracks ?? [], startAt: index) } }
                }
            }
        }
        .navigationTitle(album?.name ?? "Album")
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
    let track: LibraryTrack
    @EnvironmentObject private var core: CoreHost
    @State private var playlists: [Components.Schemas.SyncedPlaylist] = []

    var body: some View {
        MediaRow(title: track.title, subtitle: track.artist, coverArtID: track.coverArtId)
            .contextMenu {
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
    @EnvironmentObject private var core: CoreHost
    let title: String
    let subtitle: String
    let coverArtID: String
    var size: CGFloat = 44

    var body: some View {
        HStack(spacing: 12) {
            Group {
                if let local = core.core, let url = local.coverURL(id: coverArtID) {
                    CoverImage(request: local.request(url))
                } else {
                    Color.secondary.opacity(0.15).overlay { Image(systemName: "music.note") }
                }
            }
            .frame(width: size, height: size).clipShape(RoundedRectangle(cornerRadius: 6))
            VStack(alignment: .leading) {
                Text(title)
                Text(subtitle).font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}

/// Cover art from the core. AsyncImage cannot send the launch secret, so this
/// loads the request itself.
struct CoverImage: View {
    let request: URLRequest
    @State private var image: UIImage?

    var body: some View {
        Group {
            if let image {
                Image(uiImage: image).resizable().scaledToFill()
            } else {
                Color.secondary.opacity(0.15)
            }
        }
        .task(id: request.url) {
            guard let (data, _) = try? await URLSession.shared.data(for: request) else { return }
            image = UIImage(data: data)
        }
    }
}
