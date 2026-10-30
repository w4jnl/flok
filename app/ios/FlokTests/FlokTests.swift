import Foundation
import SwiftUI
import Testing
import UserNotifications
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
        #expect(inst.name == "Home" && inst.online && inst.unseen == 2 && !inst.switchedOff)
        #expect(inst.agents.count == 3)
        #expect(inst.agents[0].paneRef == "%3" && inst.agents[0].state == .blocked)
        #expect(inst.agents[1].paneRef == "beta:%1" && inst.agents[1].state == .done)
        #expect(inst.agents[2].state == .unknown, "an unknown state does not break decoding")
        #expect(StateStyle.label(inst.agents[0]) == "needs you: perm:Bash")
        #expect(StateStyle.label(inst.agents[1]) == "done")
    }

    @Test func aSwitchedOffInstanceIsNotOffline() throws {
        let json = #"{"type":"instance","instance":"Office","inst":{"name":"Office","online":false,"off":true,"since":"2026-10-13T08:00:00Z"}}"#
        let f = try Wire.decoder.decode(Frame.self, from: Data(json.utf8))
        let inst = try #require(f.inst)
        #expect(inst.switchedOff && !inst.online)
        let lost = try Wire.decoder.decode(InstanceState.self, from: Data(#"{"name":"Home","online":false,"since":"2026-10-13T08:00:00Z"}"#.utf8))
        #expect(!lost.switchedOff, "an instance that went away without a bye is offline")
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

    @Test func decodesAndKeepsTheFeed() throws {
        let reset = try Wire.decoder.decode(Frame.self, from: Data(#"{"type":"feed","instance":"Home","feed":{"pane":"%1","reset":true,"items":[{"id":"u1","kind":"prompt","at":"2026-10-30T07:00:00Z","text":"fix it"},{"id":"a1","kind":"tool","text":"Ran go test","tool":"Bash"}]}}"#.utf8))
        #expect(reset.feed?.pane == "%1" && reset.feed?.reset == true && reset.feed?.items?.count == 2)
        #expect(reset.feed?.items?[1].tool == "Bash")
        var state = FeedState()
        state.take(reset.feed!)
        let more = try Wire.decoder.decode(Frame.self, from: Data(#"{"type":"feed","instance":"Home","feed":{"pane":"%1","items":[{"id":"a1","kind":"tool","text":"Ran go test","tool":"Bash"},{"id":"a2","kind":"text","text":"Done."}]}}"#.utf8))
        state.take(more.feed!)
        #expect(state.items.map(\.id) == ["u1", "a1", "a2"], "an item sent twice counts once")
        #expect(!state.empty)
        var none = FeedState()
        none.take(try Wire.decoder.decode(Frame.self, from: Data(#"{"type":"feed","instance":"Home","feed":{"pane":"%2","reset":true,"note":"Copilot CLI keeps no transcript flok can read: the screen is below"}}"#.utf8)).feed!)
        #expect(none.empty && none.note?.hasPrefix("Copilot") == true)
    }

    @Test func splitsMarkdownIntoBlocks() {
        let text = """
        # Plan
        First the **tests**, then the code.
        A second line of the same paragraph.

        - one
          - nested
        1. first
        2) second

        ```sh
        go test ./...
        ```
        > a quote
        ---
        | a | b |
        |---|---|
        """
        let kinds = FeedMarkdown.blocks(of: text).map(\.kind)
        #expect(kinds == [.heading(1), .paragraph, .bullet(indent: 0), .bullet(indent: 1), .numbered(indent: 0, label: "1."), .numbered(indent: 0, label: "2)"), .code(lang: "sh"), .quote, .rule, .table])
        let blocks = FeedMarkdown.blocks(of: text)
        #expect(blocks[1].text == "First the **tests**, then the code. A second line of the same paragraph.")
        #expect(blocks[6].text == "go test ./...")
        #expect(blocks[9].lines.count == 2)
        #expect(String(FeedMarkdown.inline("a **b** `c`").characters) == "a b c")
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
        #expect(NotificationAction.reply.keys == ["Enter"], "a reply is its text, then Enter")
        #expect(NotificationAction.seen.keys == nil && NotificationAction.mute.keys == nil)
        #expect(NotificationCategory.permission.actions.map(\.identifier) == ["APPROVE", "DENY", "REPLY", "MUTE_1H"])
        #expect(NotificationCategory.question.actions.map(\.identifier) == ["REPLY", "MUTE_1H"])
        #expect(NotificationCategory.done.actions.map(\.identifier) == ["REPLY", "SEEN", "MUTE_1H"])
        #expect(NotificationCategory.all.allSatisfy { c in c.actions.contains { $0 is UNTextInputNotificationAction && $0.identifier == "REPLY" } }, "every category has the text field")
        #expect(Set(NotificationCategory.all.map(\.identifier)) == ["FLOK_PERMISSION", "FLOK_QUESTION", "FLOK_DONE"])
        let answers = NotificationCategory.all.flatMap(\.actions).filter { ["APPROVE", "DENY", "REPLY"].contains($0.identifier) }
        #expect(!answers.isEmpty && answers.allSatisfy { $0.options.contains(.authenticationRequired) }, "an answer from the Lock Screen needs the phone unlocked")
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
        #expect(p.finishedInIsland, "finished agents light up the overview by default")
        p.finishedAsNotification = true
        #expect(!p.finishedInIsland)
        let json = String(decoding: try Wire.encoder.encode(p), as: UTF8.self)
        #expect(json.contains(#""time_sensitive":false"#))
        #expect(json.contains(#""finished_as_notification":true"#))
        #expect(json.contains(#""until":"2026-10-03T"#), "RFC 3339, what Go's time parses")
        #expect(json.contains(#""host":"macmini""#) && json.contains(#""agent":"docker""#))
    }

    @Test func nextMorningIsTheComingEightOClock() {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(identifier: "Europe/Amsterdam")!
        func at(_ day: Int, _ hour: Int, _ minute: Int = 0) -> Date { cal.date(from: DateComponents(year: 2026, month: 10, day: day, hour: hour, minute: minute))! }
        #expect(NotificationPrefs.nextMorning(from: at(12, 23, 30), calendar: cal) == at(13, 8), "in the evening: tomorrow")
        #expect(NotificationPrefs.nextMorning(from: at(13, 0, 40), calendar: cal) == at(13, 8), "after midnight: this morning, not the day after")
        #expect(NotificationPrefs.nextMorning(from: at(13, 7, 30), calendar: cal) == at(14, 8), "less than an hour before: the next day")
        #expect(NotificationPrefs.untilWords(at(13, 8), now: at(13, 0, 40), calendar: cal).hasPrefix("tomorrow") == false)
        #expect(NotificationPrefs.untilWords(at(14, 8), now: at(13, 7, 30), calendar: cal).hasPrefix("tomorrow "))
    }

    @Test func anEndedMuteIsDroppedAndTheNextEndIsKnown() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        var p = NotificationPrefs()
        p.mute(instance: "Home", host: nil, agent: "flok", until: now.addingTimeInterval(-60))
        p.mute(instance: "Home", host: nil, agent: "api", until: now.addingTimeInterval(3600))
        p.mute(instance: "Home", host: nil, agent: "docs", until: nil)
        #expect(p.mute(instance: "Home", host: nil, agent: "flok", now: now) == nil, "ended at its time")
        #expect(p.hasExpired(now: now))
        #expect(p.nextExpiry(after: now) == now.addingTimeInterval(3600))
        p.prune(now: now)
        #expect(p.mutes.map(\.agent) == ["api", "docs"])
        #expect(!p.hasExpired(now: now))
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

struct LiveActivityTests {
    /// What relay/live.go sends as content-state (LiveState): the names and shapes ActivityKit decodes.
    @Test func decodesTheRelaysContentState() throws {
        let json = #"{"working":2,"waiting":1,"done":0,"rows":[{"instance":"Home","host":"macmini","agent":"docker","pane":"macmini:%1","state":"blocked","label":"needs you: perm:Bash","since":1791100000}]}"#
        let st = try JSONDecoder().decode(FlokActivityAttributes.ContentState.self, from: Data(json.utf8))
        #expect(st.working == 2 && st.waiting == 1 && st.dominant == "blocked")
        let r = try #require(st.rows.first)
        #expect(r.place == "Home / macmini" && r.started == Date(timeIntervalSince1970: 1_791_100_000))
        let attrs = try JSONDecoder().decode(FlokActivityAttributes.self, from: Data(#"{"kind":"overview","instance":"","pane":"","agent":""}"#.utf8))
        #expect(attrs.kind == "overview")
    }

    @Test func overviewRanksLikeTheRelay() throws {
        let json = """
        {"type":"instances","instances":[
         {"name":"Home","online":true,"since":"2026-10-13T08:00:00Z","snap":{"agents":[
          {"pane_id":"%1","session_id":"$0","session_name":"a","name":"w-old","kind":"claude","state":"working","unseen":0,"state_since":"2026-10-13T09:00:00Z","tool":"Edit"},
          {"pane_id":"%2","session_id":"$0","session_name":"a","name":"w-new","kind":"claude","state":"working","unseen":0,"state_since":"2026-10-13T09:05:00Z"},
          {"pane_id":"%3","session_id":"$0","session_name":"a","name":"asks","kind":"claude","state":"blocked","reason":"permission:Bash","unseen":1,"state_since":"2026-10-13T09:07:00Z"},
          {"pane_id":"%4","session_id":"$0","session_name":"a","name":"done-seen","kind":"claude","state":"done","unseen":0},
          {"pane_id":"%5","session_id":"$0","session_name":"a","name":"done-unseen","kind":"claude","state":"done","unseen":1,"state_since":"2026-10-13T09:06:00Z"}]}},
         {"name":"Office","online":false,"since":"2026-10-13T08:00:00Z","snap":{"agents":[
          {"pane_id":"%1","session_id":"$0","session_name":"a","name":"offline","kind":"claude","state":"working","unseen":0}]}}]}
        """
        let f = try Wire.decoder.decode(Frame.self, from: Data(json.utf8))
        let st = LiveActivities.overview(f.instances ?? [])
        #expect(st.working == 2 && st.waiting == 1 && st.done == 1, "an offline instance does not count")
        #expect(st.rows.map(\.agent) == ["asks", "done-unseen", "w-old"])
        #expect(st.rows[0].label == "needs you: perm:Bash" && st.rows[2].label == "working", "no tool: a tool change is not a push")
    }

    @Test func linksOpenTheAgent() throws {
        let r = LiveRow(instance: "Home", host: "macmini", agent: "docker", pane: "macmini:%1", state: "working", label: "working", since: 0)
        let t = try #require(FlokLink.target(r.link))
        #expect(t.instance == "Home" && t.pane == "macmini:%1")
        #expect(FlokLink.target(URL(string: "flok://home")!) == nil)
    }

    /// Renders the Lock Screen view and the island's parts for a look by eye: TEST_RUNNER_FLOK_LIVE_PNG=/dir
    @MainActor @Test func rendersTheActivity() throws {
        guard let dir = ProcessInfo.processInfo.environment["FLOK_LIVE_PNG"] else { return }
        let now = Date().timeIntervalSince1970
        let st = FlokActivityAttributes.ContentState(working: 2, waiting: 1, done: 1, rows: [
            LiveRow(instance: "Home", host: "", agent: "dotfiles-revamp", pane: "%36", state: "blocked", label: "needs you: perm:Bash", since: now - 95),
            LiveRow(instance: "Home", host: "", agent: "api", pane: "%39", state: "done", label: "done", since: now - 30),
            LiveRow(instance: "Home", host: "macmini", agent: "docker", pane: "macmini:%1", state: "working", label: "working", since: now - 724),
        ])
        func save<V: View>(_ v: V, _ name: String, width: CGFloat, bg: Color) throws {
            let r = ImageRenderer(content: v.frame(width: width).background(bg))
            r.scale = 3
            try #require(r.uiImage?.pngData()).write(to: URL(fileURLWithPath: dir).appending(path: name))
        }
        try save(LiveLockScreen(attributes: .overview, state: st), "lock.png", width: 370, bg: Brand.ground)
        try save(LiveExpandedBottom(state: st).padding(12), "expanded.png", width: 370, bg: .black)
        try save(HStack { LiveCompactLeading(state: st); Spacer(); LiveCompactTrailing(attributes: .overview, state: st) }.padding(8), "compact.png", width: 230, bg: .black)
    }
}

/// What the app remembers between starts: UserDefaults, read back at init.
struct SettingsTests {
    @MainActor @Test func wrapIsTheDefaultAndAChoiceIsKept() {
        let d = UserDefaults.standard
        let before = d.object(forKey: "wrapLines")
        defer { if let before { d.set(before, forKey: "wrapLines") } else { d.removeObject(forKey: "wrapLines") } }
        d.removeObject(forKey: "wrapLines")
        #expect(Settings().wrapLines, "a fresh install wraps long lines")
        let s = Settings()
        s.wrapLines = false
        #expect(!Settings().wrapLines, "switched off, it stays off at the next start")
        s.wrapLines = true
        #expect(Settings().wrapLines)
    }
}

/// The app lock's pure decisions: when a return to the foreground asks, what Settings offers,
/// how much of the phone's own unlock counts; and the privacy flag the extensions read.
struct LockTests {
    private func lock(since: Duration?, sameDay: Bool = true, grace: LockGrace = .after(.seconds(60)), enabled: Bool = true, onLaunch: Bool = true, launching: Bool = false) -> Bool {
        LockPolicy.shouldLock(since: since, sameDay: sameDay, grace: grace, enabled: enabled, onLaunch: onLaunch, launching: launching)
    }

    @Test func offMeansNeverLocked() {
        #expect(!lock(since: .seconds(3600), grace: .after(.zero), enabled: false, launching: true))
        #expect(!lock(since: nil, grace: .after(.zero), enabled: false))
        #expect(!lock(since: .seconds(90000), sameDay: false, grace: .untilTomorrow, enabled: false))
    }

    @Test func aColdStartFollowsLockOnLaunch() {
        #expect(lock(since: nil, launching: true))
        #expect(lock(since: .seconds(1), launching: true), "Lock on launch never trusts a grace")
        #expect(!lock(since: nil, onLaunch: false, launching: true), "nothing remembered: nothing to measure")
        #expect(!lock(since: .seconds(30), onLaunch: false, launching: true), "a remembered background inside the grace: a cold start is a return")
        #expect(lock(since: .seconds(61), onLaunch: false, launching: true))
    }

    @Test func theGraceIsMeasuredFromTheBackground() {
        #expect(!lock(since: .seconds(59)))
        #expect(lock(since: .seconds(60)), "the boundary locks")
        #expect(lock(since: .seconds(3600)))
        #expect(lock(since: .zero, grace: .after(.zero), onLaunch: false), "Immediately is grace zero")
        #expect(lock(since: .seconds(-5)), "a clock that went backwards locks rather than trusts")
        #expect(!lock(since: .seconds(86399), grace: .after(.seconds(86400))) && lock(since: .seconds(86400), grace: .after(.seconds(86400))))
    }

    @Test func untilTomorrowAsksOnceADay() {
        #expect(!lock(since: .seconds(8 * 3600), sameDay: true, grace: .untilTomorrow), "away all afternoon, still today: no prompt")
        #expect(lock(since: .seconds(60), sameDay: false, grace: .untilTomorrow), "a minute across midnight: the day changed")
        #expect(lock(since: .seconds(3 * 86400), sameDay: false, grace: .untilTomorrow))
        #expect(!lock(since: nil, sameDay: false, grace: .untilTomorrow), "inactive never locks, whatever the day")
        #expect(LockGrace(seconds: LockPolicy.untilTomorrow) == .untilTomorrow && LockGrace(seconds: 300) == .after(.seconds(300)))
    }

    @Test func goingInactiveNeverLocks() {
        #expect(!lock(since: nil, grace: .after(.zero)), "Control Center, a call, the Face ID sheet: no background, no lock")
    }

    @Test func gracesAreOrderedAndHoldTheDefault() {
        let s = LockPolicy.graces.map(\.seconds)
        let timed = s.filter { $0 >= 0 }
        #expect(timed == timed.sorted() && timed.first == 0 && timed.last == 86400)
        #expect(s.last == LockPolicy.untilTomorrow, "the once-a-day choice closes the list")
        #expect(s.contains(LockPolicy.defaultGrace))
        #expect(Set(s).count == s.count)
    }

    @Test func thePhonesOwnUnlockCountsWithinTheGraceUpToFiveMinutes() {
        #expect(LockPolicy.reuse(forGrace: 0) == 0, "Immediately reuses nothing")
        #expect(LockPolicy.reuse(forGrace: 60) == 60)
        #expect(LockPolicy.reuse(forGrace: 900) == 300 && LockPolicy.reuse(forGrace: 86400) == 300, "iOS allows five minutes at most")
        #expect(LockPolicy.reuse(forGrace: LockPolicy.untilTomorrow) == 300, "Until tomorrow: the full five minutes")
    }

    @Test func biometryNames() {
        #expect(LockPolicy.label(.faceID) == "Face ID" && LockPolicy.symbol(.faceID) == "faceid")
        #expect(LockPolicy.label(.touchID) == "Touch ID" && LockPolicy.symbol(.touchID) == "touchid")
        #expect(LockPolicy.label(.none) == "passcode" && LockPolicy.symbol(.none) == "lock.fill")
    }

    @MainActor @Test func lockSettingsDefaultOffAndKeepAChoice() {
        let d = UserDefaults.standard
        let keys = ["lockEnabled", "lockGraceSeconds", "lockOnLaunch"]
        let before = keys.map { ($0, d.object(forKey: $0)) }
        defer { for (k, v) in before { if let v { d.set(v, forKey: k) } else { d.removeObject(forKey: k) } } }
        for k in keys { d.removeObject(forKey: k) }
        let fresh = Settings()
        #expect(!fresh.lockEnabled, "a fresh install asks for nothing")
        #expect(fresh.lockGraceSeconds == 60 && fresh.lockGrace == .after(.seconds(60)))
        #expect(fresh.lockOnLaunch)
        fresh.lockEnabled = true
        fresh.lockGraceSeconds = 300
        fresh.lockOnLaunch = false
        let next = Settings()
        #expect(next.lockEnabled && next.lockGraceSeconds == 300 && !next.lockOnLaunch)
    }

    /// The flag's reading only: the unsigned simulator build the tests run in has no keychain
    /// to round-trip through (SecItemAdd wants a signed host), so the item itself is tried on a phone.
    @Test func hideDetailsIsAFlagInTheKeychainGroup() {
        #expect(!Privacy.hideDetails(nil) && !Privacy.hideDetails("0"), "off by default: the Lock Screen shows the names")
        #expect(Privacy.hideDetails("1"))
        #expect(Privacy.key == "hideDetailsLocked", "the content extension and the widget read this account")
    }
}

struct FilterTests {
    private func agent(_ name: String, _ state: AgentState, unseen: Int = 0) -> Agent {
        let json = #"{"pane_id":"%1","session_id":"$0","session_name":"s","window_id":"@1","name":"\#(name)","kind":"claude","state":"\#(state.rawValue)","unseen":\#(unseen),"state_since":"2026-10-17T09:00:00Z","has_hooks":true}"#
        return try! Wire.decoder.decode(Agent.self, from: Data(json.utf8))
    }

    @Test func filtersKeepTheRightAgentsInAttentionOrder() {
        let agents = [agent("idle", .idle), agent("worker", .working), agent("asks", .blocked), agent("finished", .done, unseen: 1), agent("seen", .done), agent("bg", .paused)]
        #expect(AgentFilter().apply(agents).map(\.name) == ["idle", "worker", "asks", "finished", "seen", "bg"])
        #expect(AgentFilter.needsYou.apply(agents).map(\.name) == ["asks", "finished"])
        #expect(AgentFilter.active.apply(agents).map(\.name) == ["asks", "worker", "bg"])
        let both: AgentFilter = [.needsYou, .active]
        #expect(both.apply(agents).map(\.name) == ["asks", "finished", "worker", "bg"])
        #expect(both.title == "Needs you · Active" && both.emptyText == "nothing running or waiting" && AgentFilter.active.emptyText == "nothing running")
        #expect(AgentFilter(rawValue: 3) == both && AgentFilter(rawValue: 0).isEmpty)
    }
}

struct PairTests {
    @Test func readsThePairingLink() {
        let p = FlokLink.pairing(URL(string: "flok://pair?code=AB7Q-K2MN&relay=https%3A%2F%2Fflok.example.net")!)
        #expect(p?.relay == "https://flok.example.net" && p?.code == "AB7Q-K2MN")
        let bare = FlokLink.pairing(URL(string: "flok://pair?code=ab7qk2mn&relay=http%3A%2F%2F127.0.0.1%3A8080")!)
        #expect(bare?.relay == "http://127.0.0.1:8080" && bare?.code == "AB7Q-K2MN")
        #expect(FlokLink.pairing(URL(string: "flok://agent?instance=home&pane=%251")!) == nil)
        #expect(FlokLink.pairing(URL(string: "flok://pair?relay=https%3A%2F%2Fflok.example.net")!) == nil)
        #expect(FlokLink.pairing(URL(string: "flok://pair?code=AB7Q&relay=https%3A%2F%2Fflok.example.net")!) == nil)
        #expect(FlokLink.target(URL(string: "flok://pair?code=AB7Q-K2MN&relay=x")!) == nil)
    }

    @Test func codesAreTypedAnyWay() {
        #expect(PairCode.normalize(" ab7q k2mn ") == "AB7QK2MN")
        #expect(PairCode.normalize("AB7Q–K2MN") == "AB7QK2MN")
        #expect(PairCode.format("ab7qk2mn") == "AB7Q-K2MN")
        #expect(PairCode.format("ab7") == "AB7" && PairCode.format("ab7qk") == "AB7Q-K")
        #expect(PairCode.isComplete("AB7Q-K2MN") && !PairCode.isComplete("AB7Q-K2M"))
    }

    @Test func pairBodiesMatchTheRelay() throws {
        let body = String(decoding: try Wire.encoder.encode(PairBody(code: "AB7QK2MN", name: "Jaro's iPhone")), as: UTF8.self)
        #expect(body.contains(#""code":"AB7QK2MN""#) && body.contains(#""name":"Jaro's iPhone""#))
        let json = #"{"token":"0123456789abcdef0123456789abcdef0123456789abcdef","phone":{"id":"a1b2c3d4e5f6","name":"Jaro's iPhone"},"tenant":{"id":"default","name":"default"},"relay":{"version":"v0.9.0"}}"#
        let r = try Wire.decoder.decode(PairResponse.self, from: Data(json.utf8))
        #expect(r.token.count == 48 && r.phone == PairedPhone(id: "a1b2c3d4e5f6", name: "Jaro's iPhone") && r.relay?.version == "v0.9.0" && r.tenant?.name == "default")
        let old = #"{"token":"0123456789abcdef0123456789abcdef0123456789abcdef","phone":{"id":"a1b2c3d4e5f6","name":"x"}}"#
        #expect(try Wire.decoder.decode(PairResponse.self, from: Data(old.utf8)).tenant == nil) // a relay before tenants
    }

    @Test func readsTheJoinLink() throws {
        let l = FlokLink.linking(URL(string: "flok://link?code=AB7Q-K2MN&relay=https%3A%2F%2Fflok.example.net")!)
        #expect(l?.relay == "https://flok.example.net" && l?.code == "AB7Q-K2MN")
        #expect(FlokLink.linking(URL(string: "flok://pair?code=AB7Q-K2MN&relay=https%3A%2F%2Fflok.example.net")!) == nil)
        #expect(FlokLink.pairing(URL(string: "flok://link?code=AB7Q-K2MN&relay=https%3A%2F%2Fflok.example.net")!) == nil)
        #expect(FlokLink.linking(URL(string: "flok://link?code=AB7Q&relay=https%3A%2F%2Fflok.example.net")!) == nil)
        let json = #"{"pending":{"name":"office","hostname":"macmini-office","version":"v0.9.0","from":"1.2.3.4","started":"2026-10-17T09:00:00Z","expires_at":"2026-10-17T09:05:00Z"},"tenant":{"id":"t1","name":"jaro"}}"#
        let r = try Wire.decoder.decode(JoinLookup.self, from: Data(json.utf8))
        #expect(r.pending.name == "office" && r.pending.describe == "office (macmini-office, flok v0.9.0)" && r.tenant?.name == "jaro" && r.pending.expiresAt != nil)
        let bare = try Wire.decoder.decode(JoinLookup.self, from: Data(#"{"pending":{"name":"lab","started":"2026-10-17T09:00:00Z","expires_at":"2026-10-17T09:05:00Z"}}"#.utf8))
        #expect(bare.pending.describe == "lab" && bare.tenant == nil)
    }

    @Test func aGoneInstanceDecodes() throws {
        let f = try Wire.decoder.decode(Frame.self, from: Data(#"{"type":"instance","instance":"office","inst":{"name":"office","online":false,"since":"2026-10-17T09:00:00Z","gone":true}}"#.utf8))
        #expect(f.inst?.gone == true && f.inst?.name == "office")
    }
}

struct FormTests {
    static let agentJSON = """
    {"pane_id":"%3","session_id":"$1","session_name":"nt","window_id":"@2","name":"nt","kind":"claude","state":"blocked","reason":"question","unseen":1,"state_since":"2026-10-27T20:39:00Z","has_hooks":true,
     "form":{"questions":[{"header":"Fruit","text":"Which fruits do you like?","options":[{"label":"Apple","description":"red or green"},{"label":"Banana"},{"label":"Cherry"}],"multi":true},
                          {"header":"Size","text":"Which size?","options":[{"label":"Small"},{"label":"Large"}]}]}}
    """

    @Test func decodesTheFormTheInstanceSends() throws {
        let a = try Wire.decoder.decode(Agent.self, from: Data(Self.agentJSON.utf8))
        #expect(a.asksForm)
        let form = try #require(a.form)
        #expect(form.questions.count == 2 && form.questions[0].isMulti && !form.questions[1].isMulti)
        #expect(form.questions[0].options[0].description == "red or green" && form.questions[1].header == "Size")
        #expect(AgentView.formWords(form) == "2 questions: Fruit, Size")
        // without the form (an older flok, a question the hooks missed) the keys are all there is
        let bare = try Wire.decoder.decode(Agent.self, from: Data(#"{"pane_id":"%3","name":"nt","state":"blocked","reason":"question","unseen":1}"#.utf8))
        #expect(!bare.asksForm && bare.form == nil)
        let old = try Wire.decoder.decode(InstanceState.self, from: Data(#"{"name":"Home","online":true,"since":"2026-10-27T20:39:00Z","features":["answer","files"]}"#.utf8))
        #expect(!old.takesForms)
        let new = try Wire.decoder.decode(InstanceState.self, from: Data(#"{"name":"Home","online":true,"since":"2026-10-27T20:39:00Z","features":["answer","form"]}"#.utf8))
        #expect(new.takesForms)
    }

    @Test func theDraftFollowsTheFormsRules() throws {
        let form = try #require(try Wire.decoder.decode(Agent.self, from: Data(Self.agentJSON.utf8)).form)
        var d = FormDraft(form: form)
        #expect(!d.complete && !d.answered(0))
        d.tap(0, 0); d.tap(0, 2); d.tap(0, 0) // tick, tick, untick: a multi-select keeps a set
        #expect(d.selected[0] == [2] && d.answered(0) && !d.complete)
        d.tap(1, 0); d.tap(1, 1) // a single-select keeps one
        #expect(d.selected[1] == [1] && d.complete)
        d.setOther(1, " Medium ") // words replace the pick there
        #expect(d.selected[1].isEmpty && d.answered(1))
        d.tap(1, 0) // and a pick drops the words
        #expect(d.other[1].isEmpty && d.selected[1] == [0])
        d.tap(1, 0) // tapped again it is unpicked
        #expect(d.selected[1].isEmpty && !d.complete)
        d.tap(1, 1)
        d.setOther(0, "Kiwi") // on a multi-select the words come on top of the ticks
        #expect(d.selected[0] == [2])
        #expect(d.body == FormAnswerBody(answers: [FormChoice(selected: [2], other: "Kiwi"), FormChoice(selected: [1], other: nil)]))
        // on the wire, under "form", as the relay reads it
        let data = try Wire.encoder.encode(AnswerBody(instance: "Home", pane: "%3", form: d.body))
        let obj = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        let answers = try #require((obj["form"] as? [String: Any])?["answers"] as? [[String: Any]])
        #expect(answers.count == 2 && answers[0]["selected"] as? [Int] == [2] && answers[0]["other"] as? String == "Kiwi" && answers[1]["other"] == nil)
        #expect(obj["text"] == nil && obj["keys"] == nil)
    }
}
