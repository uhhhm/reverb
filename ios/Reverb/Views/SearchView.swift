import Foundation
import ReverbAPI
import SwiftUI

struct SearchEnvelope: Decodable, Identifiable {
    let source: String
    let status: String
    let results: [SearchResult]
    let error: String?
    var id: String { source }
}

struct SearchResult: Decodable, Identifiable {
    struct Match: Decodable { let status: String; let libraryTrackId: String; let coverArtId: String? }
    let source: String
    let externalId: String
    let title: String
    let artist: String
    let album: String
    let durationMs: Int
    let coverUrl: String?
    let coverArtId: String?
    let match: Match?
    var id: String { source + ":" + externalId }

    /// A library match plays from the library; anything else streams from
    /// its source through the core's yt-dlp.
    var playerTrack: PlayerTrack {
        guard let match, match.status == "in_library" else {
            return Player.externalTrack(
                source: source, externalId: externalId, title: title, artist: artist, album: album,
                durationMs: durationMs, coverUrl: coverUrl
            )
        }
        let extra: [String: (any Sendable)?] = ["coverArtId": match.coverArtId ?? coverArtId]
        return PlayerTrack(
            id: match.libraryTrackId, title: title, artist: artist, album: album, durationMs: durationMs,
            additionalProperties: (try? OpenAPIObjectContainer(unvalidatedValue: extra)) ?? .init()
        )
    }
}

struct SearchView: View {
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player
    @State private var query = ""
    @State private var local: Components.Schemas.LibrarySearchResults?
    @State private var catalog: [CatalogTrack] = []
    @State private var sources: [SearchEnvelope] = []

    var body: some View {
        List {
            if let local, !local.tracks.isEmpty {
                Section("Your library") {
                    ForEach(Array(local.tracks.enumerated()), id: \.element.id) { index, track in
                        LibraryTrackRow(track: track)
                            .contentShape(Rectangle())
                            .onTapGesture { Task { await player.play(local.tracks, startAt: index) } }
                    }
                }
            }
            let remote = catalog.filter { $0.playback != .local }
            if !remote.isEmpty {
                Section("Household library") {
                    ForEach(remote, id: \.id) { track in CatalogTrackRow(track: track) }
                }
            }
            ForEach(sources) { source in
                Section(source.source.capitalized) {
                    if source.status != "ok" {
                        Label(source.error ?? "This source could not be searched.", systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.secondary)
                    }
                    ForEach(source.results) { result in
                        SearchResultRow(result: result)
                            .contentShape(Rectangle())
                            .onTapGesture { Task { await play(result, among: source.results) } }
                    }
                }
            }
        }
        .navigationTitle("Search")
        .searchable(text: $query, prompt: "Songs, albums, or artists")
        .overlay {
            if query.isEmpty {
                ContentUnavailableView("Search Reverb", systemImage: "magnifyingglass", description: Text("Results come from this iPhone's library, Deezer, and Spotify."))
            }
        }
        .task(id: query) {
            guard !query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { local = nil; catalog = []; sources = []; return }
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled, let client = core.client, let localCore = core.core else { return }
            async let localResult = try? client.searchLibrary(query: .init(q: query)).ok.body.json
            async let catalogResult = CatalogLibrary.load(client: client, query: query)
            async let externalResult = LoopbackAPI.search(core: localCore, query: query)
            local = await localResult
            catalog = await catalogResult
            sources = await externalResult
            ExternalPrewarm.shared.prewarm(results: sources.flatMap(\.results).map(\.playerTrack), core: localCore)
        }
    }

    private func play(_ result: SearchResult, among rows: [SearchResult]) async {
        guard let index = rows.firstIndex(where: { $0.id == result.id }) else { return }
        await player.play(rows.map(\.playerTrack), startAt: index)
    }
}

struct SearchResultRow: View {
    let result: SearchResult
    @EnvironmentObject private var core: CoreHost

    var body: some View {
        HStack {
            VStack(alignment: .leading) {
                Text(result.title)
                Text("\(result.artist) · \(result.source.capitalized)").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .contextMenu {
            if result.match?.status != "in_library" {
                Button("Download", systemImage: "arrow.down.circle") {
                    Task {
                        await Downloads.enqueue(core: core, source: result.source, externalId: result.externalId,
                                                title: result.title, artist: result.artist, album: result.album)
                    }
                }
            }
            Button("Not interested", role: .destructive) {
                Task {
                    guard let client = core.client else { return }
                    _ = try? await client.markNotInterested(body: .json(.init(
                        kind: .track, source: result.source, externalId: result.externalId,
                        title: result.title, artist: result.artist, album: result.album, durationMs: result.durationMs
                    ))).ok
                }
            }
        }
    }
}

enum LoopbackAPI {
    static func search(core: LocalCore, query: String) async -> [SearchEnvelope] {
        var components = URLComponents(url: core.apiURL.appendingPathComponent("search/everywhere"), resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "q", value: query), URLQueryItem(name: "type", value: "track")]
        guard let url = components.url else { return [] }
        let bytes: URLSession.AsyncBytes
        do {
            let (stream, response) = try await URLSession.shared.bytes(for: core.request(url))
            guard (response as? HTTPURLResponse)?.statusCode == 200 else {
                return [.init(source: "Search", status: "error", results: [], error: "Search sources are unavailable on this iPhone.")]
            }
            bytes = stream
        } catch {
            return [.init(source: "Search", status: "error", results: [], error: error.localizedDescription)]
        }
        var result: [SearchEnvelope] = []
        do {
            for try await line in bytes.lines where line.hasPrefix("data: ") {
                guard let data = line.dropFirst(6).data(using: .utf8),
                      let envelope = try? JSONDecoder().decode(SearchEnvelope.self, from: data) else { continue }
                result.removeAll { $0.source == envelope.source }
                result.append(envelope)
            }
        } catch {
            return [.init(source: "Search", status: "error", results: [], error: error.localizedDescription)]
        }
        return result.sorted { $0.source < $1.source }
    }

}
