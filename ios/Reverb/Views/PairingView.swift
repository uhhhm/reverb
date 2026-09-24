import ReverbAPI
import Reverbcore
import SwiftUI
import UIKit

/// Pairing from the phone's side: the phone is the redeemer. The code proves
/// possession through the core's challenge-response and never crosses the
/// network, whether it arrives in a pairing link, a scanned QR code, or typed.
///
/// A link or QR code names the device to pair with, and anyone can send a
/// link, so neither pairs until the owner has seen where it points and tapped
/// Pair. A typed code is already the owner's own act.
@MainActor
final class PairingModel: ObservableObject {
    @Published var isPresented = false
    /// A link or scanned code waiting for the owner to confirm its target.
    @Published var pending: PendingPair?
    /// Why the last link or scanned code could not be read.
    @Published var problem: String?

    struct PendingPair: Equatable {
        let payload: String
        let target: PairTarget
        /// Opened from outside the app, so cancelling closes the sheet
        /// rather than returning to the scanner.
        let fromLink: Bool
    }

    /// Opens a pairing link from the system (the Camera, a message, a web
    /// page) for confirmation. Nothing is dialled or redeemed here.
    func opened(_ url: URL) {
        guard url.scheme == "reverb", url.host() == "pair" else { return }
        review(url.absoluteString, fromLink: true)
        isPresented = true
    }

    /// Puts a code read by the in-app scanner up for confirmation.
    func scanned(_ payload: String) {
        review(payload, fromLink: false)
    }

    func discard() {
        pending = nil
        problem = nil
    }

    private func review(_ payload: String, fromLink: Bool) {
        switch PairTarget.inspect(payload) {
        case let .success(target):
            pending = PendingPair(payload: payload, target: target, fromLink: fromLink)
            problem = nil
        case let .failure(error):
            pending = nil
            problem = error.localizedDescription
        }
    }
}

/// Where a pairing link points, read by the core without dialling it.
struct PairTarget: Decodable, Equatable {
    struct Address: Decodable, Equatable, Hashable {
        let addr: String
        /// On a LAN or VPN (loopback, private, link-local or CGNAT) rather
        /// than the open internet.
        let local: Bool
    }

    let peerId: String
    let expiresAt: Int64
    let addrs: [Address]

    /// Every address is on the open internet: nothing about the link says the
    /// device is the owner's.
    var allPublic: Bool { !addrs.contains(where: \.local) }

    static func inspect(_ payload: String) -> Result<PairTarget, Error> {
        var error: NSError?
        let json = ReverbcoreInspectPairPayload(payload, &error)
        if let error { return .failure(error) }
        do {
            return .success(try JSONDecoder().decode(PairTarget.self, from: Data(json.utf8)))
        } catch {
            return .failure(error)
        }
    }
}

struct PairingView: View {
    enum Mode: String, CaseIterable, Identifiable {
        case scan = "Scan QR code"
        case type = "Type a code"
        var id: Self { self }
    }

    @EnvironmentObject private var core: CoreHost
    @EnvironmentObject private var pairing: PairingModel
    @Environment(\.dismiss) private var dismiss
    @State private var mode: Mode = .scan
    @State private var code = ""
    @State private var address = ""
    @State private var busy = false
    @State private var message: String?

    var body: some View {
        NavigationStack {
            Group {
                if let pending = pairing.pending {
                    confirmation(pending)
                } else {
                    chooser
                }
            }
            .navigationTitle("Pair a device")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
    }

    /// What a link or scanned code points at, for the owner to accept or not.
    private func confirmation(_ pending: PairingModel.PendingPair) -> some View {
        Form {
            Section {
                Text(pending.target.peerId)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                    .accessibilityIdentifier("pair.peer")
            } header: {
                Text("Device")
            } footer: {
                Text("Pair only with a code you just showed on your own computer. A paired device receives your library, playlists and history.")
            }
            if pending.target.allPublic {
                Section {
                    Label("None of these addresses are on your home network or VPN. Someone else may have sent you this code.", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .accessibilityIdentifier("pair.publicWarning")
                }
            }
            Section("Addresses") {
                ForEach(pending.target.addrs, id: \.self) { address in
                    LabeledContent {
                        Text(address.local ? "LAN/VPN" : "Public")
                            .foregroundStyle(address.local ? Color.secondary : Color.orange)
                    } label: {
                        Text(address.addr).font(.caption.monospaced())
                    }
                }
            }
            Section {
                Button("Pair") { Task { await redeem(payload: pending.payload) } }
                    .disabled(busy)
                    .accessibilityIdentifier("pair.confirm")
                Button("Cancel", role: .cancel) {
                    message = nil
                    pairing.discard()
                    if pending.fromLink { dismiss() }
                }
                .disabled(busy)
                .accessibilityIdentifier("pair.reject")
            }
            status
        }
    }

    private var chooser: some View {
        Form {
            Picker("How", selection: $mode) {
                ForEach(Mode.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .accessibilityIdentifier("pair.mode")

            switch mode {
            case .scan:
                Section {
                    QRScannerView { payload in
                        message = nil
                        pairing.scanned(payload)
                    }
                    .frame(height: 280)
                    .listRowInsets(EdgeInsets())
                } footer: {
                    Text("On your computer, open Reverb's Settings, choose Pair a device, and point the camera at the code.")
                }
            case .type:
                Section {
                    TextField("Pairing code", text: $code)
                        .textInputAutocapitalization(.characters)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("pair.code")
                    TextField("Address (optional)", text: $address)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .keyboardType(.URL)
                        .accessibilityIdentifier("pair.address")
                } footer: {
                    Text("On your home network the code is enough. Over a VPN, also copy one of the addresses the computer shows under Pair a device.")
                }
                Section {
                    Button("Pair") { Task { await redeem() } }
                        .disabled(busy || code.trimmingCharacters(in: .whitespaces).isEmpty)
                        .accessibilityIdentifier("pair.submit")
                }
            }
            status
        }
    }

    @ViewBuilder private var status: some View {
        if busy {
            ProgressView("Pairing…")
        }
        if let text = message ?? pairing.problem {
            Text(text)
                .foregroundStyle(.red)
                .accessibilityIdentifier("pair.error")
        }
    }

    private var deviceName: String { UIDevice.current.name }

    private func redeem(payload: String) async {
        guard !busy, let client = core.client else { return }
        busy = true
        defer { busy = false }
        do {
            let output = try await client.redeemPairingQR(body: .json(.init(payload: payload, deviceName: deviceName)))
            switch output {
            case .ok: await paired()
            case .badRequest: message = "That is not a Reverb pairing code, or it is from a newer version of Reverb."
            case .conflict: message = "That code was already used. Show a new one on the computer."
            case .gone: message = "That code has expired. Show a new one on the computer."
            case .tooManyRequests: message = "Too many attempts. Wait a minute and try again."
            default: message = "Pairing failed. Check that both devices are on the same network or VPN."
            }
        } catch {
            message = error.localizedDescription
        }
    }

    private func redeem() async {
        guard !busy, let client = core.client else { return }
        busy = true
        defer { busy = false }
        let target = address.trimmingCharacters(in: .whitespaces)
        let typed = code.trimmingCharacters(in: .whitespaces)
        do {
            let output = try await client.redeemPairingCode(body: .json(.init(
                peerId: target.isEmpty ? nil : target, code: typed, deviceName: deviceName
            )))
            switch output {
            case .ok: await paired()
            case .badRequest: message = "Enter the code the computer shows."
            default: message = "Pairing failed. Check the code, and that both devices are on the same network or VPN."
            }
        } catch {
            message = error.localizedDescription
        }
    }

    private func paired() async {
        message = nil
        dismiss()
        Task {
            await core.refreshSearchCredentials()
            if let current = core.client { _ = try? await current.triggerSync() }
        }
    }
}
