import Foundation
import Security

struct SpotifyCredentials: Codable, Equatable, Sendable {
    let clientId: String
    let clientSecret: String
}

/// The desktop's search secret stays on this device, outside Reverb's backed-up
/// and replicated database. Only the signed app can read its Keychain item.
enum SearchCredentialStore {
    private static let service = "io.github.uhhhm.reverb.spotify"
    private static let account = "client-credentials"

    private static var query: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account]
    }

    static func load() -> SpotifyCredentials? {
        var request = query
        request[kSecReturnData as String] = true
        request[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        guard SecItemCopyMatching(request as CFDictionary, &result) == errSecSuccess,
              let data = result as? Data else { return nil }
        return try? JSONDecoder().decode(SpotifyCredentials.self, from: data)
    }

    /// Returns true only when the stored credentials changed.
    static func save(_ credentials: SpotifyCredentials) throws -> Bool {
        guard !credentials.clientId.isEmpty, !credentials.clientSecret.isEmpty else { return false }
        if load() == credentials { return false }
        let data = try JSONEncoder().encode(credentials)
        var item = query
        item[kSecValueData as String] = data
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(item as CFDictionary, nil)
        if status == errSecDuplicateItem {
            let update: [String: Any] = [
                kSecValueData as String: data,
                kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            ]
            let updated = SecItemUpdate(query as CFDictionary, update as CFDictionary)
            guard updated == errSecSuccess else { throw keychainError(updated) }
        } else if status != errSecSuccess {
            throw keychainError(status)
        }
        return true
    }

    static func clear() {
        SecItemDelete(query as CFDictionary)
    }

    private static func keychainError(_ status: OSStatus) -> NSError {
        NSError(domain: NSOSStatusErrorDomain, code: Int(status))
    }
}
