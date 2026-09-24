import ReverbAPI
import SwiftUI

/// Downloads onto this iPhone. A Download lands in the phone's library and
/// plays at once; it stays until a paired device holds it (see Devices).
enum Downloads {
    @MainActor
    static func enqueue(core: CoreHost, source: String, externalId: String, title: String, artist: String,
                        album: String, isrc: String? = nil) async {
        guard let client = core.client else { return }
        _ = try? await client.enqueueDownload(body: .json(.init(
            source: source, externalId: externalId, artist: artist, title: title, album: album, isrc: isrc
        ))).ok
    }
}

/// The Downloads still waiting for a paired device, in sync status.
struct PendingUploadsSection: View {
    let pending: Components.Schemas.PendingUploads

    var body: some View {
        Section {
            ForEach(pending.files, id: \.relPath) { file in
                VStack(alignment: .leading) {
                    Text(file.title)
                    Text([file.artist, formatBytes(file.sizeBytes)].filter { !$0.isEmpty }.joined(separator: " · "))
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
        } header: {
            Text("Waiting to upload")
        } footer: {
            Text("\(formatBytes(pending.totalBytes)) of downloads stay on this iPhone until a paired device holds them. Keep Reverb installed until then.")
        }
        .accessibilityIdentifier("sync.pending")
    }
}
