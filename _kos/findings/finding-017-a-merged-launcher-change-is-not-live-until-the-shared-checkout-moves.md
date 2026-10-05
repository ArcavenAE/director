# finding-017: a merged launcher change is not live until the shared checkout moves, and nothing reports the gap

- **Date:** 2026-10-02
- **Session:** the arcaven builder seat, placing the 2026-10-02 team harvest
- **Subject:** how sim/twin/cast-launch.sh reaches the seats it casts
- **Confidence:** one live instance, reported by two seats; the deploy path read from the private seat wrapper's exec line

## 0. The sentence

**Seats are cast by the launcher in a shared working checkout of this repo,
not by main, so a merged change to the launcher reaches no seat until someone
fast-forwards that checkout, and no seat, spawn line or roster shows which
launcher revision cast it.**

## 1. What was observed

#175 (the C-5 opt-in) merged at 2026-10-02T00:54Z. The two C-0 seats were
then cast with `DIRECTOR_CUE=1` in their role environment, and both showed
`cue: unverified`. Their claude processes carried no
`--dangerously-load-development-channels`. The private seat wrapper execs
the launcher from the shared director checkout, which still sat at d7ddf16,
three commits behind and before #175. The shims had the cue only because the
variable was inherited. A clean `git pull --ff-only` of that checkout to
d3e736f put the flag in the launcher (`grep -c` went from 0 to 1).

## 2. Why it matters

- The C-0 measurement would have failed for a reason that has nothing to do
  with the cue. It was caught because the processes were read by hand.
- A launcher fix that guards something, such as #174's scope or the
  spawn-line note #175 added for a dangerous flag, is not in force when the
  PR merges. A reviewer's approval describes main, not what casts seats.
- The shared checkout is also a working copy that sessions use, so it moves
  only when someone pulls it, and a dirty tree would block the pull.

## 3. What this does not establish

How often the checkout lags, or whether another wrapper execs a different
copy. Options for a fix, none chosen: the spawn line names the launcher's
own revision; the wrapper execs a pinned install, not a working tree; or
the wrapper refuses a launcher behind origin/main.

## Related

- finding-018: the shim's build stamp has the same blindness for a different
  binary.
- question-director-seat-startup, "Rough spots": a stale build is
  indistinguishable from a current one; this is the launcher's version of
  it.
