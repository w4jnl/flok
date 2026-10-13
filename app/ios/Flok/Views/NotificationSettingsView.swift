// Settings → Notifications: which pushes this phone gets, with which sound, whether they break
// through Focus, and the agents muted from their screen or from a notification. The relay holds
// a copy and decides before every push (iOS cannot drop a push once it arrives); the badge still
// counts everything that waits.
import AudioToolbox
import SwiftUI

struct NotificationSettingsView: View {
    @Environment(Relay.self) private var relay
    @Environment(Settings.self) private var settings
    @State private var now = Date()

    private var prefs: NotificationPrefs { settings.notificationPrefs }

    var body: some View {
        Form {
            ForEach(PushKind.allCases) { kind in
                Section {
                    Toggle(isOn: Binding(get: { prefs.isOn(kind) }, set: { on in relay.updatePrefs { $0.setOn(kind, on) } })) {
                        HStack(spacing: 8) {
                            Text(kind.glyph).font(.system(.body, design: .monospaced)).foregroundStyle(kind == .done ? Palette.green : Palette.orange)
                            Text(kind.title)
                        }
                    }
                    if prefs.isOn(kind) {
                        Picker("Sound", selection: Binding(get: { prefs.sound(kind) }, set: { s in
                            relay.updatePrefs { $0.setSound(kind, s) }
                            SoundPreview.play(s, kind: kind)
                        })) {
                            ForEach(PushSound.allCases) { Text($0.title).tag($0) }
                        }
                        Toggle("Break through Focus", isOn: Binding(get: { prefs.timeSensitive(kind) }, set: { on in relay.updatePrefs { $0.setTimeSensitive(kind, on) } }))
                    }
                } footer: {
                    if kind == PushKind.allCases.last {
                        Text("Break through Focus marks the push time-sensitive: it shows during a Focus that allows flok's time-sensitive notifications.")
                    }
                }
            }
            Section {
                Toggle("Show flok while agents work", isOn: Binding(get: { prefs.showsActivity }, set: { on in
                    relay.updatePrefs { $0.noActivity = on ? nil : true }
                    LiveActivities.shared.ensureOverview(relay.instances, allowed: on)
                }))
                if prefs.showsActivity {
                    Toggle("Finished agents as notifications too", isOn: Binding(get: { prefs.finishedAsNotification == true }, set: { on in
                        relay.updatePrefs { $0.finishedAsNotification = on ? true : nil }
                    }))
                }
            } header: {
                Text("Dynamic Island and Lock Screen")
            } footer: {
                Text(LiveActivities.shared.enabled
                     ? "What works, what waits on you and what finished, starting when the first agent works and ending when all is quiet. While it shows, a finished or failed agent lights it up with its sound instead of a separate notification (none in Notification Center then); prompts and questions are always notifications. Follow one agent from its screen (⋯ → Follow in Dynamic Island)."
                     : "Live Activities are off for flok: Settings → flok → Live Activities.")
            }
            Section {
                let mutes = prefs.mutes.filter { m in m.until.map { $0 > now } ?? true }
                if mutes.isEmpty {
                    Text("No agent is muted.").foregroundStyle(.secondary)
                }
                ForEach(mutes) { m in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(m.agent)
                        HStack(spacing: 6) {
                            Text(m.place)
                            Text("·")
                            Text(m.until.map { "until " + NotificationPrefs.untilWords($0) } ?? "always")
                        }
                        .font(.footnote).foregroundStyle(.secondary)
                    }
                    .swipeActions {
                        Button("Unmute") { relay.updatePrefs { $0.unmute(instance: m.instance, host: m.host, agent: m.agent) } }.tint(Palette.cyan)
                    }
                }
            } header: {
                Text("Muted agents")
            } footer: {
                Text("Mute an agent from its screen (⋯ → Mute notifications) or with Mute 1 h on a notification. A muted agent still counts in the badge.")
            }
            if let e = relay.prefsError {
                Section {
                    Text("Not on the relay yet: \(e)").font(.footnote).foregroundStyle(Palette.orange)
                    Button("Try again") { Task { await relay.syncPrefs() } }
                }
            }
        }
        .navigationTitle("Notifications")
        .navigationBarTitleDisplayMode(.inline)
        .onAppear {
            now = .now
            relay.expireMutes()
        }
    }
}

/// Plays the sound a choice stands for, so the picker can be heard.
enum SoundPreview {
    @MainActor private static var ids: [String: SystemSoundID] = [:]

    @MainActor static func play(_ s: PushSound, kind: PushKind) {
        switch s {
        case .none:
            return
        case .system:
            AudioServicesPlaySystemSound(1007) // the tri-tone a notification makes by default
        case .flok:
            let name = kind.flokSound
            if ids[name] == nil, let url = Bundle.main.url(forResource: name, withExtension: "caf") {
                var id: SystemSoundID = 0
                if AudioServicesCreateSystemSoundID(url as CFURL, &id) == noErr { ids[name] = id }
            }
            if let id = ids[name] { AudioServicesPlaySystemSound(id) }
        }
    }
}

/// What the app ships that others made.
struct AcknowledgementsView: View {
    var body: some View {
        List {
            Section {
                Text("done and request, the sounds of the sidebar and of these notifications, are herdr's (github.com/herdrdev/herdr), under the Apache License, Version 2.0.")
            } header: { Text("Sounds") }
            Section {
                Text("JetBrains Mono, the face of the expanded notification, is © The JetBrains Mono Project Authors, under the SIL Open Font License 1.1.")
            } header: { Text("Type") }
        }
        .navigationTitle("Acknowledgements")
        .navigationBarTitleDisplayMode(.inline)
    }
}
