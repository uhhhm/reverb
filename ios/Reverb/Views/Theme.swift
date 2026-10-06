import CoreImage
import ObjectiveC
import SwiftUI
import UIKit

/// The app's look: dark surfaces, white type, and green for play and for
/// what is kept on this iPhone.
enum Theme {
    static let background = Color(red: 0.07, green: 0.07, blue: 0.07)
    static let elevated = Color(white: 0.16)
    static let highlight = Color(white: 0.24)
    static let green = Color(red: 0.12, green: 0.84, blue: 0.38)
    static let secondaryText = Color(white: 0.70)

    /// Tab and navigation bars over the dark background.
    @MainActor
    static func applyAppearance() {
        let tabBar = UITabBarAppearance()
        tabBar.configureWithTransparentBackground()
        tabBar.backgroundEffect = UIBlurEffect(style: .systemChromeMaterialDark)
        tabBar.backgroundColor = UIColor(white: 0.04, alpha: 0.85)
        for layout in [tabBar.stackedLayoutAppearance, tabBar.inlineLayoutAppearance, tabBar.compactInlineLayoutAppearance] {
            layout.normal.iconColor = UIColor(white: 0.7, alpha: 1)
            layout.normal.titleTextAttributes = [.foregroundColor: UIColor(white: 0.7, alpha: 1)]
            layout.selected.iconColor = .white
            layout.selected.titleTextAttributes = [.foregroundColor: UIColor.white]
        }
        UITabBar.appearance().standardAppearance = tabBar
        UITabBar.appearance().scrollEdgeAppearance = tabBar

        let nav = UINavigationBarAppearance()
        nav.configureWithTransparentBackground()
        nav.largeTitleTextAttributes = [.foregroundColor: UIColor.white, .font: UIFont.systemFont(ofSize: 30, weight: .heavy)]
        nav.titleTextAttributes = [.foregroundColor: UIColor.white, .font: UIFont.systemFont(ofSize: 17, weight: .bold)]
        let scrolled = UINavigationBarAppearance()
        scrolled.configureWithDefaultBackground()
        scrolled.backgroundEffect = UIBlurEffect(style: .systemChromeMaterialDark)
        scrolled.largeTitleTextAttributes = nav.largeTitleTextAttributes
        scrolled.titleTextAttributes = nav.titleTextAttributes
        UINavigationBar.appearance().standardAppearance = scrolled
        UINavigationBar.appearance().compactAppearance = scrolled
        UINavigationBar.appearance().scrollEdgeAppearance = nav
    }

    /// A stable colour for something with no artwork, picked from its name.
    static func color(for seed: String) -> Color {
        let palette: [Color] = [
            Color(red: 0.31, green: 0.20, blue: 0.62), Color(red: 0.12, green: 0.45, blue: 0.40),
            Color(red: 0.70, green: 0.24, blue: 0.24), Color(red: 0.16, green: 0.35, blue: 0.62),
            Color(red: 0.62, green: 0.36, blue: 0.12), Color(red: 0.49, green: 0.16, blue: 0.47),
            Color(red: 0.25, green: 0.42, blue: 0.18), Color(red: 0.55, green: 0.12, blue: 0.30),
        ]
        let hash = seed.unicodeScalars.reduce(UInt32(5381)) { ($0 &* 33) &+ $1.value }
        return palette[Int(hash % UInt32(palette.count))]
    }
}

extension View {
    /// A list on the dark background with no separators or row fills.
    func darkList() -> some View {
        listStyle(.plain)
            .scrollContentBackground(.hidden)
            .background(Theme.background)
    }

    /// A row in a `darkList`.
    func darkRow() -> some View {
        listRowBackground(Color.clear)
            .listRowSeparator(.hidden)
    }
}

/// Where cover art comes from: the core's library art, or a source's own URL.
struct ArtworkSource: Equatable {
    var coverArtID: String?
    var coverURL: String?
    /// Names the colour shown when there is no art.
    var seed: String = ""
}

/// Square cover art, or a coloured tile with a note when there is none.
struct Artwork: View {
    @EnvironmentObject private var core: CoreHost
    let source: ArtworkSource
    var size: CGFloat? = nil
    var cornerRadius: CGFloat = 4
    var symbol = "music.note"

    var body: some View {
        Group {
            if let request {
                CoverImage(request: request, placeholder: placeholder)
            } else {
                placeholder
            }
        }
        .frame(width: size, height: size)
        .aspectRatio(1, contentMode: .fit)
        .clipShape(RoundedRectangle(cornerRadius: cornerRadius))
        .accessibilityHidden(true)
    }

    private var request: URLRequest? {
        if let id = source.coverArtID, !id.isEmpty, let local = core.core, let url = local.coverURL(id: id) {
            return local.request(url)
        }
        if let raw = source.coverURL, raw.hasPrefix("https://"), let url = URL(string: raw) {
            return URLRequest(url: url)
        }
        return nil
    }

    private var placeholder: some View {
        LinearGradient(colors: [Theme.color(for: source.seed), Theme.color(for: source.seed).opacity(0.45)],
                       startPoint: .topLeading, endPoint: .bottomTrailing)
            .overlay {
                GeometryReader { proxy in
                    Image(systemName: symbol)
                        .font(.system(size: max(12, proxy.size.width * 0.32), weight: .semibold))
                        .foregroundStyle(.white.opacity(0.8))
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
    }
}

/// Cover art from the core or a source. AsyncImage cannot send the launch
/// secret, so this loads the request itself.
struct CoverImage<Placeholder: View>: View {
    let request: URLRequest
    let placeholder: Placeholder
    @State private var image: UIImage?

    var body: some View {
        Group {
            if let image {
                Image(uiImage: image).resizable().scaledToFill()
            } else {
                placeholder
            }
        }
        .task(id: request.url) {
            guard let (data, _) = try? await URLSession.shared.data(for: request) else { return }
            image = UIImage(data: data)
        }
    }
}

extension CoverImage where Placeholder == Color {
    init(request: URLRequest) {
        self.init(request: request, placeholder: Theme.elevated)
    }
}

/// The round green play button that heads every collection.
struct PlayCircleButton: View {
    var isPlaying = false
    var size: CGFloat = 52
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: isPlaying ? "pause.fill" : "play.fill")
                .font(.system(size: size * 0.4, weight: .bold))
                .foregroundStyle(.black)
                .offset(x: isPlaying ? 0 : size * 0.03)
                .frame(width: size, height: size)
                .background(Theme.green, in: Circle())
        }
        .buttonStyle(PressableStyle())
        .accessibilityLabel(isPlaying ? "Pause" : "Play")
    }
}

/// Shrinks a little while pressed, as Spotify's controls do.
struct PressableStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(configuration.isPressed ? 0.94 : 1)
            .opacity(configuration.isPressed ? 0.85 : 1)
            .animation(.easeOut(duration: 0.12), value: configuration.isPressed)
    }
}

/// A bold section title above a shelf.
struct ShelfHeader: View {
    let title: String

    var body: some View {
        Text(title)
            .font(.title2.weight(.bold))
            .foregroundStyle(.white)
            .lineLimit(1)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// A rounded filter chip, as heads Your Library.
struct FilterChip: View {
    let title: String
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(title)
                .font(.subheadline.weight(.medium))
                .foregroundStyle(selected ? .black : .white)
                .padding(.horizontal, 14)
                .padding(.vertical, 7)
                .background(selected ? Theme.green : Theme.elevated, in: Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// The header of a playlist, album or artist: big art over a wash of its colour.
struct CollectionHeader<Art: View, Actions: View>: View {
    let title: String
    let subtitle: String
    var detail: String = ""
    var tint: Color
    @ViewBuilder let art: () -> Art
    @ViewBuilder let actions: () -> Actions

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            art()
                .frame(width: 230, height: 230)
                .shadow(color: .black.opacity(0.5), radius: 18, y: 8)
                .frame(maxWidth: .infinity)
                .padding(.top, 8)
            Text(title)
                .font(.title.weight(.heavy))
                .foregroundStyle(.white)
                .lineLimit(2)
            if !subtitle.isEmpty {
                Text(subtitle)
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(.white)
            }
            if !detail.isEmpty {
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(Theme.secondaryText)
            }
            actions()
        }
        .padding(.horizontal)
        .padding(.bottom, 8)
        .background(alignment: .top) {
            LinearGradient(colors: [tint.opacity(0.75), Theme.background], startPoint: .top, endPoint: .bottom)
                .frame(height: 520)
                .offset(y: -200)
        }
    }
}

private var averageColorKey = 0

extension UIImage {
    /// The image's average colour, darkened so white type stays readable.
    /// Views ask on every frame, so it is worked out once per image.
    var averageColor: Color? {
        if let cached = objc_getAssociatedObject(self, &averageColorKey) as? Color { return cached }
        let color = computeAverageColor()
        if let color { objc_setAssociatedObject(self, &averageColorKey, color, .OBJC_ASSOCIATION_RETAIN) }
        return color
    }

    private func computeAverageColor() -> Color? {
        guard let input = CIImage(image: self) else { return nil }
        let extent = CIVector(x: input.extent.minX, y: input.extent.minY, z: input.extent.width, w: input.extent.height)
        guard let filter = CIFilter(name: "CIAreaAverage", parameters: [kCIInputImageKey: input, kCIInputExtentKey: extent]),
              let output = filter.outputImage else { return nil }
        var pixel = [UInt8](repeating: 0, count: 4)
        CIContext(options: [.workingColorSpace: kCFNull as Any])
            .render(output, toBitmap: &pixel, rowBytes: 4, bounds: CGRect(x: 0, y: 0, width: 1, height: 1), format: .RGBA8, colorSpace: nil)
        let color = UIColor(red: CGFloat(pixel[0]) / 255, green: CGFloat(pixel[1]) / 255, blue: CGFloat(pixel[2]) / 255, alpha: 1)
        var hue: CGFloat = 0, saturation: CGFloat = 0, brightness: CGFloat = 0, alpha: CGFloat = 0
        color.getHue(&hue, saturation: &saturation, brightness: &brightness, alpha: &alpha)
        return Color(hue: hue, saturation: min(1, saturation * 1.2), brightness: min(brightness, 0.55))
    }
}

/// "Good morning" and so on, by the phone's clock.
func greeting(at date: Date = .now) -> String {
    switch Calendar.current.component(.hour, from: date) {
    case 5..<12: "Good morning"
    case 12..<18: "Good afternoon"
    default: "Good evening"
    }
}

func formatTimestamp(_ seconds: Double) -> String {
    let total = seconds.isFinite ? max(0, Int(seconds)) : 0
    return String(format: "%d:%02d", total / 60, total % 60)
}

/// A track in a list: art, title (green while it plays), artist, and a
/// trailing accessory such as the "…" menu.
struct TrackLine<Accessory: View>: View {
    let title: String
    let subtitle: String
    var art: ArtworkSource?
    var artSize: CGFloat = 48
    var artCornerRadius: CGFloat = 4
    var artSymbol = "music.note"
    var isCurrent = false
    /// The small green arrow Spotify shows on a downloaded song.
    var downloaded = false
    @ViewBuilder var accessory: () -> Accessory

    var body: some View {
        HStack(spacing: 12) {
            if let art {
                Artwork(source: art, size: artSize, cornerRadius: artCornerRadius, symbol: artSymbol)
            }
            VStack(alignment: .leading, spacing: 3) {
                Text(title)
                    .font(.body)
                    .foregroundStyle(isCurrent ? Theme.green : .white)
                    .lineLimit(1)
                HStack(spacing: 5) {
                    if downloaded {
                        Image(systemName: "arrow.down.circle.fill")
                            .font(.caption2)
                            .foregroundStyle(Theme.green)
                    }
                    Text(subtitle)
                        .font(.subheadline)
                        .foregroundStyle(Theme.secondaryText)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            accessory()
        }
        .padding(.vertical, 2)
    }
}

extension TrackLine where Accessory == EmptyView {
    init(title: String, subtitle: String, art: ArtworkSource? = nil, artSize: CGFloat = 48, artCornerRadius: CGFloat = 4,
         artSymbol: String = "music.note", isCurrent: Bool = false, downloaded: Bool = false) {
        self.init(title: title, subtitle: subtitle, art: art, artSize: artSize, artCornerRadius: artCornerRadius, artSymbol: artSymbol,
                  isCurrent: isCurrent, downloaded: downloaded) { EmptyView() }
    }
}

/// The "…" that opens a row's menu.
struct MoreGlyph: View {
    var body: some View {
        Image(systemName: "ellipsis")
            .font(.body.weight(.semibold))
            .foregroundStyle(Theme.secondaryText)
            .frame(width: 32, height: 40)
            .contentShape(Rectangle())
    }
}

/// "1 song", "12 songs".
func songsLabel(_ count: Int) -> String {
    count == 1 ? "1 song" : "\(count) songs"
}
