#!/usr/bin/env python3
"""Tests for locus.py against a throwaway git repo. Run: python3 test_locus.py"""
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
LOCUS = os.path.join(HERE, "locus.py")
GRAMMAR = os.path.join(HERE, "id-grammar.json")

REGISTER = """# Register

**R-01 (OBSERVED) · tracked by a finding.**
*Earned by: finding-012.*

**R-02 (OBSERVED) · tracked by a file that exists.**
*Earned by: cmd/tool/main.go read this sitting.*

**R-03 (OBSERVED) · a file that does not exist does not count.**
*Earned by: missing.go read this sitting.*

**R-04 (OBSERVED) · notes only.**
*Earned by: O-12; FR-9.*

**R-05 (OBSERVED) · a slug observation.**
*Earned by: O idle-seats-never-poll.*

**R-06. A ruling with no record.**
*Earned by: operator ruling 2026-09-10.*
*Source: RULED.*

**R-07 (JUDGMENT) · session only.**
*Earned by: the roundtable.*

**R-08 (OBSERVED) · prose only, no Earned-by line.**

**R-09 (JUDGMENT) · an Earned-by line with no id.**
*Earned by: the zombie analysis.*

**R-10 (OBSERVED) · a notes path.**
*Earned by: replay pilot 01 (sim/notes/replay-pilot-01.md).*

**R-11 (OBSERVED) · an operator correction with no record.**
*Earned by: the operator's correction 2026-09-26.*

**R-12 (JUDGMENT) · an adversarial pass.**
*Earned by: the succession adversarial pass.*

**R-13 (OBSERVED) · a dated observation id counts once.**
*Earned by: O-2026-09-25-gate-lost-in-hop.*

**R-14 (OBSERVED) · a path at the end of a sentence.**
*Earned by: the parser in cmd/tool/main.go.*

**R-15 (OBSERVED) · a dated observation in prose.**
*Earned by: O 2026-09-26 four panes stalled.*

**R-16 (OBSERVED) · another one on the same date.**
*Earned by: O 2026-09-26 a review stranded; O-12.*

**R-17 (OBSERVED) · a bare name with a line that exists only deeper.**
*Earned by: main.go:3.*

**R-18 (JUDGMENT) · a path relative to the register.**
*Earned by: `specs/a.md` R5.*
"""

EXPECT = {
    "R-01": "tracked",
    "R-02": "tracked",
    "R-03": "session-relative",
    "R-04": "notes-only",
    "R-05": "notes-only",
    "R-06": "ruling-no-record",
    "R-07": "session-relative",
    "R-08": "none",
    "R-09": "unresolved",
    "R-10": "notes-only",
    "R-11": "ruling-no-record",
    "R-12": "session-relative",
    "R-13": "notes-only",
    "R-14": "tracked",
    "R-15": "notes-only",
    "R-16": "notes-only",
    "R-17": "unresolved",
    "R-18": "tracked",
}


def sh(cwd, *args):
    return subprocess.run(args, cwd=cwd, capture_output=True, check=True).stdout.decode()


class LocusTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        d = self.tmp.name
        os.makedirs(os.path.join(d, "sim"))
        os.makedirs(os.path.join(d, "cmd", "tool"))
        with open(os.path.join(d, "cmd", "tool", "main.go"), "w") as f:
            f.write("package main\n")
        os.makedirs(os.path.join(d, "sim", "specs"))
        with open(os.path.join(d, "sim", "specs", "a.md"), "w") as f:
            f.write("# a\n")
        with open(os.path.join(d, "sim", "requirements.md"), "w") as f:
            f.write(REGISTER)
        sh(d, "git", "init", "-q")
        sh(d, "git", "add", ".")
        # Commits inherit the host git config, signing included; the repo is never pushed.
        sh(d, "git", "commit", "-q", "-m", "fixture")
        self.repo = d
        self.pin = sh(d, "git", "rev-parse", "HEAD").strip()

    def tearDown(self):
        self.tmp.cleanup()

    def run_locus(self, *extra):
        out = subprocess.run(
            [sys.executable, LOCUS, self.repo, self.pin, GRAMMAR, *extra],
            capture_output=True,
        )
        return out.returncode, out.stdout.decode(), out.stderr.decode()

    def test_each_locus(self):
        rc, out, err = self.run_locus()
        self.assertEqual(rc, 0, err)
        got = {}
        for line in out.strip().split("\n")[1:]:
            cols = line.split("\t")
            got[cols[0]] = cols[7]
        self.assertEqual(got, EXPECT)

    def test_session_rows_are_anchored(self):
        rc, out, _ = self.run_locus("--ids", "R-07")
        self.assertEqual(rc, 0)
        anchor = out.strip().split("\n")[1].split("\t")[8]
        self.assertTrue(anchor.startswith(self.pin[:7]), anchor)

    def test_summary_counts(self):
        rc, out, _ = self.run_locus("--summary")
        self.assertEqual(rc, 0)
        self.assertIn("entries\t18", out)
        self.assertIn("tracked\t4", out)
        self.assertIn("notes-only\t6", out)

    def test_overlapping_ids_count_once(self):
        rc, out, _ = self.run_locus("--ids", "R-13")
        self.assertEqual(rc, 0)
        self.assertEqual(out.strip().split("\n")[1].split("\t")[4], "1")

    def ledger_rows(self, path):
        rc, out, err = self.run_locus("--ledger", path)
        self.assertEqual(rc, 0, err)
        return [l for l in out.split("\n") if l.startswith("| EV-")]

    def test_ledger_rows_are_withheld_and_dense(self):
        rows = self.ledger_rows(os.path.join(self.repo, "no-ledger.md"))
        ids = [r.split("|")[1].strip() for r in rows]
        self.assertEqual(ids, [f"EV-{k:04d}" for k in range(1, len(rows) + 1)])
        self.assertTrue(all(r.rstrip().endswith("| withheld |") for r in rows))

    def test_ledger_never_prints_a_native_id(self):
        rows = "\n".join(self.ledger_rows(os.path.join(self.repo, "no-ledger.md")))
        for native in ("O-12", "FR-9", "gate-lost-in-hop", "replay-pilot", "2026-09-26 four"):
            self.assertNotIn(native, rows)

    def test_shared_id_is_one_row_and_a_bare_date_is_not_shared(self):
        rows = self.ledger_rows(os.path.join(self.repo, "no-ledger.md"))
        cites = [r.split("|")[3].strip() for r in rows]
        self.assertIn("R-04, R-16", cites)  # O-12 cited by both
        self.assertIn("R-15", cites)
        self.assertNotIn("R-15, R-16", cites)

    def test_ledger_refuses_to_extend(self):
        path = os.path.join(self.repo, "ledger.md")
        with open(path, "w") as f:
            f.write("| EV-0001 | 2026-09-10 | R-04 | withheld |\n")
        rc, _, err = self.run_locus("--ledger", path)
        self.assertNotEqual(rc, 0)
        self.assertIn("crosswalk", err)

    def test_duplicate_definition_refused(self):
        path = os.path.join(self.repo, "sim", "requirements.md")
        with open(path, "a") as f:
            f.write("\n**R-01 (OBSERVED) · a second definition.**\n")
        sh(self.repo, "git", "commit", "-q", "-am", "dup")
        self.pin = sh(self.repo, "git", "rev-parse", "HEAD").strip()
        rc, _, err = self.run_locus()
        self.assertNotEqual(rc, 0)
        self.assertIn("second definition", err)

    def test_no_prose_in_output(self):
        rc, out, _ = self.run_locus()
        self.assertEqual(rc, 0)
        for word in ("roundtable", "zombie", "sitting", "idle-seats", "replay-pilot"):
            self.assertNotIn(word, out)


if __name__ == "__main__":
    unittest.main()
