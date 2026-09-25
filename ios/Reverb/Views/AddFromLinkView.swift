import ReverbAPI
import SwiftUI
import UIKit

typealias LinkResolveResult = Components.Schemas.LinkResolveResult

/// Add from link: a Spotify or YouTube link, pasted, opened as
/// reverb://add?url=…, or shared from another app, is added to a playlist
/// and/or downloaded onto this iPhone. The core resolves it; an album or
/// playlist is added as its tracks.
@MainActor
final class LinkModel: ObservableObject {
    @Published var isPresented = false
    /// A link handed to the app, for the sheet to start with.
    @Published var incoming: String?

    /// Takes reverb://add?url=… from the share extension or anywhere else.
    /// Nothing is added until the owner confirms in the sheet.
    func opened(_ url: URL) {
        guard url.scheme == "reverb", url.host() == "add",
              let link = URLComponents(url: url, resolvingAgainstBaseURL: false)?
              .queryItems?.first(where: { $0.name == "url" })?.value,
              !link.isEmpty else { return }
        incoming = link
        isPresented = true
    }
}

struct AddFromLinkView: View {
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var links: LinkModel
    @Environment(\.dismiss) private var dismiss

    @State private var text = ""
    @State private var resolved: LinkResolveResult?
    @State private var resolving = false
    @State private var playlists: [Components.Schemas.SyncedPlaylist] = []
    @State private var playlistID = ""
    @State private var download = true
    @State private var adding = false
    @State private var problem: String?
    @State private var done: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Spotify or YouTube link", text: $text, axis: .vertical)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.URL)
                        .accessibilityIdentifier("link.url")
                    Button("Paste") {
                        if let pasted = UIPasteboard.general.string { text = pasted }
                    }
                }
                if resolving {
                    ProgressView()
                } else if let resolved {
                    Section("Link") {
                        VStack(alignment: .leading) {
                            Text(resolved.title)
                            if !resolved.artist.isEmpty && resolved.artist != "Unknown" {
                                Text(resolved.artist).font(.caption).foregroundStyle(.secondary)
                            }
                            Text("\(resolved.source.rawValue.capitalized) \(resolved.kind.rawValue)")
                                .font(.caption2).foregroundStyle(.secondary)
                        }
                        .accessibilityIdentifier("link.resolved")
                    }
                    Section {
                        Picker("Add to playlist", selection: $playlistID) {
                            Text("Library only").tag("")
                            ForEach(playlists, id: \.id) { Text($0.name).tag($0.id) }
                        }
                        Toggle("Download to this iPhone", isOn: $download)
                    } footer: {
                        Text(resolved.kind == .track
                             ? "A download stays on this iPhone until a paired device holds it."
                             : "Each track is added on its own. Downloads stay on this iPhone until a paired device holds them.")
                    }
                    Section {
                        Button {
                            Task { await add(resolved) }
                        } label: {
                            if adding { ProgressView() } else { Text("Add") }
                        }
                        .disabled(adding || (playlistID.isEmpty && !download))
                        .accessibilityIdentifier("link.add")
                    }
                }
                if let problem {
                    Text(problem).font(.callout).foregroundStyle(.red)
                }
                if let done {
                    Label(done, systemImage: "checkmark.circle").foregroundStyle(.green)
                }
            }
            .navigationTitle("Add from link")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Done") { dismiss() } }
            }
            // A link handed over while the sheet is open replaces the one in it.
            .onReceive(links.$incoming) { incoming in
                guard let incoming else { return }
                text = incoming
                links.incoming = nil
            }
            .task {
                if let client = core.client, let list = try? await client.listPlaylists().ok.body.json {
                    playlists = list.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
                }
            }
            .task(id: text) { await resolve() }
        }
    }

    private func resolve() async {
        let link = text.trimmingCharacters(in: .whitespacesAndNewlines)
        resolved = nil
        problem = nil
        done = nil
        guard !link.isEmpty, let client = core.client else { return }
        try? await Task.sleep(for: .milliseconds(300))
        guard !Task.isCancelled else { return }
        resolving = true
        defer { resolving = false }
        do {
            switch try await client.resolveLink(body: .json(.init(url: link))) {
            case let .ok(ok): resolved = try ok.body.json
            case .unprocessableContent: problem = "Reverb can add Spotify and YouTube links."
            default: problem = "This link could not be read."
            }
        } catch {
            problem = error.localizedDescription
        }
    }

    private func add(_ link: LinkResolveResult) async {
        guard let client = core.client else { return }
        adding = true
        defer { adding = false }
        problem = nil
        do {
            switch try await client.addFromLink(body: .json(.init(
                url: link.url, playlistId: playlistID.isEmpty ? nil : playlistID, download: download
            ))) {
            case let .ok(ok):
                let added = try ok.body.json
                let downloads = added.jobs?.count ?? (added.job == nil ? 0 : 1)
                if let error = added.downloadError, !error.isEmpty {
                    if playlistID.isEmpty {
                        done = downloads == 1 ? "Started 1 download" : "Started \(downloads) downloads"
                    } else {
                        done = "Added to playlist"
                    }
                    problem = "Some downloads could not be started: \(error)"
                    return
                }
                if downloads > 0 {
                    done = downloads == 1 ? "Downloading" : "Downloading \(downloads) tracks"
                } else {
                    done = "Added"
                }
            case .badGateway: problem = "Reverb could not look this link up. Spotify links need Spotify search, which this iPhone copies from a paired computer."
            case .serviceUnavailable: problem = "Downloads are not available on this iPhone."
            case .notFound: problem = "That playlist no longer exists."
            default: problem = "This link could not be added."
            }
        } catch {
            problem = error.localizedDescription
        }
    }
}
