import SwiftUI

@main
struct ReverbApp: App {
    @StateObject private var core: CoreHost
    @StateObject private var player: Player
    @StateObject private var pairing = PairingModel()
    @Environment(\.scenePhase) private var scenePhase

    init() {
        let core = CoreHost()
        _core = StateObject(wrappedValue: core)
        _player = StateObject(wrappedValue: Player(core: core))
        core.start()
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(core)
                .environmentObject(player)
                .environmentObject(pairing)
                // The system Camera opens a scanned pairing QR code here.
                .onOpenURL { url in
                    pairing.scanned(url.absoluteString)
                }
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                Task { await core.ensureRunning() }
            }
        }
    }
}

struct RootView: View {
    @EnvironmentObject private var core: CoreHost

    var body: some View {
        switch core.state {
        case .starting:
            ProgressView("Starting Reverb…")
        case let .failed(message):
            ContentUnavailableView {
                Label("Reverb could not start", systemImage: "exclamationmark.triangle")
            } description: {
                Text(message)
            } actions: {
                Button("Try again") { core.start() }
            }
        case .running:
            MainView()
        }
    }
}

struct MainView: View {
    @EnvironmentObject private var pairing: PairingModel

    var body: some View {
        TabView {
            // The bar sits on the stack, not its root, so pushed screens show it too.
            NavigationStack {
                PlaylistsView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Playlists", systemImage: "music.note.list") }

            NavigationStack {
                DevicesView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Devices", systemImage: "laptopcomputer.and.iphone") }
        }
        .sheet(isPresented: $pairing.isPresented) {
            PairingView()
        }
    }
}

/// Formats bytes the way Settings shows storage.
func formatBytes(_ bytes: Int64) -> String {
    ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file)
}
