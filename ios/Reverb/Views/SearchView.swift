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
        let libraryId = match?.status == "in_library" ? match?.libraryTrackId : nil
        return Player.resolvedTrack(
            source: source, externalId: externalId, title: title, artist: artist, album: album,
            durationMs: durationMs, libraryId: libraryId,
            coverArtId: match?.coverArtId ?? coverArtId, coverUrl: coverUrl
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
                Section {
                    ForEach(Array(local.tracks.enumerated()), id: \.element.id) { index, track in
                        LibraryTrackRow(track: track)
                            .contentShape(Rectangle())
                            .onTapGesture { Task { await player.play(local.tracks, startAt: index) } }
                            .darkRow()
                    }
                } header: { SearchSectionHeader(title: "Your library") }
            }
            let remote = catalog.filter { $0.playback != .local }
            if !remote.isEmpty {
                Section {
                    ForEach(remote, id: \.id) { track in CatalogTrackRow(track: track).darkRow() }
                } header: { SearchSectionHeader(title: "Household library") }
            }
            ForEach(sources) { source in
                Section {
                    if source.status != "ok" {
                        Label(source.error ?? "This source could not be searched.", systemImage: "exclamationmark.triangle")
                            .font(.footnote)
                            .foregroundStyle(Theme.secondaryText)
                            .darkRow()
                    }
                    ForEach(source.results) { result in
                        SearchResultRow(result: result)
                            .contentShape(Rectangle())
                            .onTapGesture { Task { await play(result, among: source.results) } }
                            .darkRow()
                    }
                } header: { SearchSectionHeader(title: source.source.capitalized) }
            }
        }
        .darkList()
        .navigationTitle("Search")
        .searchable(text: $query, prompt: "Songs, albums, or artists")
        .overlay {
            if query.isEmpty {
                VStack(spacing: 10) {
                    Text("Play what you love")
                        .font(.title2.weight(.bold))
                        .foregroundStyle(.white)
                    Text("Search this iPhone's library, Deezer, and Spotify.")
                        .font(.subheadline)
                        .foregroundStyle(Theme.secondaryText)
                }
                .multilineTextAlignment(.center)
                .padding()
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

private struct SearchSectionHeader: View {
    let title: String

    var body: some View {
        Text(title)
            .font(.headline.weight(.bold))
            .foregroundStyle(.white)
            .textCase(nil)
    }
}

struct SearchResultRow: View {
    @EnvironmentObject private var player: Player
    let result: SearchResult
    @EnvironmentObject private var core: CoreHost

    var body: some View {
        TrackLine(title: result.title, subtitle: "Song · \(result.artist)",
                  art: .init(coverArtID: result.match?.coverArtId ?? result.coverArtId, coverURL: result.coverUrl, seed: result.album),
                  isCurrent: player.current?.title == result.title && player.current?.artist == result.artist,
                  downloaded: result.match?.status == "in_library")
        .contextMenu {
            Button("Start Radio", systemImage: "dot.radiowaves.left.and.right") { Task { await player.startRadio(tracks: [result.playerTrack]) } }
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
