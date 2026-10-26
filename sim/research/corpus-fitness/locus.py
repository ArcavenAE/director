#!/usr/bin/env python3
"""Evidence locus projection for director's requirements register.

Reads sim/requirements.md at a pinned commit, applies id-grammar.json, and
prints one row per R entry: id, line, first class, the evidence tokens found
on its Earned-by line by class, and its evidence locus. It emits ids and
counts only, never the register's prose.

A session-relative entry is anchored to the commit that introduced its
definition line (git log -S on the heading token, oldest first), as the
design asks, so "this session" gets a date.

With --ledger it prints the evidence ledger instead: one EV row per distinct
notes token cited by the selected entries, in first-citation order (register
order, then position on the Earned-by line), each marked withheld. A row holds
only its EV id, the date its first citing entry entered the register, the R
ids that cite it, and the word withheld; the native note id is never printed.
The crosswalk from EV id to native id is kept outside this repository, so this
mode allocates a first ledger only and refuses when one exists.

Usage: locus.py <repo> <pin> <grammar.json> [--ids R-05,R-14,...] [--summary | --ledger <path>]
"""
import os
import json
import re
import subprocess
import sys

CLASSES = ("OBSERVED", "JUDGMENT", "RULED", "FORWARD")


def git(repo, *args):
    out = subprocess.run(["git", "-C", repo, *args], capture_output=True, check=True)
    return out.stdout.decode("utf-8", errors="replace")


def entries(text):
    lines = text.split("\n")
    starts = []
    for i, line in enumerate(lines):
        m = re.match(r"^\*\*R-(\d+)\b", line)
        if m:
            starts.append((i, int(m.group(1))))
    seen = set()
    for k, (i, n) in enumerate(starts):
        if n in seen:
            raise SystemExit(f"R-{n} has a second definition line at {i + 1}")
        seen.add(n)
        end = starts[k + 1][0] if k + 1 < len(starts) else len(lines)
        yield n, i + 1, "\n".join(lines[i:end])


def first_class(block):
    head = re.match(r"^\*\*R-\d+ \(([A-Z]+)", block)
    if head and head.group(1) in CLASSES:
        return head.group(1)
    src = re.search(r"\*Source:\s*([A-Z]+)", block)
    if src and src.group(1) in CLASSES:
        return src.group(1)
    return "unknown"


def earned_by(block, grammar):
    m = re.search(grammar["earned_by"] + r"(.*?)(?:\*\s*$|\n\s*\n|\*Source)", block, re.S | re.M)
    return " ".join(m.group(1).split()) if m else None


FILE_TOKEN = re.compile(r"[A-Za-z0-9_.-]+\.[a-z]{2,4}(?::[0-9]+(?:-[0-9]+)?)?$")
LINE_SUFFIX = re.compile(r":[0-9]+(?:-[0-9]+)?$")
REGISTER_DIR = "sim/"


def is_file(tok):
    t = tok.strip("`")
    return "/" in t or bool(FILE_TOKEN.search(t))


def resolves(tok, tree):
    """A path or file-name token is evidence only if it names a tracked file as
    written: a :line suffix is dropped, and the rest must be an exact path at the
    pin, from the repo root or from the register's own directory (a directory
    counts if it holds a tracked file). A bare file name that
    exists only deeper in the tree does not resolve, since it does not say which."""
    t = LINE_SUFFIX.sub("", tok.strip("`"))
    for base in ("", REGISTER_DIR):  # from the repo root, or relative to the register
        c = base + t
        if c in tree or (c.endswith("/") and any(p.startswith(c) for p in tree)):
            return True
    return False


SHA_TOKEN = re.compile(r"^[0-9a-f]{7,40}$")


def sha_resolves(repo, pin, sha):
    """A commit sha is evidence only if it names a commit in the pin's history."""
    ok = subprocess.run(["git", "-C", repo, "rev-parse", "-q", "--verify", f"{sha}^{{commit}}"], capture_output=True)
    if ok.returncode != 0:
        return False
    anc = subprocess.run(["git", "-C", repo, "merge-base", "--is-ancestor", sha, pin], capture_output=True)
    return anc.returncode == 0


def classify(eb, grammar, tree, repo=None, pin=None):
    found = {}
    for cls, pats in grammar["token_classes"].items():
        spans = {(m.start(), m.end()) for p in pats for m in re.finditer(p, eb)}
        if cls == "tracked":
            # A path or file-name token is evidence only if it resolves at the pin.
            spans = {s for s in spans if not (is_file(eb[s[0]:s[1]]) and not resolves(eb[s[0]:s[1]], tree))}
            spans = {s for s in spans if not (SHA_TOKEN.match(eb[s[0]:s[1]]) and not sha_resolves(repo, pin, eb[s[0]:s[1]]))}
        toks, last_end = [], -1
        for start, end in sorted(spans, key=lambda s: (s[0], -s[1])):
            if end <= last_end:
                continue  # inside a longer match of the same class
            toks.append(eb[start:end])
            last_end = max(last_end, end)
        found[cls] = sorted(set(toks))
    for cls, locus in grammar["locus_order"]:
        if found[cls]:
            return locus, found
    return grammar["no_token"], found


def introduced_date(repo, pin, n):
    anchor = introduced(repo, pin, n)
    return anchor.split("@")[1] if "@" in anchor else "-"


BARE_DATE = re.compile(r"^(?:O|FR|observation|friction)[- ][0-9]{4}-[0-9]{2}-[0-9]{2}$")


def ledger(repo, pin, cited, path):
    """cited: (R number, [notes tokens in line order]) in register order."""
    if os.path.exists(path) and any(l.startswith("| EV-") for l in open(path)):
        raise SystemExit(f"{path} already holds EV rows; extending it needs the crosswalk, which is not here")
    order, cites = [], {}
    for n, toks in cited:
        for tok in toks:
            if BARE_DATE.match(tok):
                tok = (tok, n)  # a date alone names no one observation, so it is never shared
            if tok not in cites:
                order.append(tok)
                cites[tok] = []
            if n not in cites[tok]:
                cites[tok].append(n)
    print("| EV id | date | R ids | status |")
    print("|---|---|---|---|")
    for k, tok in enumerate(order, 1):
        ids = ", ".join(f"R-{n:02d}" for n in cites[tok])
        print(f"| EV-{k:04d} | {introduced_date(repo, pin, cites[tok][0])} | {ids} | withheld |")


def notes_in_line_order(eb, grammar):
    spans = {(m.start(), m.end()) for p in grammar["token_classes"]["notes"] for m in re.finditer(p, eb)}
    toks, last_end = [], -1
    for start, end in sorted(spans, key=lambda s: (s[0], -s[1])):
        if end <= last_end:
            continue
        toks.append(eb[start:end])
        last_end = max(last_end, end)
    return toks


def introduced(repo, pin, n):
    """Oldest commit at or before pin whose diff adds or removes the token."""
    out = git(repo, "log", "--reverse", "--format=%h %cs", "-S", f"**R-{n:02d}", pin, "--", "sim/requirements.md")
    first = out.strip().split("\n")[0] if out.strip() else ""
    return first.replace(" ", "@") if first else "-"


def main():
    if len(sys.argv) < 4:
        raise SystemExit(__doc__)
    repo, pin, gpath = sys.argv[1:4]
    want = None
    rest = sys.argv[4:]
    if "--ids" in rest:
        want = {int(x.split("-")[1]) for x in rest[rest.index("--ids") + 1].split(",")}
    summary = "--summary" in rest
    ledger_path = rest[rest.index("--ledger") + 1] if "--ledger" in rest else None
    grammar = json.load(open(gpath))
    text = git(repo, "show", f"{pin}:sim/requirements.md")
    tree = set(git(repo, "ls-tree", "-r", "--name-only", pin).split("\n")) - {""}
    rows, cited = [], []
    for n, line, block in entries(text):
        if want is not None and n not in want:
            continue
        eb = earned_by(block, grammar)
        if eb is None:
            locus, found = grammar["no_earned_by"], {c: [] for c in grammar["token_classes"]}
        else:
            locus, found = classify(eb, grammar, tree, repo, pin)
        if eb is not None and found["notes"]:
            cited.append((n, notes_in_line_order(eb, grammar)))
        counts = [str(len(found[c])) for c in ("tracked", "notes", "ruling", "session")]
        anchor = introduced(repo, pin, n) if locus == "session-relative" else "-"
        rows.append([f"R-{n:02d}", str(line), first_class(block), *counts, locus, anchor])
    if ledger_path is not None:
        ledger(repo, pin, cited, ledger_path)
        return
    if summary:
        tally = {}
        for r in rows:
            tally[r[7]] = tally.get(r[7], 0) + 1
        print(f"pin\t{pin}\nentries\t{len(rows)}")
        for locus in ("tracked", "notes-only", "ruling-no-record", "session-relative", "unresolved", "none"):
            print(f"{locus}\t{tally.get(locus, 0)}")
        return
    print("\t".join(["id", "line", "class", "tracked", "notes", "ruling", "session", "locus", "introduced"]))
    for r in rows:
        print("\t".join(r))


if __name__ == "__main__":
    main()
