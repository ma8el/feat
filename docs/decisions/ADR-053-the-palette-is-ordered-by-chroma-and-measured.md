# ADR-053 — The palette is ordered by chroma, and measured

Status: accepted  
Recorded: 2026-08-12, alpha review, after ADR-052

ADR-051 chose the six colours as a set and said what the set was for: the
selection colour and the attention colour at one weight, so that the eye reads
the difference between them as meaning rather than as loudness. It did not say
what weight meant, and the values it shipped were taken from an existing terminal
theme. The maintainer read the result in use and reported that the orange was
right and the blue was not.

Evidence, all of it measured in OKLCH and in simulated colour-vision deficiency
(Machado 2009 at full severity) rather than argued from taste:

1. The pair was not at one weight after all. On dark the accent carried chroma
   0.132 against the attention colour's 0.106, so the blue led; on light it was
   0.178 against 0.108, which is half again as much. The rule the ADR set was
   satisfied on neither theme.
2. The accent shared a hue with every neutral in the palette — 264° against the
   text's 268.5°, the muted colour's 269°, the rule's 272.7°. The effect is
   smaller than it sounds, because those neutrals are nearly grey, but it is
   exactly where the focused card's border changes: rule to accent was a change of
   lightness and chroma with no change of hue.
3. The palette's closest pair was not the one the ADR was about. Attention against
   failure measured 0.086 in OKLab distance under deuteranopia — orange and red
   are neighbours, and at the old weights lightness was nearly all that separated
   them. The pair the ADR did protect measured 0.254.
4. A cooler accent was proposed, argued for on hue separation, and withdrawn on
   measurement: teal at 200° loses two fifths of the accent/attention separation
   under the two common deficiencies, and at the light theme's lightness sRGB
   holds no more than 0.086 chroma there — less than the orange's 0.108 — so the
   equal-weight rule would have been unsatisfiable on one theme by construction.
   Violet is worse: 0.094 against the orange under tritanopia.

Decisions:

- The set is ordered by chroma: failure above attention, attention level with the
  accent. Hue and lightness distinguish colours within a rank; chroma is the rank.
- The accent stays blue and is re-weighted to the orange rather than re-hued away
  from it. Nearly opposite in hue and lower in lightness is the pairing that keeps
  working when a channel is missing.
- The dark values are `#53a0ff`, `#f5a623`, and `#ff6287`. The red moved because
  the orange did: at the orange's new chroma the old red no longer outranked it.
- The light values are `#1f4e88`, `#8a5a00`, and `#a8202a`. The light accent
  copies the dark relationship — same chroma as the orange, about a tenth darker —
  rather than matching it on every axis, which measured worse: a pair matched on
  lightness and chroma has only hue left to lose.
- The light theme's orange is not the dark one adjusted. `#f5a623` reads at 2.03:1
  on white, and `#8a5a00` is already the most chroma the hue holds at a lightness
  that is legible there. The product's colour is a dark-theme value, and the light
  theme has an ochre standing in for it.

Consequence: every pair in the palette separates further than it did, including
the one nobody had looked at — attention against failure goes from 0.086 to 0.135
under deuteranopia, and accent against attention from 0.254 to 0.334. Nothing
outside `internal/ui/palette.go` changes; no format, endpoint, or state moves. The
figures above are reproducible from the hexes in that file, and a future change to
them should be measured the same way rather than compared by eye.
