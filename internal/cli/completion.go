package cli

import (
	"fmt"
	"strings"
)

// command is the public CLI surface used to generate shell completions.
type command struct {
	name, help string
	flags      []string
}

var commands = []command{
	{"up", "start or re-attach the sidebar session", []string{"--detach"}},
	{"down", "stop the outer session", nil},
	{"keep-awake", "keep the Mac awake while flok runs", nil},
	{"status", "print sessions and agents once", []string{"--json"}},
	{"jump", "switch to the newest agent needing input", []string{"--client"}},
	{"next", "next agent pane", []string{"--client"}},
	{"prev", "previous agent pane", []string{"--client"}},
	{"toggle", "sidebar full width <-> rail", nil},
	{"hide", "hide or show the sidebar", nil},
	{"focus", "put the keyboard in the sidebar", nil},
	{"reload", "restart the sidebar pane (re-reads config.toml)", nil},
	{"keys", "keybinds help", []string{"--print", "--filter"}},
	{"explain", "show matching screen-detection rules for a pane", nil},
	{"doctor", "check the installation", nil},
	{"theme", "show or switch the light/dark palette", nil},
	{"install", "wire agent hooks and print the tmux snippet", []string{"--claude", "--copilot", "--tmux"}},
	{"completion", "print a shell completion script", nil},
	{"version", "print the version", nil},
	{"help", "show usage", nil},
}

var flagHelp = map[string]string{
	"--detach": "create the outer session without attaching", "--json": "machine-readable output",
	"--client": "inner client tty to drive", "--print": "dump the help as text", "--filter": "keep bindings matching a substring",
	"--claude": "Claude Code hooks", "--copilot": "Copilot CLI hooks", "--tmux": "print the tmux.conf snippet",
}

// runCompletion prints the completion script for bash or zsh.
func runCompletion(args []string) int {
	shell := ""
	if len(args) > 0 {
		shell = args[0]
	}
	switch shell {
	case "bash":
		fmt.Print(bashCompletion())
	case "zsh":
		fmt.Print(zshCompletion())
	default:
		fmt.Println("usage: flok completion <bash|zsh>")
		fmt.Println("  bash: echo 'eval \"$(flok completion bash)\"' >> ~/.bashrc")
		fmt.Println("  zsh:  echo 'eval \"$(flok completion zsh)\"'  >> ~/.zshrc   (after compinit), or")
		fmt.Println("        flok completion zsh > ~/.zfunc/_flok  with ~/.zfunc in $fpath before compinit")
		return 2
	}
	return 0
}

func commandNames() []string {
	names := make([]string, 0, len(commands))
	for _, c := range commands {
		names = append(names, c.name)
	}
	return names
}

func bashCompletion() string {
	var b strings.Builder
	b.WriteString("# bash completion for flok — install with: eval \"$(flok completion bash)\"\n")
	b.WriteString("_flok() {\n")
	b.WriteString("    local cur prev cmd\n    COMPREPLY=()\n")
	b.WriteString("    cur=${COMP_WORDS[COMP_CWORD]}\n    prev=${COMP_WORDS[COMP_CWORD-1]}\n    cmd=${COMP_WORDS[1]}\n")
	b.WriteString("    if [ \"$COMP_CWORD\" -eq 1 ]; then\n")
	fmt.Fprintf(&b, "        COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") )\n        return 0\n    fi\n", strings.Join(commandNames(), " "))
	b.WriteString("    case \"$cmd\" in\n")
	b.WriteString("        completion) COMPREPLY=( $(compgen -W \"bash zsh\" -- \"$cur\") ) ;;\n")
	b.WriteString("        keep-awake) COMPREPLY=( $(compgen -W \"on off toggle status\" -- \"$cur\") ) ;;\n")
	b.WriteString("        explain) COMPREPLY=( $(compgen -W \"$(tmux list-panes -a -F '#{pane_id}' 2>/dev/null)\" -- \"$cur\") ) ;;\n")
	b.WriteString("        jump|next|prev)\n            if [ \"$prev\" = --client ]; then\n")
	b.WriteString("                COMPREPLY=( $(compgen -W \"$(tmux list-clients -F '#{client_tty}' 2>/dev/null)\" -- \"$cur\") )\n")
	b.WriteString("            else\n                COMPREPLY=( $(compgen -W \"--client\" -- \"$cur\") )\n            fi ;;\n")
	b.WriteString("        keys)\n            if [ \"$prev\" = --filter ]; then return 0; fi\n")
	b.WriteString("            COMPREPLY=( $(compgen -W \"--print --filter\" -- \"$cur\") ) ;;\n")
	for _, c := range commands {
		switch c.name {
		case "completion", "explain", "jump", "next", "prev", "keys":
			continue
		}
		if len(c.flags) > 0 {
			fmt.Fprintf(&b, "        %s) COMPREPLY=( $(compgen -W \"%s\" -- \"$cur\") ) ;;\n", c.name, strings.Join(c.flags, " "))
		}
	}
	b.WriteString("    esac\n    return 0\n}\ncomplete -F _flok flok\n")
	return b.String()
}

func zshCompletion() string {
	var b strings.Builder
	b.WriteString("#compdef flok\n")
	b.WriteString("# zsh completion for flok — install with: eval \"$(flok completion zsh)\" (after compinit),\n")
	b.WriteString("# or save as _flok in a directory on $fpath before compinit.\n")
	b.WriteString("_flok() {\n    local -a cmds\n    cmds=(\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "        '%s:%s'\n", c.name, strings.ReplaceAll(c.help, ":", " "))
	}
	b.WriteString("    )\n    if (( CURRENT == 2 )); then\n        _describe -t commands 'flok command' cmds\n        return\n    fi\n")
	b.WriteString("    case ${words[2]} in\n")
	b.WriteString("        completion) _values 'shell' bash zsh ;;\n")
	b.WriteString("        keep-awake) _values 'keep-awake' on off toggle status ;;\n")
	b.WriteString("        explain)\n            local -a panes\n            panes=(${(f)\"$(tmux list-panes -a -F '#{pane_id}' 2>/dev/null)\"})\n            _describe -t panes 'pane' panes ;;\n")
	b.WriteString("        jump|next|prev)\n            _arguments '--client[inner client tty to drive]:tty:($(tmux list-clients -F \"#{client_tty}\" 2>/dev/null))' ;;\n")
	b.WriteString("        keys)\n            _arguments '--print[dump the help as text]' '--filter[keep bindings matching a substring]:filter' ;;\n")
	for _, c := range commands {
		switch c.name {
		case "completion", "explain", "jump", "next", "prev", "keys":
			continue
		}
		if len(c.flags) == 0 {
			continue
		}
		var specs []string
		for _, f := range c.flags {
			specs = append(specs, fmt.Sprintf("'%s[%s]'", f, flagHelp[f]))
		}
		fmt.Fprintf(&b, "        %s) _arguments %s ;;\n", c.name, strings.Join(specs, " "))
	}
	b.WriteString("    esac\n}\n")
	b.WriteString("if [ \"${funcstack[1]}\" = _flok ]; then\n    _flok \"$@\"\nelse\n    compdef _flok flok\nfi\n")
	return b.String()
}
