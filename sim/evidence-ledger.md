# Evidence ledger

Public ids for the evidence `sim/requirements.md` cites from the gitignored `sim/notes/`, after section 4 item 5 of the design merged in director#288. A row holds only its EV id, the date its first citing entry entered the register, the R ids that cite it, its status and a disposition label. No row carries a note's text or its native id; the crosswalk from EV id to native id is kept outside this repository.

- **Allocation:** by `sim/research/corpus-fitness/locus.py --ledger`, at director `62b55fc`: dense, in first-citation order, append-only, never reused. This first allocation covers the corpus-fitness probe's 20 sampled entries, which yield 9 rows from 6 entries; the other 14 cite no notes and take no row.
- **Status:** every row is `withheld`. A row changes only when a seat that holds the note writes its text in its own scanned commit.
- **Disposition:** a judged label, not parser output: the research team's reading on 2026-10-25, one reader. `publishable` means the note can be rewritten into public text as is; `rewrite` gives its reason as a class only; `withheld` stays withheld.

| EV id | date | R ids | status | disposition |
|---|---|---|---|---|
| EV-0001 | 2026-09-10 | R-23 | withheld | rewrite (notes name private or third-party work) |
| EV-0002 | 2026-09-10 | R-23 | withheld | rewrite (notes name private or third-party work) |
| EV-0003 | 2026-09-25 | R-113 | withheld | rewrite (notes name private or third-party work) |
| EV-0004 | 2026-09-25 | R-122 | withheld | publishable |
| EV-0005 | 2026-09-25 | R-122 | withheld | publishable |
| EV-0006 | 2026-09-26 | R-149 | withheld | publishable |
| EV-0007 | 2026-09-27 | R-158 | withheld | rewrite (notes name private or third-party work) |
| EV-0008 | 2026-10-03 | R-176 | withheld | withheld |
| EV-0009 | 2026-10-03 | R-176 | withheld | withheld |
