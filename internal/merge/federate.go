package merge

// HostSnapshot is one remote host's merged view, as its own poller produced it.
type HostSnapshot struct {
	Host string
	Snap Snapshot
}

// Federate joins the local merge and the remote hosts' merges into the view the sidebar renders.
// Every agent and space is stamped with its host, agents are re-sorted by attention across
// hosts, unseen counts add up, remote warnings carry their host, and Focus (with the "current"
// space) comes from the front host, the one whose work pane is on screen. The write-back
// fields (NewlySeen, Corrections, StaleHooks) stay nil: each host's poller has already
// persisted them. Build is never re-run here; it counts samples, so only the host that changed
// may run it.
func Federate(local Snapshot, remotes []HostSnapshot, front string) Snapshot {
	out := Snapshot{TakenAt: local.TakenAt}
	add := func(host string, s Snapshot) {
		for _, sp := range s.Spaces {
			sp.Host = host
			sp.Current = sp.Current && host == front
			out.Spaces = append(out.Spaces, sp)
		}
		for _, a := range s.Agents {
			a.Host = host
			out.Agents = append(out.Agents, a)
		}
		out.Unseen += s.Unseen
		for _, w := range s.Warnings {
			if host != "" {
				w = host + ": " + w
			}
			out.Warnings = append(out.Warnings, w)
		}
		if host == front {
			out.Focus = s.Focus
			out.Focus.Host = host
		}
	}
	add("", local)
	for _, r := range remotes {
		add(r.Host, r.Snap)
	}
	SortAgents(out.Agents)
	return out
}
