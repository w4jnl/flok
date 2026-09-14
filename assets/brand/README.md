# flok brand assets

## The mark
A terminal bracket holding one chevron and a cursor. The bracket is the tmux
server, the chevron is the flock, the cursor is what an agent is waiting on.
Thin frame (2.9), heavy bird (5.3) — the frame is a container, not a subject.

## The two icons
| file | cursor | meaning |
| --- | --- | --- |
| `flok.png` | bar | at rest — nothing is waiting on you |
| `flok-dot.png` | dot | waiting — an agent is blocked or finished unseen |

The flock never moves; only the cursor changes. A wide thin bar vs a compact
solid disc is a shape change, which is what survives monochrome template
rendering at 16px. The dot is the "4" from `w4j`.

## Files
| file | use |
| --- | --- |
| `flok-mark.svg` | mark, `currentColor` — template icon, docs, inline |
| `flok-mark-dot.svg` | waiting variant |
| `flok-mark-teal.svg` | W4J teal, for light backgrounds |
| `flok-lockup.svg` | mark + wordmark, horizontal |
| `flok-lockup-tagline.svg` | lockup + "agent sidebar for tmux" |
| `flok-wordmark.svg` | `[flok]` — primary wordmark, mark-free contexts |
| `favicon.svg` | 32px, teal tile, white mark |

Wordmark SVGs reference JetBrains Mono 700 by name. Convert text to outlines
before shipping anywhere the font is not loaded.

## Palette — flok's state colours are the brand palette
| token | hex | role |
| --- | --- | --- |
| W4J teal | `#12999D` | brand tie — who made this |
| working | `#3FD0D4` | primary / running |
| blocked | `#E8963C` | attention |
| done | `#5FC47A` | success |
| idle | `#8A999C` | muted |
| ground | `#14181A` | terminal background |

## Type
JetBrains Mono 700/800 for the wordmark and all UI. The app lives in a
terminal; a mono wordmark is the honest choice and it is the face the sidebar
is already read in.

## ASCII lockup
For `--version`, `flok doctor`, prompts and footers:

```
  ╭─────────╮
  │  ⩓      │   flok 0.4.2
  │  ▁      │   agent sidebar for tmux
  ╰─────────╯   w4j.nl · MIT

[⩓▁] flok  ·  3 sessions · 2 agents · ● 1 waiting
```

## Clear space
One bracket-width (3.9 units on the 24 grid) on all sides. Never place the
mark on a background between #4A5A5D and #A8B4B6 — the frame loses contrast.
