package cli

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/qr"
)

// pairScreen is the pairing picture `flok phone pair` draws in a terminal or a tmux popup: the
// QR, the code in letters large enough to type from across a desk, the relay, and a countdown
// that turns into the outcome, from the reply file the sidebar writes (paired) or the clock
// (expired). With hold the screen stays after the outcome until a key, for a popup that would
// otherwise vanish with it.
type pairScreen struct {
	qr      *qr.Matrix
	code    string
	relay   string
	expires time.Time
	path    string
	hold    bool

	width, height int
	outcome       string
	paired        bool
	name          string
}

type pairTickMsg time.Time

func pairTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return pairTickMsg(t) })
}

func (s pairScreen) Init() tea.Cmd { return pairTick() }

func (s pairScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return s, tea.Quit
		case "enter":
			if s.outcome != "" {
				return s, tea.Quit
			}
		}
	case pairTickMsg:
		if s.outcome != "" {
			return s, nil
		}
		if f, ok := readReply(s.path); ok && isPaired(f) {
			s.paired, s.name = true, f.Phone.Phone.Name
			s.outcome = "paired: " + s.name
		} else if !time.Time(msg).Before(s.expires) {
			s.outcome = "the code expired: run flok phone pair again"
		}
		if s.outcome != "" { // nothing more to watch: leave, or wait for a key
			if s.hold {
				return s, nil
			}
			return s, tea.Quit
		}
		return s, pairTick()
	}
	return s, nil
}

func (s pairScreen) View() string {
	var b strings.Builder
	cols, rows := s.qr.TerminalSize(4)
	if s.width == 0 || (s.width >= cols+2 && s.height >= rows+7) {
		b.WriteString("\n")
		for _, line := range strings.Split(strings.TrimSuffix(s.qr.Terminal(4), "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("\n  the window is too small for the QR: type the code in the app\n\n")
	}
	fmt.Fprintf(&b, "  code    \x1b[1m%s\x1b[0m\n", spaced(s.code))
	fmt.Fprintf(&b, "  relay   %s\n", s.relay)
	b.WriteString("  scan it with the Camera app, or in flok › Settings › Pair this phone\n")
	switch {
	case s.outcome != "" && s.hold:
		fmt.Fprintf(&b, "  %s\n  Enter closes\n", s.outcome)
	case s.outcome != "":
		fmt.Fprintf(&b, "  %s\n", s.outcome)
	default:
		left := time.Until(s.expires).Round(time.Second)
		if left < 0 {
			left = 0
		}
		fmt.Fprintf(&b, "  the code lasts %d:%02d more\n  q closes\n", int(left.Minutes()), int(left.Seconds())%60)
	}
	return b.String()
}

// spaced puts a space between the characters of a code: A B 7 Q - K 2 M N.
func spaced(code string) string {
	parts := make([]string, 0, len(code))
	for _, r := range code {
		parts = append(parts, string(r))
	}
	return strings.Join(parts, " ")
}

// runPairScreen runs the screen until the outcome or a key and returns the final state; tests
// drive Update and View by hand instead.
var runPairScreen = func(s pairScreen) (pairScreen, error) {
	p := tea.NewProgram(s, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return s, err
	}
	if out, ok := final.(pairScreen); ok {
		return out, nil
	}
	return s, nil
}
