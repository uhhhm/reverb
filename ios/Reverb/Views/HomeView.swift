import ReverbAPI
import SwiftUI

typealias RecommendedTrack = Components.Schemas.RecommendedTrack

struct HomeView: View {
    @EnvironmentObject private var core: CoreHost
    @State private var shelves: Components.Schemas.HomeShelves?
    @State private var mixes: [Components.Schemas.Mix] = []
    @State private var playlists: [Components.Schemas.SyncedPlaylist] = []

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 28) {
                if shelves?.offline == true || mixes.contains(where: { $0.offline == true }) {
                    Label("Offline — showing the last recommendations saved on this iPhone.", systemImage: "wifi.slash")
                        .font(.footnote).foregroundStyle(Theme.secondaryText)
                        .accessibilityIdentifier("home.offline")
                }
                if !mixes.isEmpty || !playlists.isEmpty {
                    quickPicks
                }
                if !mixes.isEmpty {
                    VStack(alignment: .leading, spacing: 12) {
                        ShelfHeader(title: "Made for you")
                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack(alignment: .top, spacing: 14) {
                                ForEach(mixes, id: \.kind.rawValue) { mix in
                                    NavigationLink { MixView(kind: mix.kind) } label: { MixCard(mix: mix) }
                                        .buttonStyle(PressableStyle())
                                }
                            }
                        }
                        .scrollClipDisabled()
                    }
                }
                ForEach(Array((shelves?.shelves ?? []).enumerated()), id: \.offset) { _, shelf in
                    VStack(alignment: .leading, spacing: 12) {
                        ShelfHeader(title: shelf.title)
                        ScrollView(.horizontal, showsIndicators: false) {
                            LazyHStack(alignment: .top, spacing: 14) {
                                ForEach(shelf.tracks, id: \.externalId) { track in
                                    RecommendedTrackCard(track: track, queue: shelf.tracks) { await load() }
                                }
                                ForEach(shelf.artists, id: \.externalId) { artist in
                                    ArtistCard(artist: artist) { await markArtist(artist) }
                                }
                            }
                        }
                        .scrollClipDisabled()
                    }
                }
            }
            .padding(.horizontal)
            .padding(.bottom, 24)
        }
        .background(Theme.background)
        .navigationTitle(greeting())
        .refreshable { await load() }
        .task { await load() }
    }

    /// The two-column grid of shortcuts at the top, as Spotify's Home opens with.
    private var quickPicks: some View {
        LazyVGrid(columns: [GridItem(.flexible(), spacing: 8), GridItem(.flexible(), spacing: 8)], spacing: 8) {
            ForEach(mixes, id: \.kind.rawValue) { mix in
                NavigationLink { MixView(kind: mix.kind) } label: {
                    QuickPickTile(title: mix.kind.title, art: .init(seed: mix.kind.title), symbol: "sparkles")
                }
            }
            ForEach(playlists.prefix(max(0, 8 - mixes.count)), id: \.id) { playlist in
                NavigationLink { PlaylistView(playlistID: playlist.id) } label: {
                    QuickPickTile(title: playlist.name, art: .init(coverURL: playlist.coverUrl, seed: playlist.name), symbol: "music.note.list")
                }
            }
        }
        .buttonStyle(PressableStyle())
    }

    private func load() async {
        guard let client = core.client else { return }
        async let home = try? client.getHomeShelves().ok.body.json
        async let mixList = try? client.listMixes().ok.body.json
        async let playlistList = try? client.listPlaylists().ok.body.json
        shelves = await home
        mixes = await mixList?.mixes ?? []
        // Recently synced first, as the shortcuts are what was last in use.
        playlists = (await playlistList ?? []).sorted { $0.lastSyncedAt > $1.lastSyncedAt }
    }

    private func markArtist(_ artist: Components.Schemas.ExternalArtist) async {
        guard let client = core.client else { return }
        if (try? await client.markNotInterested(body: .json(.init(
            kind: .artist, source: artist.source, id: artist.externalId, name: artist.name
        ))).ok) != nil { await load() }
    }
}

private struct QuickPickTile: View {
    let title: String
    let art: ArtworkSource
    let symbol: String

    var body: some View {
        HStack(spacing: 8) {
            Artwork(source: art, size: 56, cornerRadius: 0, symbol: symbol)
            Text(title)
                .font(.footnote.weight(.bold))
                .foregroundStyle(.white)
                .lineLimit(2)
                .multilineTextAlignment(.leading)
            Spacer(minLength: 0)
        }
        .background(Theme.elevated)
        .clipShape(RoundedRectangle(cornerRadius: 4))
    }
}

private struct MixCard: View {
    let mix: Components.Schemas.Mix

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ZStack(alignment: .bottomLeading) {
                Artwork(source: .init(seed: mix.kind.title), size: 150, cornerRadius: 6, symbol: "sparkles")
                Text(mix.kind.title)
                    .font(.headline.weight(.heavy))
                    .foregroundStyle(.white)
                    .shadow(radius: 4)
                    .padding(10)
            }
            Text(mix.tracks.prefix(3).map(\.artist).joined(separator: ", "))
                .font(.caption)
                .foregroundStyle(Theme.secondaryText)
                .lineLimit(2)
                .multilineTextAlignment(.leading)
                .frame(width: 150, alignment: .leading)
        }
    }
}

/// A recommended track as a card in a Home shelf.
struct RecommendedTrackCard: View {
    let track: RecommendedTrack
    var queue: [RecommendedTrack] = []
    var onMarked: (() async -> Void)? = nil
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player

    var body: some View {
        Button { Task { await track.play(in: queue, player: player) } } label: {
            VStack(alignment: .leading, spacing: 6) {
                Artwork(source: .init(coverArtID: track.coverArtId, coverURL: track.coverUrl, seed: track.album), size: 140)
                Text(track.title)
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(nowPlaying ? Theme.green : .white)
                    .lineLimit(1)
                Text(track.artist)
                    .font(.caption)
                    .foregroundStyle(Theme.secondaryText)
                    .lineLimit(1)
            }
            .frame(width: 140, alignment: .leading)
        }
        .buttonStyle(PressableStyle())
        .modifier(RecommendedTrackMenu(track: track, onMarked: onMarked))
    }

    private var nowPlaying: Bool { player.current?.title == track.title && player.current?.artist == track.artist }
}

private struct ArtistCard: View {
    let artist: Components.Schemas.ExternalArtist
    let onNotInterested: () async -> Void
    @EnvironmentObject private var player: Player

    var body: some View {
        Button { Task { await player.startRadio(artist: artist.name) } } label: {
            VStack(spacing: 8) {
                Artwork(source: .init(coverArtID: artist.coverArtId, coverURL: artist.coverUrl, seed: artist.name),
                        size: 140, cornerRadius: 70, symbol: "person.fill")
                Text(artist.name)
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.white)
                    .lineLimit(1)
                Text("Artist")
                    .font(.caption)
                    .foregroundStyle(Theme.secondaryText)
            }
            .frame(width: 140)
        }
        .buttonStyle(PressableStyle())
        .contextMenu {
            Button("Start Radio", systemImage: "dot.radiowaves.left.and.right") { Task { await player.startRadio(artist: artist.name) } }
            Button("Not interested", systemImage: "hand.thumbsdown", role: .destructive) { Task { await onNotInterested() } }
        }
    }
}

struct MixView: View {
    let kind: Components.Schemas.MixKind
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player
    @State private var mix: Components.Schemas.Mix?
    @State private var saved = false

    private var tracks: [RecommendedTrack] { mix?.tracks ?? [] }

    var body: some View {
        List {
            CollectionHeader(
                title: kind.title, subtitle: "Made for you",
                detail: mix?.offline == true ? "Offline — showing the last saved Mix." : songsLabel(tracks.count),
                tint: Theme.color(for: kind.title)
            ) {
                Artwork(source: .init(seed: kind.title), cornerRadius: 4, symbol: "sparkles")
            } actions: {
                HStack(spacing: 20) {
                    Button { Task { await save() } } label: {
                        Image(systemName: saved ? "checkmark.circle.fill" : "plus.circle")
                            .font(.title2)
                            .foregroundStyle(saved ? Theme.green : Theme.secondaryText)
                    }
                    .disabled(saved || tracks.isEmpty)
                    .accessibilityLabel(saved ? "Saved" : "Save as playlist")
                    Spacer()
                    Button { Task { await player.play(tracks.shuffled().map(\.playerTrack), startAt: 0) } } label: {
                        Image(systemName: "shuffle").font(.title2).foregroundStyle(Theme.secondaryText)
                    }
                    .accessibilityLabel("Shuffle play")
                    .disabled(tracks.isEmpty)
                    PlayCircleButton { Task { await player.play(tracks.map(\.playerTrack), startAt: 0) } }
                        .disabled(tracks.isEmpty)
                }
                .buttonStyle(.plain)
            }
            .listRowInsets(EdgeInsets())
            .darkRow()
            ForEach(tracks, id: \.externalId) { track in
                RecommendedTrackRow(track: track, queue: tracks) { await load() }
                    .darkRow()
            }
        }
        .darkList()
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
    }

    private func load() async {
        guard let client = core.client else { return }
        mix = try? await client.getMix(path: .init(kind: kind)).ok.body.json
    }

    private func save() async {
        guard let client = core.client else { return }
        if (try? await client.saveMixAsPlaylist(path: .init(kind: kind), body: .json(.init())).created.body.json) != nil {
            saved = true
        }
    }
}

struct RecommendedTrackRow: View {
    let track: RecommendedTrack
    /// The shelf or Mix the track belongs to; playing a track plays on
    /// through the rest of it.
    var queue: [RecommendedTrack] = []
    var onMarked: (() async -> Void)? = nil
    @EnvironmentObject private var player: Player

    var body: some View {
        TrackLine(
            title: track.title, subtitle: track.artist,
            art: .init(coverArtID: track.coverArtId, coverURL: track.coverUrl, seed: track.album),
            isCurrent: player.current?.title == track.title && player.current?.artist == track.artist
        ) {
            RecommendedTrackMenuButton(track: track, onMarked: onMarked)
        }
        .contentShape(Rectangle())
        .onTapGesture { Task { await track.play(in: queue, player: player) } }
        .modifier(RecommendedTrackMenu(track: track, onMarked: onMarked))
    }
}

/// Radio, Download and Not interested, for a long press on a recommendation.
private struct RecommendedTrackMenu: ViewModifier {
    let track: RecommendedTrack
    var onMarked: (() async -> Void)?
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player

    func body(content: Content) -> some View {
        content.contextMenu { RecommendedTrackActions(track: track, onMarked: onMarked) }
    }
}

/// The same actions behind a row's "…" button.
private struct RecommendedTrackMenuButton: View {
    let track: RecommendedTrack
    var onMarked: (() async -> Void)?

    var body: some View {
        Menu { RecommendedTrackActions(track: track, onMarked: onMarked) } label: { MoreGlyph() }
            .accessibilityLabel("More")
    }
}

private struct RecommendedTrackActions: View {
    let track: RecommendedTrack
    var onMarked: (() async -> Void)?
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player

    var body: some View {
        Button("Start Radio", systemImage: "dot.radiowaves.left.and.right") { Task { await player.startRadio(tracks: [track.playerTrack]) } }
        if track.source != "library" && track.match?.status != .in_library {
            Button("Download", systemImage: "arrow.down.circle") {
                Task {
                    await Downloads.enqueue(core: core, source: track.source, externalId: track.externalId,
                                            title: track.title, artist: track.artist, album: track.album, isrc: track.isrc)
                }
            }
        }
        Button("Not interested", systemImage: "hand.thumbsdown", role: .destructive) { Task { await mark() } }
    }

    private func mark() async {
        guard let client = core.client else { return }
        if (try? await client.markNotInterested(body: .json(.init(
            kind: .track, source: track.source, externalId: track.externalId,
            trackId: track.source == "library" ? track.externalId : nil,
            title: track.title, artist: track.artist, album: track.album, durationMs: track.durationMs
        ))).ok) != nil {
            await onMarked?()
        }
    }
}

extension RecommendedTrack {
    /// Plays this track and on through the rest of its shelf or Mix.
    @MainActor
    func play(in queue: [RecommendedTrack], player: Player) async {
        let tracks = queue.isEmpty ? [self] : queue
        let start = tracks.firstIndex { $0.source == source && $0.externalId == externalId } ?? 0
        await player.play(tracks.map(\.playerTrack), startAt: start)
    }

    /// A library track plays from the library; anything else streams from its
    /// source through the core's yt-dlp.
    var playerTrack: PlayerTrack {
        let libraryId = source == "library" ? externalId : (match?.status == .in_library ? match?.libraryTrackId : nil)
        return Player.resolvedTrack(
            source: source, externalId: externalId, title: title, artist: artist, album: album,
            durationMs: durationMs, libraryId: libraryId, coverArtId: coverArtId, coverUrl: coverUrl
        )
    }
}

private extension Components.Schemas.MixKind {
    var title: String { self == .discoverWeekly ? "Discover Weekly" : "Release Radar" }
}

private extension Components.Schemas.Shelf {
    var title: String {
        switch kind {
        case .becauseYouPlayed: "Because you played \(seed?.title ?? seed?.artist ?? "music")"
        case .similarTo: "Similar to \(seed?.title ?? seed?.artist ?? "your library")"
        case .artistsYouMightLike: "Artists you might like"
        case .moreFromArtistsYouLove: "More from artists you love"
        }
    }
}
