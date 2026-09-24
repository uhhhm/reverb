import SwiftUI

@main
struct ReverbApp: App {
    @StateObject private var core: CoreHost
    @StateObject private var player: Player
    @StateObject private var sync: SyncManager
    @StateObject private var pairing = PairingModel()
    @Environment(\.scenePhase) private var scenePhase

    init() {
        let core = CoreHost()
        _core = StateObject(wrappedValue: core)
        let player = Player(core: core)
        _player = StateObject(wrappedValue: player)
        _sync = StateObject(wrappedValue: SyncManager(core: core))
        core.start()
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(core)
                .environmentObject(player)
                .environmentObject(sync)
                .environmentObject(pairing)
                // The system Camera, or any app or web page, opens a pairing
                // link here. It waits for the owner to confirm its target.
                .onOpenURL { url in
                    pairing.opened(url)
                }
                .task { await sync.foregrounded() }
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                Task { await sync.foregrounded() }
            } else if phase == .background {
                sync.scheduleBackgroundRefresh()
            }
        }
        .onChange(of: player.isPlaying) { _, playing in
            sync.setPlaybackActive(playing)
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
                HomeView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Home", systemImage: "house") }

            NavigationStack {
                PlaylistsView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Playlists", systemImage: "music.note.list") }

            NavigationStack {
                LibraryView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Library", systemImage: "music.note") }

            NavigationStack {
                SearchView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Search", systemImage: "magnifyingglass") }

            NavigationStack {
                DevicesView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Devices", systemImage: "laptopcomputer.and.iphone") }
        }
        // Dismissing the sheet drops an unconfirmed link without dialling it.
        .sheet(isPresented: $pairing.isPresented, onDismiss: pairing.discard) {
            PairingView()
        }
    }
}

/// Formats bytes the way Settings shows storage.
func formatBytes(_ bytes: Int64) -> String {
    ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file)
}
