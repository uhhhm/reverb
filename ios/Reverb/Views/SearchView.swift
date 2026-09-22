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
    var playableID: String? { match?.status == "in_library" ? match?.libraryTrackId : nil }
}

struct SearchView: View {
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var player: Player
    @State private var query = ""
    @State private var local: Components.Schemas.LibrarySearchResults?
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
            guard !query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { local = nil; sources = []; return }
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled, let client = core.client, let localCore = core.core else { return }
            async let localResult = try? client.searchLibrary(query: .init(q: query)).ok.body.json
            async let externalResult = LoopbackAPI.search(base: localCore.apiURL, query: query)
            local = await localResult
            sources = await externalResult
        }
    }

    private func play(_ result: SearchResult, among rows: [SearchResult]) async {
        let playable = rows.compactMap { row -> PlayerTrack? in
            guard let id = row.playableID else { return nil }
            return PlayerTrack(id: id, title: row.title, artist: row.artist, album: row.album, durationMs: row.durationMs)
        }
        guard let id = result.playableID, let index = playable.firstIndex(where: { $0.id == id }) else { return }
        await player.play(playable, startAt: index)
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
            if result.playableID == nil {
                Image(systemName: "icloud.slash").foregroundStyle(.secondary)
                    .accessibilityLabel("Not yet playable")
            }
        }
        .opacity(result.playableID == nil ? 0.55 : 1)
        .contextMenu {
            Button("Not interested", role: .destructive) {
                Task {
                    guard let base = core.core?.apiURL else { return }
                    await LoopbackAPI.notInterested(base: base, body: .init(
                        kind: "track", source: result.source, externalId: result.externalId,
                        title: result.title, artist: result.artist, album: result.album, durationMs: result.durationMs
                    ))
                }
            }
        }
    }
}

enum LoopbackAPI {
    struct NotInterestedBody: Encodable {
        let kind: String
        let source: String
        var externalId: String? = nil
        var trackId: String? = nil
        var title: String? = nil
        var artist: String? = nil
        var album: String? = nil
        var durationMs: Int? = nil
        var id: String? = nil
        var name: String? = nil
    }

    static func search(base: URL, query: String) async -> [SearchEnvelope] {
        var components = URLComponents(url: base.appendingPathComponent("search/everywhere"), resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "q", value: query), URLQueryItem(name: "type", value: "track")]
        guard let url = components.url else { return [] }
        let bytes: URLSession.AsyncBytes
        do {
            let (stream, response) = try await URLSession.shared.bytes(from: url)
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

    @discardableResult
    static func notInterested(base: URL, body: NotInterestedBody) async -> Bool {
        var request = URLRequest(url: base.appendingPathComponent("not-interested"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try? JSONEncoder().encode(body)
        guard let (_, response) = try? await URLSession.shared.data(for: request) else { return false }
        return (response as? HTTPURLResponse)?.statusCode == 200
    }
}
