# Corpus-fitness probe: the mechanical half

The probe in `_kos/probes/brief-sim-corpus-fitness.yaml` asks whether the requirements register can feed a reverse brief. This directory holds the parts a parser writes, so the counts in the probe's finding can be re-run by anyone at the same commit.

- **Pin:** director `62b55fc`. The register there holds R-01 to R-230, one definition line each.
- **`id-grammar.json`:** the evidence id grammar (design `sim/design/corpus-to-brief-and-spectacle.md`, section 4 item 4), committed before the parser.
- **`locus.py`:** applies the grammar to each entry's Earned-by line. Python 3 standard library only. It prints ids, counts and loci, never the register's prose.
- **`test_locus.py`:** its tests, against a throwaway fixture repository. They run in a clean HOME: the fixture's git identity comes from the environment, and signing is left as the host configures it.
- **`projection.tsv`:** the parser's output at the pin, one row per entry. Nothing in it is written by hand.

## Columns

`id`, `line` (of the definition line), `class` (the first source class), the number of `tracked`, `notes`, `ruling` and `session` tokens found on the Earned-by line, the `locus`, and `introduced` (for a session-relative entry, the commit and date that added its definition line).

## Loci

An entry takes the first locus that applies, in this order:

1. **tracked:** at least one token names a tracked finding, ticket, pull request, or a file that resolves as written at the pin;
2. **notes-only:** only tokens that resolve into the gitignored `sim/notes/`;
3. **ruling-no-record:** an operator ruling or correction with no record named;
4. **session-relative:** "this session", "this sitting", a roundtable or an adversarial pass;
5. **unresolved:** an Earned-by line with no token the grammar recognizes. These mean the grammar is unfinished, not that the evidence is missing;
6. **none:** no Earned-by line at all.

## Re-run

From the repository root:

```sh
python3 sim/research/corpus-fitness/test_locus.py
python3 sim/research/corpus-fitness/locus.py . 62b55fc sim/research/corpus-fitness/id-grammar.json > sim/research/corpus-fitness/projection.tsv
python3 sim/research/corpus-fitness/locus.py . 62b55fc sim/research/corpus-fitness/id-grammar.json --summary
```
