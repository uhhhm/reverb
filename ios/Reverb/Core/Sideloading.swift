import Foundation
import UIKit

/// The signature SideStore or AltStore gave this install (ADR 0004). A free
/// Apple ID's lasts 7 days; once it lapses the app stops launching until it
/// is re-signed, so it warns while it still can.
enum SigningProfile {
    /// When this install's signature expires: nil in the simulator, from Xcode
    /// without a profile, or when the profile cannot be read.
    static var currentExpiry: Date? {
        let url = Bundle.main.url(forResource: "embedded", withExtension: "mobileprovision")
        return expiry(of: url.flatMap { try? Data(contentsOf: $0) })
    }

    /// Reads ExpirationDate from a provisioning profile: a CMS envelope whose
    /// signed content is a plain XML plist.
    static func expiry(of profile: Data?) -> Date? {
        guard let profile,
              let start = profile.range(of: Data("<?xml".utf8)),
              let end = profile.range(of: Data("</plist>".utf8), in: start.lowerBound..<profile.endIndex),
              let plist = try? PropertyListSerialization.propertyList(from: profile[start.lowerBound..<end.upperBound], format: nil),
              let dict = plist as? [String: Any]
        else { return nil }
        return dict["ExpirationDate"] as? Date
    }

    /// Whole calendar days left when that is two or fewer (0 is today, or
    /// already past), and nil while the signature has longer to run.
    static func daysLeftToWarn(expiry: Date, now: Date = .now, calendar: Calendar = .current) -> Int? {
        let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: now), to: calendar.startOfDay(for: expiry)).day ?? 0
        return days <= 2 ? max(days, 0) : nil
    }
}

/// Hands installs and refreshes to SideStore, or AltStore when SideStore is
/// not installed. Each action is a list of links tried in order.
enum Sideloader {
    static func install(ipa: String, fallback: String) -> [URL] {
        var links = ipa.isEmpty ? [] : ["sidestore", "altstore"].compactMap { link($0, "install", ipa) }
        if !fallback.isEmpty, let web = URL(string: fallback) { links.append(web) }
        return links
    }

    static func addSource(_ source: String) -> [URL] {
        source.isEmpty ? [] : ["sidestore", "altstore"].compactMap { link($0, "source", source) }
    }

    /// Opens the app at My Apps, where the owner taps Refresh; neither has a
    /// link that refreshes by itself.
    static let refresh = [URL(string: "sidestore://")!, URL(string: "altstore://")!]

    /// Opens the first link an installed app accepts.
    @MainActor
    static func open(_ links: [URL]) {
        guard let first = links.first else { return }
        UIApplication.shared.open(first) { opened in
            if !opened { Task { @MainActor in open(Array(links.dropFirst())) } }
        }
    }

    // The whole URL is one query value, so everything but unreserved
    // characters is escaped; SideStore decodes it with URLComponents.
    private static func link(_ scheme: String, _ action: String, _ url: String) -> URL? {
        let unreserved = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")
        guard let value = url.addingPercentEncoding(withAllowedCharacters: unreserved) else { return nil }
        return URL(string: "\(scheme)://\(action)?url=\(value)")
    }
}
