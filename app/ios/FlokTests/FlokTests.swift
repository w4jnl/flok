import Foundation
import Testing
@testable import Flok

struct WireTests {
    @Test func decodesTheInstancesFrame() throws {
        let json = """
        {"type":"instances","instances":[{"name":"Home","online":true,"since":"2026-10-10T22:39:28.350444+02:00","version":"0.6.0","hostname":"mac.local",
         "snap":{"updated_at":"2026-10-10T20:40:00Z","unseen":2,
          "agents":[{"pane_id":"%3","session_id":"$1","session_name":"Hugo","window_id":"@2","name":"flok","kind":"claude","state":"blocked","reason":"permission:Bash","unseen":1,"state_since":"2026-10-10T20:39:00.1Z","has_hooks":true},
                    {"pane_id":"%1","host":"beta","session_id":"$0","session_name":"Remote","name":"docs","kind":"claude","state":"done","unseen":1,"state_since":"2026-10-10T20:39:00Z","has_hooks":false},
                    {"pane_id":"%9","session_id":"$2","session_name":"x","name":"new","kind":"claude","state":"whatever","unseen":0}],
          "sessions":[{"id":"$1","name":"Hugo","rollup":"blocked","agents":1}],
          "hosts":[{"name":"beta","mode":"full","state":"connected","agents":1,"pending":1,"front":false}]}}]}
        """
        let f = try Wire.decoder.decode(Frame.self, from: Data(json.utf8))
        #expect(f.type == "instances")
        let inst = try #require(f.instances?.first)
        #expect(inst.name == "Home" && inst.online && inst.unseen == 2)
        #expect(inst.agents.count == 3)
        #expect(inst.agents[0].paneRef == "%3" && inst.agents[0].state == .blocked)
        #expect(inst.agents[1].paneRef == "beta:%1" && inst.agents[1].state == .done)
        #expect(inst.agents[2].state == .unknown, "an unknown state does not break decoding")
        #expect(StateStyle.label(inst.agents[0]) == "needs you: perm:Bash")
        #expect(StateStyle.label(inst.agents[1]) == "done")
    }

    @Test func decodesEventsAndScreens() throws {
        let ev = try Wire.decoder.decode(Frame.self, from: Data(#"{"type":"event","instance":"Home","event":{"pane":"%3","agent":"flok","kind":"blocked","reason":"permission:Bash","at":"2026-10-10T20:45:04.777931+02:00"}}"#.utf8))
        #expect(ev.event?.kind == "blocked" && ev.instance == "Home")
        var utc = Calendar(identifier: .gregorian)
        utc.timeZone = TimeZone(identifier: "UTC")!
        let c = utc.dateComponents([.year, .month, .day, .hour, .minute, .second, .nanosecond], from: ev.event!.at)
        #expect(c.year == 2026 && c.month == 10 && c.day == 10 && c.hour == 18 && c.minute == 45 && c.second == 4, "the +02:00 offset is applied")
        #expect(abs(Double(c.nanosecond ?? 0) - 777_931_000) < 1_000_000, "fractional seconds survive")
        let sc = try Wire.decoder.decode(Frame.self, from: Data(#"{"type":"screen","instance":"Home","screen":{"pane":"beta:%1","text":"hello\n"}}"#.utf8))
        #expect(sc.screen?.pane == "beta:%1" && sc.screen?.text == "hello\n")
    }

    @Test func encodesBodiesInSnakeCase() throws {
        let data = try Wire.encoder.encode(AnswerBody(instance: "Home", pane: "beta:%1", text: "y", keys: ["Enter"]))
        let s = String(decoding: data, as: UTF8.self)
        #expect(s.contains(#""instance":"Home""#) && s.contains(#""keys":["Enter"]"#))
        let sub = String(decoding: try Wire.encoder.encode(SubscribeFrame(instance: "Home", pane: "%3", on: true)), as: UTF8.self)
        #expect(sub.contains(#""type":"subscribe""#) && sub.contains(#""on":true"#))
    }
}

struct URLTests {
    @Test func normalizesWhatTheUserTypes() {
        #expect(RelayURL.base("flok.example.net")?.absoluteString == "https://flok.example.net")
        #expect(RelayURL.base("https://flok.example.net/")?.absoluteString == "https://flok.example.net")
        #expect(RelayURL.base("wss://flok.example.net/link")?.absoluteString == "https://flok.example.net")
        #expect(RelayURL.base(" http://127.0.0.1:8080/api/ws ")?.absoluteString == "http://127.0.0.1:8080")
        #expect(RelayURL.base("") == nil && RelayURL.base("https://") == nil)
        #expect(RelayURL.websocket(URL(string: "https://flok.example.net")!).absoluteString == "wss://flok.example.net/api/ws")
        #expect(RelayURL.websocket(URL(string: "http://127.0.0.1:8080")!).absoluteString == "ws://127.0.0.1:8080/api/ws")
        #expect(RelayURL.api(URL(string: "https://flok.example.net")!, "/api/answer").absoluteString == "https://flok.example.net/api/answer")
    }
}

struct NotificationTests {
    @Test func aNewTokenTakesTheOldOneOut() {
        let relay = "https://flok.example.net"
        #expect(Relay.replacedToken("\(relay)|aaaa1111", relayURL: relay, now: "bbbb2222") == "aaaa1111", "TestFlight replaced the Xcode build")
        #expect(Relay.replacedToken("\(relay)|aaaa1111", relayURL: relay, now: "aaaa1111") == nil, "the same token stays")
        #expect(Relay.replacedToken("https://other.example|aaaa1111", relayURL: relay, now: "bbbb2222") == nil, "another relay's list is not ours")
        #expect(Relay.replacedToken(nil, relayURL: relay, now: "bbbb2222") == nil)
        #expect(Relay.replacedToken("garbage", relayURL: relay, now: "bbbb2222") == nil)
    }

    @Test func actionsTypeTheRightKeys() {
        #expect(NotificationAction.approve.keys == ["Enter"])
        #expect(NotificationAction.deny.keys == ["Escape"])
        #expect(NotificationAction.seen.keys == nil)
        #expect(NotificationCategory.permission.actions.map(\.identifier) == ["APPROVE", "DENY"])
        #expect(Set(NotificationCategory.all.map(\.identifier)) == ["FLOK_PERMISSION", "FLOK_QUESTION", "FLOK_DONE"])
    }

    @Test func elapsedReadsLikeTheSidebar() {
        let t0 = Date(timeIntervalSince1970: 0)
        #expect(elapsed(since: t0, now: t0.addingTimeInterval(65)) == "1:05")
        #expect(elapsed(since: t0, now: t0.addingTimeInterval(3725)) == "1:02:05")
        #expect(elapsed(since: nil) == "")
    }
}
