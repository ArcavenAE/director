# A task judge: a reviewer for tasks, not code

- **Status:** idea (pre-hypothesis, no commitment).
- **Date:** 2026-10-24
- **Subject:** director. The question is a coordination one: what a team's
  supervisor is told about a task before dispatch and at handoff, and who
  tells it. The role text itself would live in the role library, which has no graph yet.
- **Asked by:** the operator, via director (bus 01M5QS01YPSPNGX96FT9TBHS1E).
- **Related, not merged:** orc `_kos/ideas/review-as-a-team-service.md` and
  `question-review-as-a-team-service`, whose judge triages code-review
  findings. This judge reads tasks.

## The ask (operator's words, preserved)

> should we incorporate more judge roles to assess effort, depth,
> completeness appropriate for a task, or to keep tasks aligned with the
> broader project goals as a common feature in teams, based on anthropic or
> other LLM research? if so, what might that look like? something like a
> reviewer, but for tasks rather than code?

## Short answer

Yes, as a shadow first, and only as a proposer. The fleet's own record shows
the failure it would catch, and outside work shows a judge built this way
agrees with people most of the time. The same research says a judge on the
author's own model, or a score that becomes a gate, makes things worse.

## 1. Why do it at all

The fleet's records show the failure is in tasks, not code:

- partial completion is about 46% of the cases where a ticket outlives its
  fix (finding-152);
- authors check premises only where they already doubt them, and three of
  four confident claims were wrong (finding-126);
- false premises in batch-filed tickets (finding-103), the wrong output form
  (finding-122), coverage claimed for an object never examined (finding-095),
  and unverified results shown as verified (finding-188).

The first measured plan party (3ptdd on marvel#452) missed all three defects
review later found, and two of them were in a plan the party never saw.

Outside results point the same way:
- MAST measured premature termination at 6.2%, incomplete verification at
  8.2% and task-spec violation at 11.8% of multi-agent failures, and a
  task-objective verification step raised one system's success by 15.6% [12].
- Every model tested drifted from its goal over long runs [13].
- Strong models inherit drift from a weaker agent's trajectory [14].

Two reasons to hesitate:
- No finding measures effort mismatch or scope drift directly.
- Three partial task judges already exist on paper: the retrospector's
  full/partial/none grade after merge, the ptdd red-test check, and
  aae-orc-mr4c3's scope-and-intent reviewer, not yet minted.

So the first step measures rather than builds.

## 2. What it reads

- **The ask.** The ticket or envelope, verbatim, with its acceptance lines.
- **The goals.** The project's charter or the named goal the ticket cites. If
  none is cited, the judge says so; it does not infer a goal.
- **The output.** The artifacts themselves: diff, files, PR, finding, report.
  It works with workspace access, as agent-as-a-judge does [11]. The author's
  own summary comes last or not at all, because a capable author's write-up
  can mislead a judge [6], and the judge reads it in a fresh context, never
  the author's transcript [14].

## 3. What it emits

One confirm-or-override proposal to the supervisor that owns the task, never
to the author and never as a gate (ADR-007, SOUL section 8,
`diagnostic-not-gate.md`). The proposal scores four separate dimensions,
each with an explicit Unknown [3]:

1. **Requirements met.** Each acceptance line, with the artifact that meets it
   or "not found".
2. **Verification done.** Was the claimed check run, and is its output there?
   This grades what was produced, not the claim [3].
3. **Effort fit.** Within the band set at kickoff. Over-effort is flagged as
   well as under-effort [18], and length is never read as effort [10].
4. **Goal fit.** Does the work serve the cited goal, or does it drift?

A structural check, such as "every ticket names its red tests", may gate
through a ratified rule. The four scores stay diagnostic. Closure stays a
human or supervisor act; the judge never closes, reopens or re-scopes.

There is a naming clash to settle: the role library uses "judge" to mean assess,
while ADR-007 uses "judging" to mean decide. A name such as `task-assessor`
would avoid it.

## 4. Where it sits

- **Before dispatch (sizing).** It reads the ask and the goal and proposes an
  effort band and a rubric before work starts. Judging against the ask's own
  reference cut wrong-answer approvals from 70% to 15% in one measurement [4].
- **At handoff (depth).** It runs when a seat says done: the four scores
  against the rubric set at kickoff.
- **Periodically (alignment).** For multi-session work only: goal fit against
  the charter, at a cadence the supervisor sets. Small tasks may not need a
  judge at all [15, 18].

## 5. Not grading its own team's model

- The judge runs on a model family different from the author's, ideally as a
  small mixed panel [9]. GPT-4's self-preference reached 0.912 in one
  measurement [7], and judges favour text that is familiar to them [8].
- The author's identity is hidden, though style may still give it away.
  Panel order is randomised: GPT-4 kept the same verdict after an order swap
  only 65% of the time [4].
- Calibration: operator spot checks at the start and periodically after, with
  judge-human agreement tracked as a diagnostic number [2, 3]. People
  disagree with each other 10 to 30% of the time on such tasks [11], so the
  target is people's own agreement rate, not 100%.
- A score never becomes a reward or a gate, because then it gets gamed [5, 16].

## 6. Cost

- One judge call per touch point, more for a panel. Token use alone explained
  80% of performance variance in Anthropic's research system [2], so the
  judge's own depth is a budget choice.
- Agent-as-a-judge cost about 2% of human evaluation time for its task set
  [11].
- Operator time is the real cost: the spot checks that keep the judge honest.
- Risk: noise in the supervisor's queue. Proposals the supervisor overrides
  most of the time are a signal to stop, not to tune the judge until it
  agrees.

## 7. Smallest first probe

A shadow judge over one week of closed tickets, outside any loop:

1. Take every bd ticket closed in one week across two or three teams, with its
   description, linked PR or finding, and the charter it names.
2. A judge on a model family other than the authors' scores each on the four
   dimensions, with Unknown allowed, blind to who did the work.
3. The operator spot-checks a random fifth, blind to the judge's scores, then
   compares.
4. Report:
   - judge-operator agreement per dimension;
   - the share the judge marks partial, a cross-check on finding-152's 46%;
   - false alarms;
   - cost per ticket.

Pass: agreement with the operator near people's own rate on at least
requirements-met and verification-done, and at least one partial completion
found that the close missed. Fail: agreement low, or every finding is one the
retrospector's merge grade already makes. Nothing is routed to any seat during
the probe.

## Open questions

1. A new role, or the four dimensions given to the retrospector and to
   aae-orc-mr4c3's scope-and-intent reviewer?
2. Who sets the effort band at kickoff when the ticket gives none: the judge,
   the supervisor, or the filer?
3. Which teams for the probe, and is a week enough tickets?
4. Should the plan-stage check be the ptdd red-test check, ratified, rather
   than a second instrument?

## Sources

Fleet: finding-095, finding-103, finding-122, finding-126, finding-152,
finding-168, finding-188 (orc `_kos/findings`); ADR-007; SOUL section 8;
`.claude/rules/diagnostic-not-gate.md`; the role library's retrospector 2.0.0;
ptdd-process-draft.md; beadle DESIGN.md; aae-orc-mr4c3.

Outside (fetched 2026-10-24):

1. Anthropic, "Building effective agents", 2024-12-19. https://www.anthropic.com/engineering/building-effective-agents
2. Anthropic, "How we built our multi-agent research system", 2025-06-13. https://www.anthropic.com/engineering/multi-agent-research-system
3. Anthropic, "Demystifying evals for AI agents", 2026-01-09. https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents ; and Claude docs, "Define success criteria and build evaluations". https://platform.claude.com/docs/en/test-and-evaluate/develop-tests
4. Zheng et al., "Judging LLM-as-a-Judge with MT-Bench and Chatbot Arena", 2023. https://arxiv.org/abs/2306.05685
5. Anthropic, "Natural emergent misalignment from reward hacking", 2025-11-21. https://www.anthropic.com/research/emergent-misalignment-reward-hacking
6. Gasteiger, Mikulik (Anthropic), "Automated Researchers Can Subtly Sandbag", 2025. https://alignment.anthropic.com/2025/automated-researchers-sandbag/
7. Panickssery, Bowman, Feng, "LLM Evaluators Recognize and Favor Their Own Generations", 2024. https://arxiv.org/abs/2404.13076
8. Wataoka, Takahashi, Ri, "Self-Preference Bias in LLM-as-a-Judge", 2024. https://arxiv.org/abs/2410.21819
9. Verga et al., "Replacing Judges with Juries", 2024. https://arxiv.org/abs/2404.18796
10. Dubois et al., "Length-Controlled AlpacaEval", 2024. https://arxiv.org/abs/2404.04475
11. Zhuge et al., "Agent-as-a-Judge: Evaluate Agents with Agents", 2024. https://arxiv.org/abs/2410.10934
12. Cemri et al., "Why Do Multi-Agent LLM Systems Fail?", 2025. https://arxiv.org/abs/2503.13657
13. Arike et al., "Technical Report: Evaluating Goal Drift in Language Model Agents", 2025. https://arxiv.org/abs/2505.02709
14. Menon et al., "Inherited Goal Drift: Contextual Pressure Can Undermine Agentic Goals", 2026. https://arxiv.org/abs/2603.03258
15. Kwa et al. (METR), "Measuring AI Ability to Complete Long Tasks", 2025. https://metr.org/blog/2025-03-19-measuring-ai-ability-to-complete-long-tasks/
16. Von Arx, Chan, Barnes (METR), "Recent Frontier Models Are Reward Hacking", 2025. https://metr.org/blog/2025-06-05-recent-reward-hacking/
17. Lightman et al., "Let's Verify Step by Step", 2023. https://arxiv.org/abs/2305.20050
18. Chen et al., "Do NOT Think That Much for 2+3=? On the Overthinking of o1-Like LLMs", 2024. https://arxiv.org/abs/2412.21187

Not fetched: OpenAI's two SWE-bench Verified pages (HTTP 403) and Anthropic's
scalable-oversight papers.
