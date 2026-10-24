import CryptoKit
import Foundation

nonisolated struct ServerAddress: Codable, Sendable, Hashable {
    var host: String
    var port: Int

    var baseURL: URL {
        let h = host.contains(":") ? "[\(host)]" : host
        return URL(string: "https://\(h):\(port)")!
    }

    var display: String { "\(host):\(port)" }
}

nonisolated enum APIError: LocalizedError {
    /// The Mac answered with an error, whose message is written for people.
    case server(status: Int, message: String)
    /// The device's token isn't accepted any more.
    case signedOut(String)
    /// The certificate isn't the one the device paired with.
    case wrongCertificate
    case badResponse

    var errorDescription: String? {
        switch self {
        case .server(_, let message), .signedOut(let message): message
        case .wrongCertificate: "The Mac's certificate has changed, so the connection was refused. Pair again if you reinstalled sbx-projects."
        case .badResponse: "The Mac sent a response the app doesn't understand. Update sbx-projects on the Mac and the app."
        }
    }
}

/// Talks to sbx-projects api on one Mac. With a fingerprint, the
/// connection only succeeds when the server's certificate matches it.
/// Without one (only while pairing by code), any certificate is accepted
/// and its fingerprint is kept in `seenFingerprint` to be pinned.
nonisolated final class APIClient: Sendable {
    let address: ServerAddress
    private let token: String?
    private let pin: PinningDelegate
    private let session: URLSession

    init(address: ServerAddress, fingerprint: String?, token: String?) {
        self.address = address
        self.token = token
        pin = PinningDelegate(expected: fingerprint)
        let config = URLSessionConfiguration.ephemeral
        config.waitsForConnectivity = false
        config.timeoutIntervalForRequest = 20
        session = URLSession(configuration: config, delegate: pin, delegateQueue: nil)
    }

    deinit {
        session.invalidateAndCancel()
    }

    var seenFingerprint: String? { pin.seen }

    // MARK: Requests

    func serverInfo(timeout: TimeInterval = 20) async throws -> ServerInfo {
        var req = request("GET", "/v1/server")
        req.timeoutInterval = timeout
        return try await perform(req)
    }

    func pair(code: String, device: String) async throws -> PairResponse {
        try await perform(request("POST", "/v1/pair", body: ["code": code, "device": device]))
    }

    func projects() async throws -> [Project] {
        try await perform(request("GET", "/v1/projects"))
    }

    func sandboxes() async throws -> [Sandbox] {
        try await perform(request("GET", "/v1/sandboxes"))
    }

    func sandbox(_ name: String) async throws -> SandboxDetail {
        try await perform(request("GET", "/v1/sandboxes/\(escape(name))"))
    }

    func reply(_ name: String) async throws -> Reply {
        try await perform(request("GET", "/v1/sandboxes/\(escape(name))/reply"))
    }

    func markSeen(_ name: String) async throws -> Sandbox {
        try await perform(request("POST", "/v1/sandboxes/\(escape(name))/seen"))
    }

    func diff(_ name: String) async throws -> Diff {
        var req = request("GET", "/v1/sandboxes/\(escape(name))/diff")
        req.timeoutInterval = 40
        return try await perform(req)
    }

    func options(project: Int64) async throws -> Options {
        try await perform(request("GET", "/v1/options?project=\(project)"))
    }

    func send(_ text: String, to name: String) async throws -> Sandbox {
        try await perform(request("POST", "/v1/sandboxes/\(escape(name))/messages", body: ["text": text]))
    }

    /// Starts one of the actions with no input: "pr", "stop", "resume" or
    /// "checkout".
    func act(_ action: String, on name: String) async throws -> Sandbox {
        try await perform(request("POST", "/v1/sandboxes/\(escape(name))/\(action)"))
    }

    func publishPort(_ r: PublishPortRequest, on name: String) async throws -> Sandbox {
        try await perform(request("POST", "/v1/sandboxes/\(escape(name))/ports", body: r))
    }

    func unpublishPort(_ port: Port, on name: String) async throws -> Sandbox {
        try await perform(request("POST", "/v1/sandboxes/\(escape(name))/ports/unpublish", body: port))
    }

    func remove(_ name: String) async throws -> Sandbox {
        try await perform(request("DELETE", "/v1/sandboxes/\(escape(name))"))
    }

    func create(_ r: CreateRequest) async throws -> Sandbox {
        try await perform(request("POST", "/v1/sandboxes", body: r))
    }

    /// Answers once the project exists, which for a clone can take minutes.
    func newProject(_ r: NewProjectRequest) async throws -> NewProjectResult {
        var req = request("POST", "/v1/projects", body: r)
        req.timeoutInterval = 15 * 60
        return try await perform(req)
    }

    func projectOptions() async throws -> ProjectOptions {
        try await perform(request("GET", "/v1/project-options"))
    }

    /// Opens the agent's terminal, as Enter attaches to it in the TUI.
    /// Output arrives as binary frames; send keystrokes as binary frames and
    /// resizes as JSON text. The session's delegate pins the certificate.
    func terminal(_ name: String, cols: Int, rows: Int) -> URLSessionWebSocketTask {
        var req = request("GET", "/v1/sandboxes/\(escape(name))/terminal?cols=\(cols)&rows=\(rows)")
        req.setValue(Self.terminalProtocol, forHTTPHeaderField: "Sec-WebSocket-Protocol")
        // An idle terminal is quiet for as long as the agent is; the pings
        // both ways are what keep it open.
        req.timeoutInterval = 90
        return session.webSocketTask(with: req)
    }

    static let terminalProtocol = "sbxp-terminal"

    /// The change stream. Each event is one "data:" line, so reading line
    /// by line is enough; `lines` drops the blank lines between events.
    func events() -> AsyncThrowingStream<ServerEvent, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    var req = self.request("GET", "/v1/events")
                    // The server pings every 15 seconds, so a longer silence
                    // means the connection is gone.
                    req.timeoutInterval = 45
                    // The delegate goes in again for this task: bytes(for:)
                    // doesn't ask the session's delegate about the server's
                    // certificate, so without it the self-signed one fails.
                    let (bytes, response) = try await self.session.bytes(for: req, delegate: self.pin)
                    try self.check(response, data: nil)
                    let decoder = Self.decoder()
                    for try await line in bytes.lines {
                        guard line.hasPrefix("data: ") else { continue }
                        let event = try decoder.decode(ServerEvent.self, from: Data(line.dropFirst(6).utf8))
                        continuation.yield(event)
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: self.mapError(error))
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // MARK: Plumbing

    private func request(_ method: String, _ path: String) -> URLRequest {
        var req = URLRequest(url: URL(string: path, relativeTo: address.baseURL)!)
        req.httpMethod = method
        if let token {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return req
    }

    private func request(_ method: String, _ path: String, body: some Encodable) -> URLRequest {
        var req = request(method, path)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try? JSONEncoder().encode(body)
        return req
    }

    private func perform<T: Decodable>(_ req: URLRequest) async throws -> T {
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: req)
        } catch {
            throw mapError(error)
        }
        try check(response, data: data)
        do {
            return try Self.decoder().decode(T.self, from: data)
        } catch {
            throw APIError.badResponse
        }
    }

    private func check(_ response: URLResponse, data: Data?) throws {
        guard let http = response as? HTTPURLResponse else { throw APIError.badResponse }
        guard !(200..<300).contains(http.statusCode) else { return }
        var message = HTTPURLResponse.localizedString(forStatusCode: http.statusCode)
        if let data, let body = try? JSONDecoder().decode([String: String].self, from: data), let m = body["error"] {
            message = m
        }
        if http.statusCode == 401 && token != nil {
            throw APIError.signedOut(message)
        }
        throw APIError.server(status: http.statusCode, message: message)
    }

    private func mapError(_ error: Error) -> Error {
        if let e = error as? URLError, e.code == .cancelled || e.code == .serverCertificateUntrusted, pin.mismatched {
            return APIError.wrongCertificate
        }
        return error
    }

    private func escape(_ name: String) -> String {
        name.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? name
    }

    private static func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .secondsSince1970
        return d
    }
}

/// Checks the server's certificate against the pinned fingerprint, the
/// SHA-256 of its DER encoding in lowercase hex, as the Mac prints it.
/// It answers both the session's challenges and a task's, since some
/// tasks only ask their own delegate.
nonisolated final class PinningDelegate: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    private let expected: String?
    private let lock = NSLock()
    private var _seen: String?
    private var _mismatched = false

    init(expected: String?) {
        self.expected = expected
    }

    var seen: String? { lock.withLock { _seen } }
    var mismatched: Bool { lock.withLock { _mismatched } }

    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge) async
        -> (URLSession.AuthChallengeDisposition, URLCredential?)
    {
        evaluate(challenge)
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didReceive challenge: URLAuthenticationChallenge) async
        -> (URLSession.AuthChallengeDisposition, URLCredential?)
    {
        evaluate(challenge)
    }

    private func evaluate(_ challenge: URLAuthenticationChallenge) -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let trust = challenge.protectionSpace.serverTrust,
              let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate],
              let leaf = chain.first
        else {
            return (.performDefaultHandling, nil)
        }
        let der = SecCertificateCopyData(leaf) as Data
        let fp = SHA256.hash(data: der).map { String(format: "%02x", $0) }.joined()
        let ok = expected == nil || expected == fp
        lock.withLock {
            _seen = fp
            _mismatched = !ok
        }
        return ok ? (.useCredential, URLCredential(trust: trust)) : (.cancelAuthenticationChallenge, nil)
    }
}

/// Formats a fingerprint the way sbx-projects api prints it.
nonisolated func formatFingerprint(_ fp: String, pairs: Int = 16) -> String {
    var parts: [String] = []
    var i = fp.startIndex
    while i < fp.endIndex, parts.count < pairs {
        let j = fp.index(i, offsetBy: 2, limitedBy: fp.endIndex) ?? fp.endIndex
        parts.append(fp[i..<j].uppercased())
        i = j
    }
    return parts.joined(separator: ":")
}
