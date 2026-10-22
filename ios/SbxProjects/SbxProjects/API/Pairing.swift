import Foundation
import Network
import Security

/// A Mac this iPhone is paired with. The token lives in the Keychain;
/// the rest in UserDefaults, through `PairedServers`.
nonisolated struct PairedServer: Codable, Sendable, Hashable, Identifiable {
    /// The first 16 hex digits of the certificate's fingerprint, which the
    /// Mac also advertises over Bonjour.
    let id: String
    var name: String
    let fingerprint: String
    var address: ServerAddress
}

/// The Macs this iPhone is paired with, in the order they were paired, and
/// which one the app shows.
nonisolated enum PairedServers {
    private static let listKey = "pairedServers"
    private static let currentKey = "currentServer"
    /// Where the one Mac lived before the app could pair with several.
    private static let legacyKey = "pairedServer"

    static func load() -> (servers: [PairedServer], current: String?) {
        let d = UserDefaults.standard
        if let data = d.data(forKey: listKey), let servers = try? JSONDecoder().decode([PairedServer].self, from: data) {
            return (servers, d.string(forKey: currentKey))
        }
        guard let data = d.data(forKey: legacyKey), let s = try? JSONDecoder().decode(PairedServer.self, from: data) else {
            return ([], nil)
        }
        save([s], current: s.id)
        d.removeObject(forKey: legacyKey)
        return ([s], s.id)
    }

    static func save(_ servers: [PairedServer], current: String?) {
        let d = UserDefaults.standard
        d.set(try? JSONEncoder().encode(servers), forKey: listKey)
        d.set(current, forKey: currentKey)
    }
}

nonisolated enum Keychain {
    private static let service = "sbx-projects"

    static func token(for serverID: String) -> String? {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: serverID,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func setToken(_ token: String, for serverID: String) {
        deleteToken(for: serverID)
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: serverID,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            kSecValueData as String: Data(token.utf8),
        ]
        SecItemAdd(q as CFDictionary, nil)
    }

    static func deleteToken(for serverID: String) {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: serverID,
        ]
        SecItemDelete(q as CFDictionary)
    }
}

/// What the pairing QR code carries:
/// sbxp://pair?v=1&name=…&fp=…&hosts=a,b&port=7443&code=…
nonisolated struct PairingLink: Sendable {
    let name: String
    let fingerprint: String
    let hosts: [String]
    let port: Int
    let code: String

    init?(string: String) {
        guard let c = URLComponents(string: string), c.scheme == "sbxp", c.host == "pair" else { return nil }
        var q: [String: String] = [:]
        for item in c.queryItems ?? [] {
            q[item.name] = item.value
        }
        guard let fp = q["fp"], fp.count == 64, let code = q["code"], let port = Int(q["port"] ?? "") else { return nil }
        name = q["name"] ?? "your Mac"
        fingerprint = fp.lowercased()
        hosts = (q["hosts"] ?? "").split(separator: ",").map(String.init)
        self.port = port
        self.code = code
    }
}

/// Finds Macs running sbx-projects api on the local network.
@MainActor @Observable
final class MacBrowser {
    nonisolated static let serviceType = "_sbxp._tcp"

    struct Found: Identifiable, Hashable {
        /// The Bonjour service name, which is the Mac's name.
        let id: String
        let serverID: String?
        let endpoint: NWEndpoint
        var name: String { id }
    }

    private(set) var found: [Found] = []
    private(set) var error: String?
    private var browser: NWBrowser?

    func start() {
        guard browser == nil else { return }
        let b = NWBrowser(for: .bonjourWithTXTRecord(type: Self.serviceType, domain: nil), using: NWParameters())
        b.browseResultsChangedHandler = { [weak self] results, _ in
            MainActor.assumeIsolated { self?.update(results) }
        }
        b.stateUpdateHandler = { [weak self] state in
            MainActor.assumeIsolated {
                if case .failed(let e) = state {
                    self?.error = e.localizedDescription
                } else if case .waiting(let e) = state {
                    // Usually Local Network access being turned off.
                    self?.error = e.localizedDescription
                }
            }
        }
        b.start(queue: .main)
        browser = b
    }

    func stop() {
        browser?.cancel()
        browser = nil
        found = []
    }

    private func update(_ results: Set<NWBrowser.Result>) {
        error = nil
        found = results.compactMap { r in
            guard case .service(let name, _, _, _) = r.endpoint else { return nil }
            var id: String?
            if case .bonjour(let txt) = r.metadata {
                id = txt["id"]
            }
            return Found(id: name, serverID: id, endpoint: r.endpoint)
        }
        .sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }

    /// Looks up the IPv4 address and port behind a Bonjour service.
    nonisolated static func resolve(_ endpoint: NWEndpoint, timeout: Duration = .seconds(5)) async throws -> ServerAddress {
        let params = NWParameters.tcp
        if let ip = params.defaultProtocolStack.internetProtocol as? NWProtocolIP.Options {
            ip.version = .v4
        }
        let conn = NWConnection(to: endpoint, using: params)
        let once = Once()
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (cont: CheckedContinuation<ServerAddress, Error>) in
                conn.stateUpdateHandler = { state in
                    switch state {
                    case .ready:
                        if case .hostPort(let host, let port)? = conn.currentPath?.remoteEndpoint {
                            once.run { cont.resume(returning: ServerAddress(host: hostString(host), port: Int(port.rawValue))) }
                        } else {
                            once.run { cont.resume(throwing: URLError(.cannotFindHost)) }
                        }
                        conn.cancel()
                    case .failed(let e), .waiting(let e):
                        once.run { cont.resume(throwing: e) }
                        conn.cancel()
                    default:
                        break
                    }
                }
                conn.start(queue: .global())
                DispatchQueue.global().asyncAfter(deadline: .now() + Double(timeout.components.seconds)) {
                    once.run { cont.resume(throwing: URLError(.timedOut)) }
                    conn.cancel()
                }
            }
        } onCancel: {
            conn.cancel()
        }
    }

    nonisolated private static func hostString(_ host: NWEndpoint.Host) -> String {
        switch host {
        // From the bytes, as the description can carry an interface ("%en0").
        case .ipv4(let a): a.rawValue.map(String.init).joined(separator: ".")
        case .ipv6(let a): "\(a)"
        case .name(let n, _): n
        @unknown default: "\(host)"
        }
    }
}

/// Runs a closure at most once, from any thread.
nonisolated final class Once: @unchecked Sendable {
    private let lock = NSLock()
    private var done = false

    func run(_ fn: () -> Void) {
        let first = lock.withLock {
            defer { done = true }
            return !done
        }
        if first { fn() }
    }
}
