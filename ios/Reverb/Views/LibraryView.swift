import ReverbAPI
import SwiftUI

typealias LibraryAlbum = Components.Schemas.LibraryAlbum
typealias LibraryArtist = Components.Schemas.LibraryArtist

struct LibraryView: View {
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player
    @State private var artists: [LibraryArtist] = []
    @State private var albums: [LibraryAlbum] = []
    @State private var tracks: [LibraryTrack] = []
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
            case 1:
                ForEach(albums, id: \.id) { album in
                    NavigationLink { AlbumView(albumID: album.id) } label: {
                        MediaRow(title: album.name, subtitle: album.artist, coverArtID: album.coverArtId)
                    }
                }
            default:
                ForEach(Array(tracks.enumerated()), id: \.element.id) { index, track in
                    LibraryTrackRow(track: track)
                        .contentShape(Rectangle())
                        .onTapGesture { Task { await player.play(tracks, startAt: index) } }
                }
            }
        }
        .navigationTitle("Library")
        .refreshable { await load() }
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
                if !coverArtID.isEmpty, let url = core.core?.coverURL(id: coverArtID) {
                    AsyncImage(url: url) { image in image.resizable().scaledToFill() } placeholder: { Color.secondary.opacity(0.15) }
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
