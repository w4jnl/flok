# flok brand assets

## The mark
A terminal bracket holding one chevron and a cursor. The bracket is the tmux
server, the chevron is the flock, the cursor is what an agent is waiting on.
Thin frame (2.9), heavy bird (5.3) — the frame is a container, not a subject.

## The two icons
| file | cursor | meaning |
| --- | --- | --- |
| `flok.png` | outline | at rest — nothing is waiting on you |
| `flok-dot.png` | solid, mark knocked out | waiting — an agent is blocked or finished unseen |

At 22px the menu bar only reliably shows **value**, not detail: the waiting
state fills the frame solid and knocks the mark out in negative, tripling ink
coverage. An interior cursor swap — a bar becoming a dot — is invisible below
about 32px, so the state lives in the whole icon rather than one part of it.

Optional and worth it: alternate the two images every ~1150 ms while anything
waits, and stop on focus. Motion outranks any static difference at this size,
and since the mark contains a terminal cursor, blinking is the one animation
it has a right to.

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
| `flok-hero.png` | README banner, rendered from `hero.html` (the tagline lockup on the ground colour) by `render-hero.sh` |

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
