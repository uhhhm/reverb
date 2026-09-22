import ReverbAPI
import SwiftUI
import UIKit

/// Pairing from the phone's side: the phone is the redeemer. The code proves
/// possession through the core's challenge-response and never crosses the
/// network, whether it arrives in a scanned QR code or typed.
@MainActor
final class PairingModel: ObservableObject {
    @Published var isPresented = false
    /// A pairing link scanned by the system Camera, waiting to be redeemed.
    @Published var scannedPayload: String?

    func scanned(_ payload: String) {
        guard payload.hasPrefix("reverb://pair") else { return }
        scannedPayload = payload
        isPresented = true
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
                            Task { await redeem(payload: payload) }
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
                if busy {
                    ProgressView("Pairing…")
                }
                if let message {
                    Text(message)
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("pair.error")
                }
            }
            .navigationTitle("Pair a device")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
            .task(id: pairing.scannedPayload) {
                if let payload = pairing.scannedPayload {
                    pairing.scannedPayload = nil
                    await redeem(payload: payload)
                }
            }
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
            case .ok: await paired(client)
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
            case .ok: await paired(client)
            case .badRequest: message = "Enter the code the computer shows."
            default: message = "Pairing failed. Check the code, and that both devices are on the same network or VPN."
            }
        } catch {
            message = error.localizedDescription
        }
    }

    private func paired(_ client: Client) async {
        message = nil
        _ = try? await client.triggerSync()
        dismiss()
    }
}
