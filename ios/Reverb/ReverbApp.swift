import SwiftUI

@main
struct ReverbApp: App {
    @StateObject private var core: CoreHost
    @StateObject private var player: Player
    @StateObject private var sync: SyncManager
    @StateObject private var pairing = PairingModel()
    @StateObject private var links = LinkModel()
    @Environment(\.scenePhase) private var scenePhase

    init() {
        let core = CoreHost()
        _core = StateObject(wrappedValue: core)
        let player = Player(core: core)
        _player = StateObject(wrappedValue: player)
        _sync = StateObject(wrappedValue: SyncManager(core: core))
        core.start()
        Theme.applyAppearance()
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .preferredColorScheme(.dark)
                .environmentObject(core)
                .environmentObject(player)
                .environmentObject(sync)
                .environmentObject(pairing)
                .environmentObject(links)
                // The system Camera, or any app or web page, opens a pairing
                // link here, and the share extension a link to add. Each waits
                // for the owner to confirm it.
                .onOpenURL { url in
                    pairing.opened(url)
                    links.opened(url)
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
        content
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Theme.background)
    }

    @ViewBuilder private var content: some View {
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
                    .buttonStyle(.borderedProminent)
                    .tint(Theme.green)
            }
        case .running:
            MainView()
        }
    }
}

struct MainView: View {
    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var pairing: PairingModel
    @EnvironmentObject private var links: LinkModel

    var body: some View {
        TabView {
            // The bar sits on the stack, not its root, so pushed screens show it too.
            NavigationStack {
                HomeView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Home", systemImage: "house.fill") }

            NavigationStack {
                SearchView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Search", systemImage: "magnifyingglass") }

            NavigationStack {
                PlaylistsView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Playlists", systemImage: "music.note.list") }

            NavigationStack {
                LibraryView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Library", systemImage: "books.vertical.fill") }

            NavigationStack {
                DevicesView()
            }
            .safeAreaInset(edge: .bottom) { NowPlayingBar() }
            .tabItem { Label("Devices", systemImage: "laptopcomputer.and.iphone") }
        }
        .tint(.white)
        .safeAreaInset(edge: .top) { VersionBanner() }
        // The core checks for a release at most hourly; peers' versions change
        // as they are reached.
        .task {
            while !Task.isCancelled {
                await core.refreshVersion()
                try? await Task.sleep(for: .seconds(60))
            }
        }
        // Dismissing the sheet drops an unconfirmed link without dialling it.
        .sheet(isPresented: $pairing.isPresented, onDismiss: pairing.discard) {
            PairingView()
        }
        .sheet(isPresented: $links.isPresented) {
            AddFromLinkView()
        }
    }
}

/// A lapsing signature, a newer release, or a paired device outside the
/// protocol support window. None blocks the app: a refresh or update is only
/// offered, and an incompatible device says which update fixes it.
struct VersionBanner: View {
    @EnvironmentObject private var core: CoreHost
    @Environment(\.scenePhase) private var scenePhase
    @AppStorage("dismissedUpdate") private var dismissedUpdate = ""
    @State private var signingDaysLeft: Int?

    var body: some View {
        VStack(spacing: 0) {
            if let days = signingDaysLeft {
                HStack {
                    Label(Self.expiryMessage(days), systemImage: "clock.badge.exclamationmark")
                        .font(.footnote)
                    Spacer()
                    Button("Refresh") { Sideloader.open(Sideloader.refresh) }
                        .font(.footnote.weight(.semibold))
                        .accessibilityIdentifier("signing.refresh")
                }
                .padding(10)
                .background(.yellow.opacity(0.2))
                .accessibilityIdentifier("signing.expiry")
            }
            if let incompatible = core.version?.peers.first(where: { $0.compatibility == .incompatible }) {
                Label(incompatible.message, systemImage: "exclamationmark.triangle.fill")
                    .font(.footnote)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(10)
                    .background(.orange.opacity(0.2))
                    .accessibilityIdentifier("version.outsideWindow")
            }
            if let info = core.version, !info.latestVersion.isEmpty, info.latestVersion != dismissedUpdate {
                HStack {
                    Label("Reverb \(info.latestVersion) is available.", systemImage: "arrow.down.app")
                        .font(.footnote)
                    Spacer()
                    let links = Sideloader.install(ipa: info.ipaUrl, fallback: info.releaseUrl)
                    if !links.isEmpty {
                        Button("Update") { Sideloader.open(links) }
                            .font(.footnote.weight(.semibold))
                            .accessibilityIdentifier("version.install")
                    }
                    Button { dismissedUpdate = info.latestVersion } label: { Image(systemName: "xmark") }
                        .accessibilityLabel("Dismiss")
                }
                .padding(10)
                .background(.thinMaterial)
                .accessibilityIdentifier("version.update")
            }
        }
        // The tints are translucent; content scrolling under must not show.
        .background(.bar)
        // A re-sign brings a new profile, read again on each return.
        .task(id: scenePhase) {
            guard scenePhase == .active else { return }
            signingDaysLeft = SigningProfile.currentExpiry.flatMap { SigningProfile.daysLeftToWarn(expiry: $0) }
        }
    }

    static func expiryMessage(_ days: Int) -> String {
        let when = switch days {
        case 0: "today"
        case 1: "tomorrow"
        default: "in \(days) days"
        }
        return "Reverb's signature expires \(when). Refresh it in SideStore to keep it opening."
    }
}

/// Formats bytes the way Settings shows storage.
func formatBytes(_ bytes: Int64) -> String {
    ByteCountFormatter.string(fromByteCount: bytes, countStyle: .file)
}
