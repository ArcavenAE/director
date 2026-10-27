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

**R-19 (OBSERVED) · a commit that is not in this history.**
*Earned by: the fix in deadbee.*

**R-20 (OBSERVED) · a commit that is.**
*Earned by: the fix in BASE_SHA.*
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
    "R-19": "unresolved",
    "R-20": "tracked",
}


# The fixture's identity comes from the environment, so the tests run in a
# clean HOME. Every fixture commit is signed with a throwaway key made for the
# run in its own temp dir, outside the fixture repo, and no host config or key
# is read.
KEYDIR = tempfile.TemporaryDirectory()
KEY = os.path.join(KEYDIR.name, "fixture-key")
SIGNING = {
    "gpg.format": "ssh",
    "gpg.ssh.program": "ssh-keygen",
    "user.signingkey": KEY,
    "commit.gpgsign": "true",
}
GIT_ENV = dict(
    os.environ,
    GIT_AUTHOR_NAME="t",
    GIT_AUTHOR_EMAIL="t@example.invalid",
    GIT_COMMITTER_NAME="t",
    GIT_COMMITTER_EMAIL="t@example.invalid",
    GIT_CONFIG_GLOBAL=os.devnull,
    GIT_CONFIG_NOSYSTEM="1",
    GIT_CONFIG_COUNT=str(len(SIGNING)),
)
for i, (k, v) in enumerate(SIGNING.items()):
    GIT_ENV[f"GIT_CONFIG_KEY_{i}"] = k
    GIT_ENV[f"GIT_CONFIG_VALUE_{i}"] = v


def setUpModule():
    subprocess.run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "fixture", "-f", KEY], check=True)


def tearDownModule():
    KEYDIR.cleanup()


def refused_arg(a):
    """Args sh() will not pass on: a config, a directory or a signing switch."""
    return a in ("-c", "-C") or a.startswith("--config-env") or a.startswith("--no-gpg")


def has_signature(obj):
    """Whether a commit object's text carries a gpgsig header, before the
    blank line that ends the headers."""
    for line in obj.splitlines():
        if not line:
            return False
        if line.startswith("gpgsig "):
            return True
    return False


def sh(cwd, *args):
    """Run a command in a fixture repo. A git call refuses a caller's config,
    directory or signing switch before it runs, and after it every commit any
    ref reaches must carry a signature."""
    if args[0] == "git":
        for a in args[1:]:
            if refused_arg(a):
                raise AssertionError("sh refuses caller config, directory or signing: " + a)
    out = subprocess.run(args, cwd=cwd, env=GIT_ENV, capture_output=True, check=True).stdout.decode()
    if args[0] == "git":
        shas = subprocess.run(["git", "rev-list", "--all"], cwd=cwd, env=GIT_ENV, capture_output=True, check=True)
        for sha in shas.stdout.decode().split():
            obj = subprocess.run(["git", "cat-file", "commit", sha], cwd=cwd, env=GIT_ENV, capture_output=True, check=True)
            if not has_signature(obj.stdout.decode()):
                raise AssertionError("fixture commit is not signed:\n" + obj.stdout.decode())
    return out


def commit(cwd, *args):
    """The one fixture commit site; sh() checks the signature."""
    sh(cwd, "git", "commit", *args)


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
        sh(d, "git", "init", "-q")
        sh(d, "git", "add", ".")
        commit(d, "-q", "-m", "base")
        base = sh(d, "git", "rev-parse", "--short=7", "HEAD").strip()
        with open(os.path.join(d, "sim", "requirements.md"), "w") as f:
            f.write(REGISTER.replace("BASE_SHA", base))
        sh(d, "git", "add", ".")
        commit(d, "-q", "-m", "fixture")
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
        self.assertIn("entries\t20", out)
        self.assertIn("tracked\t5", out)
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
        commit(self.repo, "-q", "-am", "dup")
        self.pin = sh(self.repo, "git", "rev-parse", "HEAD").strip()
        rc, _, err = self.run_locus()
        self.assertNotEqual(rc, 0)
        self.assertIn("second definition", err)

    def test_no_prose_in_output(self):
        rc, out, _ = self.run_locus()
        self.assertEqual(rc, 0)
        for word in ("roundtable", "zombie", "sitting", "idle-seats", "replay-pilot"):
            self.assertNotIn(word, out)


def guard_violations(src):
    """The guard's rules over this file's source: the one git commit is
    commit()'s, signing is set in SIGNING only, and nothing here starts a
    program outside sh(), setUpModule() and run_locus(). Returns what breaks
    them. The patterns are bracketed so they cannot match their own lines."""
    import re
    out = []
    sites = re.findall(r'"git", "c[o]mmit"', src)
    if len(sites) != 1:
        out.append("commit sites: %d" % len(sites))
    for want in (r'"commit\.gpg[s]ign": "[t]rue"', r'"user\.signingkey": K[E]Y'):
        if not re.search(want, src):
            out.append("the signing config lacks " + want)
    # Signing is set on one line, in SIGNING; any other mention, in any case
    # and with any value, is a way to change it.
    for n, line in enumerate(src.splitlines(), 1):
        if re.search(r"(?i)gpg[s]ign", line) and not re.fullmatch(r'    "commit\.gpg[s]ign": "[t]rue",', line):
            out.append("a signing setting at line %d" % n)
    for banned in (r"--no-gpg-[s]ign", r"commit-[t]ree", r"fast-[i]mport", r"mk[t]ag", r'"-t", "[c]ommit"',
                   r"\bos\.(syst[e]m|pop[e]n|exe[c]|spaw[n])", r"\bP[o]pen\b", r"check_[o]utput", r"check_[c]all",
                   r"from subproc[e]ss", r"import subproc[e]ss as"):
        if re.search(banned, src):
            out.append("this file holds " + banned)
    # A program starts only inside these functions, so a git named through a
    # variable elsewhere is caught.
    spans = []
    for name in ("sh", "setUpModule", "run_locus"):
        m = re.search(r"\n( *)def " + name + r"\(", src)
        if m:
            end = re.search(r"\n" + m.group(1) + r"(def |class |\S)", src[m.end():])
            spans.append((m.start(), m.end() + (end.start() if end else len(src))))
    for m in re.finditer(r"subproc[e]ss\.\w+\(", src):
        if not any(a <= m.start() < b for a, b in spans):
            out.append("a program started outside sh, setUpModule and run_locus")
    return out


class FixtureGuardTest(unittest.TestCase):
    def source(self):
        with open(os.path.abspath(__file__)) as f:
            return f.read()

    def test_one_signed_commit_site(self):
        self.assertEqual(guard_violations(self.source()), [])

    # director#439, from the kos#158 review: the same two holes. Each mutant
    # is crafted text added at run time; none is ever run, and no test makes
    # an unsigned commit.
    def test_guard_catches_a_signing_override_in_any_case(self):
        for value in ("no", "0", "off", "FALSE"):
            mutant = self.source() + '\nsh(d, "git", "-c", "commit.gpg' + "Sign=" + value + '", "merge", "x")\n'
            self.assertNotEqual(guard_violations(mutant), [], value)

    def test_guard_catches_git_through_a_variable(self):
        mutant = self.source() + '\nprog = "g' + 'it"\nsubproc' + 'ess.r' + 'un([prog, "com' + 'mit", "-m", "x"])\n'
        self.assertNotEqual(guard_violations(mutant), [])

    def test_sh_refuses_caller_config(self):
        with tempfile.TemporaryDirectory() as d:
            sh(d, "git", "init", "-q")
            with self.assertRaisesRegex(AssertionError, "refuses caller"):
                sh(d, "git", "-c", "commit.gpg" + "Sign=no", "com" + "mit", "-q", "--allow-empty", "-m", "x")
            self.assertEqual(sh(d, "git", "rev-list", "--all"), "")

    def test_sh_refuses_a_signing_switch(self):
        with tempfile.TemporaryDirectory() as d:
            sh(d, "git", "init", "-q")
            with self.assertRaisesRegex(AssertionError, "refuses caller"):
                sh(d, "git", "com" + "mit", "--no-gpg" + "-sign", "-q", "--allow-empty", "-m", "x")
            self.assertEqual(sh(d, "git", "rev-list", "--all"), "")

    def test_signature_check_reads_the_commit_object(self):
        head = "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\ncommitter t <t@example.invalid> 0 +0000\n"
        self.assertFalse(has_signature(head + "\nx\n"))
        self.assertTrue(has_signature(head + "gpgsig -----BEGIN SSH SIGNATURE-----\n \n -----END-----\n\nx\n"))
        self.assertFalse(has_signature(head + "\nmessage\ngpgsig in the body is not a header\n"))

if __name__ == "__main__":
    unittest.main()
