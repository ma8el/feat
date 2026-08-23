# Logo

Feat's mark is a prompt chevron opening onto a beam, with two shorter lanes
branching off it and a cursor block ahead of each. The longest lane and the
leading block carry the product's colour; everything else is ink.

| File | What it is |
| --- | --- |
| `feat-wordmark-light.svg` | Mark and wordmark, ink for a light background. |
| `feat-wordmark-dark.svg` | The same, ink for a dark background. |
| `feat-mark-light.svg` | The mark alone, ink for a light background. |
| `feat-mark-dark.svg` | The same, ink for a dark background. |
| `feat-favicon.svg` | The mark simplified for 16 px: no branch lanes, no cursor blocks, square caps. |

## Two colours, and why the ink comes in pairs

The ink is `#1a1d24` on light and `#e9edf7` on dark, and the accent is
`#f5a623` in both. They are the dashboard's own text and attention colours from
[`internal/ui/palette.go`](../../internal/ui/palette.go), so the logo and the
screen it introduces are coloured from one set of decisions rather than two.

The accent does not flip with the ink. The dashboard darkens it to an ochre on a
light background because there it has to be read — as a task's name and as a
resource bar — and `#f5a623` reads at 2:1 on white. A logo is under no such
obligation, so the hue stays the one the product is recognised by on both
backgrounds and only the ink moves.

The ink cannot do the same, which is why each drawing exists twice. GitHub
renders a README's SVG as an isolated document, where `currentColor` resolves to
black and a single file would be invisible on one of the two themes. The README
picks between the pair with `<picture>` and `prefers-color-scheme`.

The favicon is the one file that carries its own media query, because a browser
renders it as a document that can still see the reader's theme. Its ink is a
stroke attribute the stylesheet overrides rather than a variable the stylesheet
supplies, so a renderer that drops the `<style>` element draws a light-ink mark
instead of nothing at all.

## Where they are used

The README shows the wordmark. Nothing in the binary reads any of these: Feat is
a terminal program, and a desktop notification carries the icon of whichever
application posted it rather than one the sender chooses, so the notifications
carry the mark's chevron as text instead. The mark and the favicon are here as
the logo's other two forms.
