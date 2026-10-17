// The list's filter: two switches the toolbar's menu holds, on their own or together. Needs you
// keeps the agents that wait on you or finished unseen, Active the ones doing something
// (working, waiting or paused); both at once keep either; neither keeps everything. Kept across
// launches as a small bit set.
import Foundation

struct AgentFilter: OptionSet, Sendable, Hashable {
    let rawValue: Int

    static let needsYou = AgentFilter(rawValue: 1)
    static let active = AgentFilter(rawValue: 2)

    /// The menu's words for the current switches.
    var title: String {
        switch self {
        case []: "All agents"
        case .needsYou: "Needs you"
        case .active: "Active"
        default: "Needs you · Active"
        }
    }

    var icon: String {
        switch self {
        case []: "line.3.horizontal.decrease"
        case .needsYou: "bell.badge"
        case .active: "bolt"
        default: "line.3.horizontal.decrease.circle"
        }
    }

    /// What an instance with no agent left after the filter says.
    var emptyText: String {
        switch self {
        case []: "no agents"
        case .needsYou: "nothing waiting"
        case .active: "nothing running"
        default: "nothing running or waiting"
        }
    }

    /// Whether an agent passes: either switch that is on may keep it.
    func keeps(_ a: Agent) -> Bool {
        if isEmpty { return true }
        if contains(.needsYou), a.state == .blocked || a.unseen > 0 { return true }
        if contains(.active), a.state == .working || a.state == .blocked || a.state == .paused { return true }
        return false
    }

    /// The agents this filter keeps, in attention order whenever something is filtered out.
    func apply(_ agents: [Agent]) -> [Agent] {
        if isEmpty { return agents }
        return agents.filter(keeps).sorted { ($0.state.priority, $0.name) < ($1.state.priority, $1.name) }
    }
}
