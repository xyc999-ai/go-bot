import ExpoModulesCore
import Network
import SafariServices

public class OAuthLoopbackModule: Module {
  private var receiver: LoopbackReceiver?
  public func definition() -> ModuleDefinition {
    Name("GoBotOAuthLoopback")
    AsyncFunction("open") { (id: String, config: [String: String], promise: Promise) in
      guard self.receiver == nil else { promise.reject("RECEIVER_BUSY", "RECEIVER_BUSY"); return }
      let receiver = LoopbackReceiver(id: id, config: config, promise: promise) { self.receiver = nil }
      self.receiver = receiver
      receiver.start(presenter: self.appContext?.utilities?.currentViewController())
    }.runOnQueue(.main)
    AsyncFunction("cancel") { (id: String) in
      if self.receiver?.id == id { self.receiver?.finish(error: "CANCELLED") }
    }.runOnQueue(.main)
    OnAppEntersBackground { DispatchQueue.main.async { self.receiver?.beginBackgroundGrace() } }
    OnAppEntersForeground { DispatchQueue.main.async { self.receiver?.endBackgroundGrace() } }
    OnDestroy { DispatchQueue.main.async { self.receiver?.finish(error: "CANCELLED") } }
  }
}

// Main-queue ownership keeps listener/browser cancellation and completion atomic.
private final class LoopbackReceiver: NSObject, SFSafariViewControllerDelegate {
  let id: String
  let config: [String: String]
  let promise: Promise
  let disposed: () -> Void
  var listeners: [NWListener] = []
  var connections: [NWConnection] = []
  var browser: SFSafariViewController?
  var timeout: DispatchWorkItem?
  var backgroundTask: UIBackgroundTaskIdentifier = .invalid
  var complete = false
  var consumed = false
  var presenting = false
  var redirect: URL?
  init(id: String, config: [String: String], promise: Promise, disposed: @escaping () -> Void) {
    self.id = id; self.config = config; self.promise = promise; self.disposed = disposed
  }
  func start(presenter: UIViewController?) {
    guard let login = URL(string: config["authorizationUrl"] ?? ""), login.scheme == "https", login.user == nil, login.password == nil,
      let target = URL(string: config["redirectUri"] ?? ""), target.scheme == "http", let host = target.host,
      ["localhost", "127.0.0.1", "[::1]", "::1"].contains(host), let port = target.port, port > 0, port <= 65535,
      target.query == nil, target.fragment == nil, target.user == nil, target.password == nil,
      let state = config["state"], !state.isEmpty, let query = URLComponents(url: login, resolvingAgainstBaseURL: false)?.queryItems,
      query.filter({$0.name == "state"}).map({$0.value}) == [state],
      query.filter({$0.name == "redirect_uri"}).map({$0.value}) == [target.absoluteString], let presenter else {
      finish(error: "INVALID_CALLBACK"); return
    }
    redirect = target
    let hosts = host == "localhost" ? ["127.0.0.1", "::1"] : [host.replacingOccurrences(of: "[", with: "").replacingOccurrences(of: "]", with: "")]
    var remaining = hosts.count
    for address in hosts {
      do {
        let parameters = NWParameters.tcp
        parameters.requiredLocalEndpoint = .hostPort(host: NWEndpoint.Host(address), port: NWEndpoint.Port(rawValue: UInt16(port))!)
        let listener = try NWListener(using: parameters)
        listeners.append(listener)
        listener.stateUpdateHandler = { [weak self] status in
          guard let self, !self.complete else { return }
          switch status {
          case .ready:
            remaining -= 1
            if remaining == 0 {
              let browser = SFSafariViewController(url: login)
              browser.delegate = self; self.browser = browser
              self.presenting = true
              presenter.present(browser, animated: true) {
                self.presenting = false
                if self.complete { browser.dismiss(animated: true) }
              }
            }
          case .failed(let error):
            if case .posix(let code) = error, code == .EADDRINUSE { self.finish(error: "PORT_IN_USE") }
            else { self.finish(error: "LISTENER_FAILED") }
          default: break
          }
        }
        listener.newConnectionHandler = { [weak self] connection in
          guard let self, !self.complete, self.connections.count < 16 else { connection.cancel(); return }
          self.connections.append(connection)
          connection.start(queue: .main)
          self.read(connection, data: Data())
          DispatchQueue.main.asyncAfter(deadline: .now() + 10) { [weak self, weak connection] in
            if let connection { self?.drop(connection) }
          }
        }
        listener.start(queue: .main)
      } catch { finish(error: "LISTENER_FAILED"); return }
    }
    let expiry = DispatchWorkItem { [weak self] in self?.finish(error: "TIMED_OUT") }
    timeout = expiry; DispatchQueue.main.asyncAfter(deadline: .now() + 300, execute: expiry)
  }
  func drop(_ connection: NWConnection) {
    connection.cancel(); connections.removeAll { $0 === connection }
  }
  func read(_ connection: NWConnection, data: Data) {
    connection.receive(minimumIncompleteLength: 1, maximumLength: 16384 - data.count) { [weak self] chunk, _, ended, error in
      guard let self, !self.complete else { connection.cancel(); return }
      var buffer = data; if let chunk { buffer.append(chunk) }
      if let text = String(data: buffer, encoding: .utf8), text.contains("\r\n\r\n") { self.respond(connection, request: text) }
      else if buffer.count < 16384 && !ended && error == nil { self.read(connection, data: buffer) }
      else { self.drop(connection) }
    }
  }
  func respond(_ connection: NWConnection, request: String) {
    let lines = request.components(separatedBy: "\r\n")
    let first = (lines.first ?? "").components(separatedBy: " ")
    var headers: [String: [String]] = [:]
    for line in lines.dropFirst() {
      guard let split = line.firstIndex(of: ":") else { continue }
      headers[String(line[..<split]).lowercased(), default: []].append(String(line[line.index(after: split)...]).trimmingCharacters(in: .whitespaces))
    }
    let expected = URLComponents(url: redirect!, resolvingAgainstBaseURL: false)!
    let authority = (expected.host!.contains(":") && !expected.host!.hasPrefix("[") ? "[\(expected.host!)]" : expected.host!) + ":\(expected.port!)"
    var callback: String?
    if !consumed, first.count == 3, first[0] == "GET", first[1].hasPrefix("/"), !first[1].hasPrefix("//"),
      headers["host"] == [authority], headers["origin"] == nil,
      let value = URLComponents(string: "http://" + authority + first[1]), value.path == (expected.path.isEmpty ? "/" : expected.path), value.fragment == nil {
      let query = value.queryItems ?? []
      let states = query.filter {$0.name == "state"}.map {$0.value ?? ""}
      let codes = query.filter {$0.name == "code"}.map {$0.value ?? ""}
      let errors = query.filter {$0.name == "error"}.map {$0.value ?? ""}
      if states == [config["state"]!] && ((codes.count == 1 && !codes[0].isEmpty && errors.isEmpty) || (errors.count == 1 && !errors[0].isEmpty && codes.isEmpty)) {
        callback = value.string; consumed = true
      }
    }
    let body = "<!doctype html><meta name=\"viewport\" content=\"width=device-width\"><title>Go Bot</title><p>" + (callback == nil ? "This request does not match the active authorization." : "Authorization received. Return to Go Bot.") + "</p>"
    let response = "HTTP/1.1 \(callback == nil ? "400 Bad Request" : "200 OK")\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: \(body.utf8.count)\r\nCache-Control: no-store\r\nReferrer-Policy: no-referrer\r\nContent-Security-Policy: default-src 'none'\r\nConnection: close\r\n\r\n" + body
    connection.send(content: response.data(using: .utf8), completion: .contentProcessed { [weak self] _ in
      guard let self else { return }
      self.drop(connection)
      if let callback { self.finish(url: callback) }
    })
  }
  func beginBackgroundGrace() {
    guard backgroundTask == .invalid, !complete else { return }
    backgroundTask = UIApplication.shared.beginBackgroundTask(withName: "GoBotOAuthCallback") { [weak self] in self?.finish(error: "TIMED_OUT") }
  }
  func endBackgroundGrace() {
    if backgroundTask != .invalid { UIApplication.shared.endBackgroundTask(backgroundTask); backgroundTask = .invalid }
  }
  func safariViewControllerDidFinish(_ controller: SFSafariViewController) { finish(error: "CANCELLED") }
  func finish(error: String? = nil, url: String? = nil) {
    guard !complete else { return }; complete = true
    endBackgroundGrace()
    timeout?.cancel(); listeners.forEach {$0.cancel()}; connections.forEach {$0.cancel()}
    listeners.removeAll(); connections.removeAll()
    if !presenting { browser?.dismiss(animated: true) }
    browser = nil; disposed()
    if let url { promise.resolve(url) } else { promise.reject(error ?? "LISTENER_FAILED", error ?? "LISTENER_FAILED") }
  }
}
