import Foundation
import Network
import Observation
import UIKit

/// Everything the app shows comes from here. While the app is in the
/// foreground it keeps the Mac's event stream open and applies each change
/// as it arrives.
@MainActor @Observable
final class AppModel {
    enum Connection: Equatable {
        case connecting
        case live
        case offline(String)
        /// The Mac no longer accepts this device's token.
        case signedOut(String)
    }

    struct Toast: Identifiable, Equatable {
        let id = UUID()
        let message: String
        let ok: Bool
    }

    private(set) var server: PairedServer?
    private(set) var connection: Connection = .connecting
    private(set) var projects: [Project] = []
    private(set) var sandboxes: [String: Sandbox] = [:]
    /// Why sbx couldn't list sandboxes on the Mac, if it couldn't.
    private(set) var listError: String?
    private(set) var pairing = false
    var pairingError: String?
    var toast: Toast?

    let browser = MacBrowser()

    private var client: APIClient?
    private var streamTask: Task<Void, Never>?
    /// Sandboxes with an action started from this device, whose outcome is
    /// shown as a toast.
    private var pending: Set<String> = []

    init() {
        server = PairedServer.load()
        makeClient()
    }

    private func makeClient() {
        guard let server, let token = Keychain.token(for: server.id) else {
            client = nil
            return
        }
        client = APIClient(address: server.address, fingerprint: server.fingerprint, token: token)
    }

    // MARK: Connection

    func connect() {
        guard client != nil, streamTask == nil else { return }
        streamTask = Task { await runStream() }
    }

    func disconnect() {
        streamTask?.cancel()
        streamTask = nil
    }

    /// Reconnects, which starts again from a fresh snapshot.
    func refresh() async {
        disconnect()
        connection = .connecting
        connect()
        // Give pull to refresh something to wait on.
        for _ in 0..<20 where connection != .live {
            try? await Task.sleep(for: .milliseconds(100))
        }
    }

    private func runStream() async {
        var failures = 0
        while !Task.isCancelled, let client {
            if connection != .live {
                connection = .connecting
            }
            do {
                for try await event in client.events() {
                    apply(event)
                    failures = 0
                }
            } catch APIError.signedOut(let message) {
                connection = .signedOut(message)
                streamTask = nil
                return
            } catch {
                if Task.isCancelled { return }
                failures += 1
                connection = .offline(error.localizedDescription)
                // The Mac may have a new address on the network.
                if failures >= 2, await rediscover() {
                    continue
                }
            }
            if Task.isCancelled { return }
            try? await Task.sleep(for: .seconds(min(2 * max(failures, 1), 10)))
        }
    }

    private func apply(_ event: ServerEvent) {
        switch event.type {
        case "snapshot":
            projects = event.projects ?? []
            sandboxes = Dictionary((event.sandboxes ?? []).map { ($0.name, $0) }, uniquingKeysWith: { _, new in new })
            listError = event.listError
            connection = .live
        case "sandbox":
            if let s = event.sandbox {
                sandboxes[s.name] = s
            }
        case "removed":
            if let name = event.name {
                sandboxes[name] = nil
            }
        case "projects":
            projects = event.projects ?? projects
        case "status":
            // Sent when listing on the Mac starts or stops failing; until it
            // clears, the states shown are the last ones seen.
            listError = event.listError
        case "action":
            if let name = event.name, pending.remove(name) != nil || event.ok != true {
                toast = Toast(message: event.message ?? "", ok: event.ok == true)
            }
        default:
            break
        }
    }

    /// Looks for the paired Mac on the network and switches to its current
    /// address. Returns whether the address changed.
    private func rediscover() async -> Bool {
        guard var server else { return false }
        browser.start()
        defer { browser.stop() }
        for _ in 0..<10 {
            if let mac = browser.found.first(where: { $0.serverID == server.id }) {
                guard let address = try? await MacBrowser.resolve(mac.endpoint), address != server.address else { return false }
                server.address = address
                server.save()
                self.server = server
                makeClient()
                return true
            }
            try? await Task.sleep(for: .milliseconds(500))
        }
        return false
    }

    // MARK: Pairing

    var deviceName: String { UIDevice.current.name }

    /// Pairs with the Mac whose QR code was scanned.
    func pair(link: String) async {
        guard let p = PairingLink(string: link) else {
            pairingError = "That isn't a pairing code from sbx-projects api."
            return
        }
        pairing = true
        defer { pairing = false }
        pairingError = nil
        for host in p.hosts {
            let address = ServerAddress(host: host, port: p.port)
            let c = APIClient(address: address, fingerprint: p.fingerprint, token: nil)
            guard (try? await c.serverInfo(timeout: 4)) != nil else { continue }
            await pair(client: c, fingerprint: p.fingerprint, code: p.code)
            return
        }
        pairingError = "Couldn't reach \(p.name). Check that your iPhone is on the same Wi-Fi network as the Mac, and that Local Network access is on for sbx-projects in Settings."
    }

    /// Pairs with a Mac found on the network, using the code shown in its
    /// terminal. The fingerprint is the one the person was shown.
    func pair(address: ServerAddress, fingerprint: String, code: String) async {
        pairing = true
        defer { pairing = false }
        pairingError = nil
        await pair(client: APIClient(address: address, fingerprint: fingerprint, token: nil), fingerprint: fingerprint, code: code)
    }

    private func pair(client c: APIClient, fingerprint: String, code: String) async {
        do {
            let r = try await c.pair(code: code, device: deviceName)
            guard fingerprint.hasPrefix(r.server.id) else { throw APIError.wrongCertificate }
            Keychain.setToken(r.token, for: r.server.id)
            let s = PairedServer(id: r.server.id, name: r.server.name, fingerprint: fingerprint, address: c.address)
            s.save()
            disconnect()
            server = s
            makeClient()
            connect()
        } catch {
            pairingError = error.localizedDescription
        }
    }

    /// Forgets the Mac on this device. The Mac keeps the device listed until
    /// sbx-projects api devices rm removes it.
    func forget() {
        disconnect()
        if let server {
            Keychain.deleteToken(for: server.id)
        }
        PairedServer.clear()
        server = nil
        client = nil
        projects = []
        sandboxes = [:]
        connection = .connecting
    }

    // MARK: Reads

    private func api() throws -> APIClient {
        guard let client else { throw APIError.signedOut("This iPhone isn't paired with a Mac.") }
        return client
    }

    func detail(_ name: String) async throws -> SandboxDetail {
        try await api().sandbox(name)
    }

    func reply(_ name: String) async throws -> Reply {
        try await api().reply(name)
    }

    func diff(_ name: String) async throws -> Diff {
        try await api().diff(name)
    }

    func options(project: Int64) async throws -> Options {
        try await api().options(project: project)
    }

    func markSeen(_ name: String) async {
        if let s = try? await api().markSeen(name) {
            sandboxes[s.name] = s
        }
    }

    // MARK: Actions

    /// Runs an action, showing its result in the list right away. Returns
    /// whether the Mac accepted it; how it ends arrives as a toast.
    @discardableResult
    private func run(_ name: String, _ fn: (APIClient) async throws -> Sandbox) async -> Bool {
        do {
            pending.insert(name)
            let s = try await fn(api())
            sandboxes[s.name] = s
            return true
        } catch {
            pending.remove(name)
            toast = Toast(message: error.localizedDescription, ok: false)
            return false
        }
    }

    func send(_ text: String, to name: String) async -> Bool {
        await run(name) { try await $0.send(text, to: name) }
    }

    func openPR(_ name: String) async {
        await run(name) { try await $0.act("pr", on: name) }
    }

    func stop(_ name: String) async {
        await run(name) { try await $0.act("stop", on: name) }
    }

    func resume(_ name: String) async {
        await run(name) { try await $0.act("resume", on: name) }
    }

    func checkout(_ name: String) async {
        await run(name) { try await $0.act("checkout", on: name) }
    }

    func remove(_ name: String) async {
        await run(name) { try await $0.remove(name) }
    }

    func create(_ r: CreateRequest) async throws -> Sandbox {
        let s = try await api().create(r)
        pending.insert(s.name)
        sandboxes[s.name] = s
        return s
    }

    // MARK: Lists

    func project(_ id: Int64) -> Project? {
        projects.first { $0.id == id }
    }

    /// The project's sandboxes in the TUI's order: by group, then newest.
    func sandboxes(in project: Project, group: StatusGroup?, matching query: String) -> [Sandbox] {
        let q = query.trimmingCharacters(in: .whitespaces)
        return sandboxes.values
            .filter { $0.projectId == project.id }
            .filter { group == nil || $0.group == group }
            .filter { q.isEmpty || $0.title.localizedCaseInsensitiveContains(q) || $0.name.localizedCaseInsensitiveContains(q) || ($0.branch ?? "").localizedCaseInsensitiveContains(q) }
            .sorted { a, b in
                let ga = StatusGroup.allCases.firstIndex(of: a.group)!, gb = StatusGroup.allCases.firstIndex(of: b.group)!
                if ga != gb { return ga < gb }
                if a.createdAt != b.createdAt { return a.createdAt > b.createdAt }
                return a.name < b.name
            }
    }

    func count(_ group: StatusGroup?) -> Int {
        group == nil ? sandboxes.count : sandboxes.values.filter { $0.group == group }.count
    }
}
