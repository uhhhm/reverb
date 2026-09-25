import UIKit
import UniformTypeIdentifiers

/// "Reverb" in the share sheet: hands a shared Spotify or YouTube link to the
/// app as reverb://add?url=…, where the owner chooses a playlist and whether
/// to download. The extension adds nothing itself; the core runs in the app.
final class ShareViewController: UIViewController {
    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        Task { await handOver() }
    }

    private func handOver() async {
        guard let link = await sharedLink(),
              var components = URLComponents(string: "reverb://add") else {
            finish(message: "There is no link to add.")
            return
        }
        components.queryItems = [URLQueryItem(name: "url", value: link)]
        if let url = components.url, await openContainingApp(url) {
            extensionContext?.completeRequest(returningItems: nil)
            return
        }
        UIPasteboard.general.string = link
        finish(message: "The link is copied. Open Reverb and paste it in Add from link.")
    }

    /// The first web URL shared, or one found in shared text.
    private func sharedLink() async -> String? {
        let providers = (extensionContext?.inputItems as? [NSExtensionItem] ?? []).flatMap { $0.attachments ?? [] }
        for provider in providers where provider.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
            if let url = try? await provider.loadItem(forTypeIdentifier: UTType.url.identifier) as? URL,
               url.scheme?.hasPrefix("http") == true {
                return url.absoluteString
            }
        }
        for provider in providers where provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
            if let text = try? await provider.loadItem(forTypeIdentifier: UTType.plainText.identifier) as? String,
               let found = Self.firstLink(in: text) {
                return found
            }
        }
        return nil
    }

    static func firstLink(in text: String) -> String? {
        let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue)
        let range = NSRange(text.startIndex..., in: text)
        return detector?.firstMatch(in: text, range: range)?.url?.absoluteString
    }

    /// An extension cannot call UIApplication.open, but the application is on
    /// its responder chain; its open method is reached through the runtime.
    private func openContainingApp(_ url: URL) async -> Bool {
        await withCheckedContinuation { continuation in
            typealias Open = @convention(c) (AnyObject, Selector, URL, NSDictionary, @convention(block) (Bool) -> Void) -> Void
            let selector = NSSelectorFromString("openURL:options:completionHandler:")
            var responder: UIResponder? = self
            while let current = responder {
                if current is UIApplication, current.responds(to: selector) {
                    let open = unsafeBitCast(current.method(for: selector), to: Open.self)
                    open(current, selector, url, NSDictionary()) { opened in
                        continuation.resume(returning: opened)
                    }
                    return
                }
                responder = current.next
            }
            continuation.resume(returning: false)
        }
    }

    private func finish(message: String) {
        let alert = UIAlertController(title: "Reverb", message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "OK", style: .default) { [weak self] _ in
            self?.extensionContext?.completeRequest(returningItems: nil)
        })
        present(alert, animated: true)
    }
}
