import Foundation
import SwiftUI
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
        #expect(NotificationAction.seen.keys == nil && NotificationAction.mute.keys == nil)
        #expect(NotificationCategory.permission.actions.map(\.identifier) == ["APPROVE", "DENY", "MUTE_1H"])
        #expect(NotificationCategory.question.actions.map(\.identifier) == ["MUTE_1H"])
        #expect(NotificationCategory.done.actions.map(\.identifier) == ["SEEN", "MUTE_1H"])
        #expect(Set(NotificationCategory.all.map(\.identifier)) == ["FLOK_PERMISSION", "FLOK_QUESTION", "FLOK_DONE"])
    }

    @Test func elapsedReadsLikeTheSidebar() {
        let t0 = Date(timeIntervalSince1970: 0)
        #expect(elapsed(since: t0, now: t0.addingTimeInterval(65)) == "1:05")
        #expect(elapsed(since: t0, now: t0.addingTimeInterval(3725)) == "1:02:05")
        #expect(elapsed(since: nil) == "")
    }
}

struct PushTests {
    @Test func readsWhatTheRelaySends() throws {
        let p = try #require(PushPayload(["instance": "Home", "pane": "macmini:%1", "agent": "docker", "host": "macmini", "session": "infra",
                                          "kind": "blocked", "reason": "permission:Bash", "push_kind": "permission"]))
        #expect(p.place == "Home / macmini · infra")
        #expect(p.glyph == "●" && p.label == "needs you: perm:Bash" && p.answerable && p.tileName == "tile-attention")
        #expect(PushPayload(["pane": "%1"]) == nil, "no instance, not a flok push")
    }

    @Test func anOlderRelayWithoutPushKindIsSortedTheSameWay() throws {
        #expect(PushPayload(["instance": "Home", "pane": "%1", "kind": "blocked", "reason": "question"])?.pushKind == "question")
        #expect(PushPayload(["instance": "Home", "pane": "%1", "kind": "blocked", "reason": "permission"])?.label == "needs you: perm")
        let failed = try #require(PushPayload(["instance": "Home", "pane": "%1", "kind": "error"]))
        #expect(failed.glyph == "✗" && failed.label == "failed" && !failed.answerable && failed.wantsYou)
        let done = try #require(PushPayload(["instance": "Home", "pane": "%1", "kind": "done"]))
        #expect(done.glyph == "✓" && done.tileName == "tile-done" && done.place == "Home")
    }

    @Test func theCardShowsTheBottomOfTheScreen() {
        let screen = "line 1\nline 2   \n❯ Allow Bash?\n  1. Yes\n  2. No\n\n\n"
        #expect(NotificationCard.tail(screen, lines: 3) == "❯ Allow Bash?\n  1. Yes\n  2. No")
        #expect(NotificationCard.tail("", lines: 3) == "")
    }

    /// Renders the card for a look by eye: TEST_RUNNER_FLOK_CARD_PNG=/path xcodebuild test …
    @MainActor @Test func rendersTheCard() throws {
        guard let path = ProcessInfo.processInfo.environment["FLOK_CARD_PNG"] else { return }
        let push = PushPayload(instance: "Home", pane: "%36", agent: "dotfiles-revamp", host: "", session: "dotfiles", kind: "blocked", reason: "permission:Bash")
        let screen = "● Bash(yadm pull --rebase)\n  ⎿  Running…\n\n╭──────────────────────────────╮\n│ Bash command                 │\n│   yadm pull --rebase         │\n│ Do you want to proceed?      │\n│ ❯ 1. Yes                     │\n│   2. Yes, and don't ask again │\n│   3. No, and tell Claude …   │\n╰──────────────────────────────╯\n"
        let card = NotificationCard(push: push, screen: .text(screen), status: "sent ↵").frame(width: 390)
        let r = ImageRenderer(content: card)
        r.scale = 3
        let image = try #require(r.uiImage)
        try #require(image.pngData()).write(to: URL(fileURLWithPath: path))
    }
}

struct PrefsTests {
    @Test func kindsBackAtTheirDefaultsAreDropped() {
        var p = NotificationPrefs()
        p.setOn(.done, false)
        p.setSound(.question, .none)
        p.setTimeSensitive(.permission, false)
        #expect(!p.isOn(.done) && p.sound(.question) == .none && !p.timeSensitive(.permission) && p.timeSensitive(.question))
        p.setOn(.done, true)
        p.setSound(.question, .flok)
        p.setTimeSensitive(.permission, true)
        #expect(p.kinds.isEmpty, "all back to the defaults: nothing left to store")
    }

    @Test func mutesMatchByInstanceHostAndName() {
        var p = NotificationPrefs()
        let now = Date()
        p.mute(instance: "Home", host: "macmini", agent: "docker", until: now.addingTimeInterval(3600))
        p.mute(instance: "Home", host: nil, agent: "api", until: nil)
        #expect(p.mute(instance: "Home", host: "macmini", agent: "docker", now: now) != nil)
        #expect(p.mute(instance: "Home", host: "", agent: "docker", now: now) == nil, "the local docker is another agent")
        #expect(p.mute(instance: "Home", host: "", agent: "api", now: now) != nil)
        #expect(p.mute(instance: "Home", host: "macmini", agent: "docker", now: now.addingTimeInterval(7200)) == nil, "expired")
        p.mute(instance: "Home", host: "macmini", agent: "docker", until: nil)
        #expect(p.mutes.count == 2, "muting again replaces the earlier mute")
        p.unmute(instance: "Home", host: "macmini", agent: "docker")
        #expect(p.mutes.count == 1)
    }

    @Test func encodesTheWayTheRelayReadsThem() throws {
        var p = NotificationPrefs()
        p.setTimeSensitive(.question, false)
        p.mute(instance: "Home", host: "macmini", agent: "docker", until: Date(timeIntervalSince1970: 1_791_000_000))
        let json = String(decoding: try Wire.encoder.encode(p), as: UTF8.self)
        #expect(json.contains(#""time_sensitive":false"#))
        #expect(json.contains(#""until":"2026-10-03T"#), "RFC 3339, what Go's time parses")
        #expect(json.contains(#""host":"macmini""#) && json.contains(#""agent":"docker""#))
    }

    @Test func tomorrowMorningIsEightTheNextDay() {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(identifier: "Europe/Amsterdam")!
        let evening = cal.date(from: DateComponents(year: 2026, month: 10, day: 12, hour: 23, minute: 30))!
        let c = cal.dateComponents([.day, .hour, .minute], from: NotificationPrefs.tomorrowMorning(from: evening, calendar: cal))
        #expect(c.day == 13 && c.hour == 8 && c.minute == 0)
    }

    @Test func theSpinnerTurnsAQuarterEveryFrame() {
        let t0 = Date(timeIntervalSinceReferenceDate: 1000)
        let frames = (0..<5).map { StateGlyph.frame(at: t0.addingTimeInterval(Double($0) * StateGlyph.frameTime)) }
        #expect(frames == ["◐", "◓", "◑", "◒", "◐"])
    }
}

struct SeenTests {
    @Test func waitingIsUnseenOrBlocked() throws {
        let json = """
        {"type":"instances","instances":[{"name":"Home","online":true,"since":"2026-10-13T08:00:00Z","snap":{"unseen":1,"agents":[
          {"pane_id":"%1","session_id":"$0","session_name":"a","name":"done-unseen","kind":"claude","state":"done","unseen":1},
          {"pane_id":"%2","session_id":"$0","session_name":"a","name":"asks-seen","kind":"claude","state":"blocked","unseen":0},
          {"pane_id":"%3","host":"macmini","session_id":"$0","session_name":"a","name":"idle","kind":"claude","state":"idle","unseen":0}]}}]}
        """
        let f = try Wire.decoder.decode(Frame.self, from: Data(json.utf8))
        #expect(Relay.waitingKeys(f.instances ?? []) == ["Home:%1", "Home:%2"], "a block waits until answered, seen or not")
        #expect(PushPayload(["instance": "Home", "pane": "macmini:%3"])?.key == "Home:macmini:%3", "the relay's name for it")
    }
}
