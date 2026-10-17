package cli

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/w4jnl/flok/internal/qr"
)

// codeScreen is the picture `flok phone pair` and `flok link join` draw in a terminal or a tmux
// popup: the QR, the code in letters large enough to type from across a desk, the relay, a hint,
// and a countdown that turns into the outcome. Every tick asks watch where things stand: still
// waiting, done (the outcome to show), or, for a join, approved: then the screen says by whom
// and waits for Enter (accept) or Esc (decline) before it is done. With hold the screen stays
// after the outcome until a key, for a popup that would otherwise vanish with it.
type codeScreen struct {
	qr      *qr.Matrix
	code    string
	relay   string
	hint    string // the line under the relay: what to do with the code
	expires time.Time
	expired string // the outcome when the clock runs out
	hold    bool
	watch   func() (codeStep, string) // each tick: where things stand, and the words for it
	accept  func() (string, error)    // approved + Enter: finish, with the outcome
	decline func()                    // approved + Esc (or q): withdraw
	yes     bool                      // accept an approval without asking (--yes)
	width   int
	height  int
	step    codeStep
	said    string // the approval's words, while asking
	outcome string
	err     error
}

// codeStep is where a code screen stands.
type codeStep int

const (
	stepWaiting  codeStep = iota
	stepApproved          // a join: approved, the user says yes or no
	stepDone              // the outcome is in
	stepFailed            // the code expired, or the relay said no
)

type codeTickMsg time.Time

func codeTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return codeTickMsg(t) })
}

func (s codeScreen) Init() tea.Cmd { return codeTick() }

func (s codeScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			if s.step == stepApproved && s.decline != nil {
				s.decline()
				s.step, s.outcome = stepFailed, "declined: nothing was kept on the relay"
				if s.hold {
					return s, nil
				}
			}
			return s, tea.Quit
		case "enter":
			switch {
			case s.step == stepApproved:
				return s.finish()
			case s.step == stepDone || s.step == stepFailed:
				return s, tea.Quit
			}
		}
	case codeTickMsg:
		if s.step != stepWaiting {
			return s, nil
		}
		step, text := s.watch()
		switch {
		case step == stepApproved && s.accept == nil: // nothing to confirm: it is the outcome
			step = stepDone
		case step == stepApproved && s.yes:
			s.step, s.said = stepApproved, text
			return s.finish()
		}
		switch step {
		case stepApproved:
			s.step, s.said = step, text
			return s, nil // the user's turn: Enter or Esc
		case stepDone, stepFailed:
			s.step, s.outcome = step, text
		default:
			if !time.Time(msg).Before(s.expires) {
				s.step, s.outcome = stepFailed, s.expired
			}
		}
		if s.step == stepWaiting {
			return s, codeTick()
		}
		if s.hold { // nothing more to watch: wait for a key
			return s, nil
		}
		return s, tea.Quit
	}
	return s, nil
}

// finish takes an approval: accept runs, and its word is the outcome.
func (s codeScreen) finish() (tea.Model, tea.Cmd) {
	out, err := s.accept()
	if err != nil {
		s.step, s.outcome, s.err = stepFailed, err.Error(), err
	} else {
		s.step, s.outcome = stepDone, out
	}
	if s.hold {
		return s, nil
	}
	return s, tea.Quit
}

func (s codeScreen) View() string {
	var b strings.Builder
	cols, rows := s.qr.TerminalSize(4)
	if s.width == 0 || (s.width >= cols+2 && s.height >= rows+8) {
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
	b.WriteString("  " + s.hint + "\n")
	switch {
	case s.step == stepApproved:
		fmt.Fprintf(&b, "  %s\n  Enter links this flok, Esc declines\n", s.said)
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

// runCodeScreen runs the screen until the outcome or a key and returns the final state; tests
// drive Update and View by hand instead.
var runCodeScreen = func(s codeScreen) (codeScreen, error) {
	p := tea.NewProgram(s, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return s, err
	}
	if out, ok := final.(codeScreen); ok {
		return out, nil
	}
	return s, nil
}
