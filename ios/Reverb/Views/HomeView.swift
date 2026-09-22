import ReverbAPI
import SwiftUI

typealias RecommendedTrack = Components.Schemas.RecommendedTrack

struct HomeView: View {
    @EnvironmentObject private var core: CoreHost
    @State private var shelves: Components.Schemas.HomeShelves?
    @State private var mixes: [Components.Schemas.Mix] = []

    var body: some View {
        List {
            if shelves?.offline == true || mixes.contains(where: { $0.offline == true }) {
                Label("Offline — showing the last recommendations saved on this iPhone.", systemImage: "wifi.slash")
                    .font(.callout).foregroundStyle(.secondary)
                    .accessibilityIdentifier("home.offline")
            }
            if !mixes.isEmpty {
                Section("Mixes") {
                    ForEach(mixes, id: \.kind.rawValue) { mix in
                        NavigationLink { MixView(kind: mix.kind) } label: {
                            Label(mix.kind.title, systemImage: "sparkles")
                        }
                    }
                }
            }
            ForEach(Array((shelves?.shelves ?? []).enumerated()), id: \.offset) { _, shelf in
                Section(shelf.title) {
                    ForEach(shelf.tracks, id: \.externalId) { track in
                        RecommendedTrackRow(track: track) { await load() }
                    }
                    ForEach(shelf.artists, id: \.externalId) { artist in
                        HStack {
                            VStack(alignment: .leading) {
                                Text(artist.name)
                                Text(artist.source.capitalized).font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer()
                            Button { Task { await markArtist(artist) } } label: { Image(systemName: "hand.thumbsdown") }
                                .buttonStyle(.borderless).accessibilityLabel("Not interested")
                        }
                    }
                }
            }
            Section {
                NavigationLink("Playlists") { PlaylistsView() }
            }
        }
        .navigationTitle("Home")
        .refreshable { await load() }
        .task { await load() }
    }

    private func load() async {
        guard let client = core.client else { return }
        async let home = try? client.getHomeShelves().ok.body.json
        async let mixList = try? client.listMixes().ok.body.json
        shelves = await home
        mixes = await mixList?.mixes ?? []
    }

    private func markArtist(_ artist: Components.Schemas.ExternalArtist) async {
        guard let base = core.core?.apiURL else { return }
        await LoopbackAPI.notInterested(base: base, body: .init(kind: "artist", source: artist.source, id: artist.externalId, name: artist.name))
        await load()
    }
}

struct MixView: View {
    let kind: Components.Schemas.MixKind
    @EnvironmentObject private var core: CoreHost
    @State private var mix: Components.Schemas.Mix?
    @State private var saved = false

    var body: some View {
        List {
            if mix?.offline == true {
                Label("Offline — showing the last saved Mix.", systemImage: "wifi.slash").foregroundStyle(.secondary)
            }
            ForEach(mix?.tracks ?? [], id: \.externalId) { track in RecommendedTrackRow(track: track) { await load() } }
        }
        .navigationTitle(kind.title)
        .toolbar {
            Button(saved ? "Saved" : "Save as playlist") { Task { await save() } }.disabled(saved || mix?.tracks.isEmpty != false)
        }
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
    var onMarked: (() async -> Void)? = nil
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player

    var body: some View {
        HStack {
            VStack(alignment: .leading) {
                Text(track.title)
                Text(track.artist).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if playableID == nil { Image(systemName: "icloud.slash").foregroundStyle(.secondary) }
            Button { Task { await mark() } } label: { Image(systemName: "hand.thumbsdown") }
                .buttonStyle(.borderless).accessibilityLabel("Not interested")
        }
        .contentShape(Rectangle())
        .onTapGesture { Task { await play() } }
        .opacity(playableID == nil ? 0.55 : 1)
    }

    private var playableID: String? {
        if track.source == "library" { return track.externalId }
        if track.match?.status == .in_library { return track.match?.libraryTrackId }
        return track.canonicalId
    }

    private func play() async {
        guard let id = playableID else { return }
        let extra: [String: (any Sendable)?] = ["coverArtId": track.coverArtId]
        let item = PlayerTrack(
            id: id, title: track.title, artist: track.artist, album: track.album, durationMs: track.durationMs,
            additionalProperties: (try? OpenAPIObjectContainer(unvalidatedValue: extra)) ?? .init()
        )
        await player.play([item], startAt: 0)
    }

    private func mark() async {
        guard let base = core.core?.apiURL else { return }
        if await LoopbackAPI.notInterested(base: base, body: .init(
            kind: "track", source: track.source, externalId: track.externalId,
            trackId: track.source == "library" ? track.externalId : nil,
            title: track.title, artist: track.artist, album: track.album, durationMs: track.durationMs
        )) {
            await onMarked?()
        }
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
