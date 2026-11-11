#!/usr/bin/env python3
"""Structural checks over director's recipes library (the library root holds
company.toml, recipes/<name>.toml, switch-list.toml and parity-table.toml).

Usage: scripts/recipes_lint.py <library-dir>
Prints one `FAIL <check>: <message>` line per defect and exits 1 when there is
any. Needs Python 3.11 or newer (tomllib). Reads only the directory it is given.

These are structural checks, so they may gate a merge. The check ids are the ones
the verifier and the PR name:
  T-A-4   the non-holder block unsets exactly DIRECTOR_GLOBAL_DOMAIN and
          DIRECTOR_GLOBAL_ROLE
  R2-B    one source for the domain: no env names it, no holder block, no {var:
  R2-C    the switch list marks the director seat and research-supervisor
          "not until marvel admits"
  C7      no bus credential or bus-state name in env, unset or mcp
  A4-2a   every key is on the company-recipe schema's list
  A4-2b   every command is a tool: name with no path
"""
import re
import sys
import tomllib
from pathlib import Path

DOMAIN = "DIRECTOR_GLOBAL_DOMAIN"
GROLE = "DIRECTOR_GLOBAL_ROLE"
CREDENTIAL_NAMES = ("DIRECTOR_NATS_USER", "DIRECTOR_NATS_PASS", "DIRECTOR_NATS_CREDS", "MARVEL_BUS_STATE")
HELD = "not until marvel admits"
SWITCHABLE = "switchable"
HELD_ROLES = ("director", "research-supervisor")

COMPANY_KEYS = {"name", "version", "kind", "schema", "requires_marvel", "source", "recipe_schema", "requires"}
SOURCE_KEYS = {"url", "commit"}
REQUIRES_KEYS = {"definition_schema"}
RECIPE_KEYS = {"schema", "adapter", "command", "prompt", "args", "env", "mcp", "preflight", "params", "when"}
MCP_KEYS = {"command", "args", "strict"}
PREFLIGHT_KEYS = {"command", "timeout"}
PARAM_KEYS = {"type", "required", "pattern"}
WHEN_KEYS = {"env", "args", "unset"}
PLACEHOLDER = re.compile(r"\{([^{}]*)\}")
KNOWN_PLACEHOLDERS = {"role", "team", "workspace", "session"}


def load(path):
    with open(path, "rb") as f:
        return tomllib.load(f)


def walk_strings(node):
    """Every key and string value in a parsed table, depth first."""
    if isinstance(node, dict):
        for k, v in node.items():
            yield k
            yield from walk_strings(v)
    elif isinstance(node, list):
        for v in node:
            yield from walk_strings(v)
    elif isinstance(node, str):
        yield node


def env_tables(recipe):
    """(label, table) for every env table in a recipe."""
    out = []
    if isinstance(recipe.get("env"), dict):
        out.append(("env", recipe["env"]))
    when = recipe.get("when", {})
    for name, block in when.items():
        if name == "global":
            for branch, b in block.items():
                if isinstance(b, dict) and isinstance(b.get("env"), dict):
                    out.append((f"when.global.{branch}.env", b["env"]))
        elif isinstance(block, dict) and isinstance(block.get("env"), dict):
            out.append((f"when.{name}.env", block["env"]))
    for name, m in recipe.get("mcp", {}).items():
        if isinstance(m, dict) and isinstance(m.get("env"), dict):
            out.append((f"mcp.{name}.env", m["env"]))
    return out


def unknown(label, table, allowed, fails, check="A4-2a"):
    for k in table:
        if k not in allowed:
            fails.append((check, f"{label}: unknown key \"{k}\""))


def lint_company(lib, fails):
    path = lib / "company.toml"
    if not path.is_file():
        fails.append(("A4-2a", "company.toml is missing"))
        return None
    c = load(path)
    unknown("company.toml", c, COMPANY_KEYS, fails)
    if isinstance(c.get("source"), dict):
        unknown("company.toml source", c["source"], SOURCE_KEYS, fails)
    if isinstance(c.get("requires"), dict):
        unknown("company.toml requires", c["requires"], REQUIRES_KEYS, fails)
    if "definitions" in c:
        fails.append(("A4-2a", "company.toml: a recipes library carries no [[definitions]]"))
    if c.get("kind") != "recipes":
        fails.append(("A4-2a", f"company.toml: kind must be \"recipes\", not {c.get('kind')!r}"))
    return c


def lint_recipe(path, company, fails):
    r = load(path)
    label = f"recipes/{path.name}"
    unknown(label, r, RECIPE_KEYS, fails)
    if company is not None and r.get("schema") != company.get("recipe_schema"):
        fails.append(("A4-2a", f"{label}: schema = {r.get('schema')!r}, but the library's recipe_schema is {company.get('recipe_schema')!r}"))
    for name, m in r.get("mcp", {}).items():
        if isinstance(m, dict):
            unknown(f"{label} mcp.{name}", m, MCP_KEYS, fails)
    if isinstance(r.get("preflight"), dict):
        unknown(f"{label} preflight", r["preflight"], PREFLIGHT_KEYS, fails)
    for name, p in r.get("params", {}).items():
        if isinstance(p, dict):
            unknown(f"{label} params.{name}", p, PARAM_KEYS, fails)
    when = r.get("when", {})
    for name, block in when.items():
        if name == "global":
            unknown(f"{label} when.global", block, {"holder", "non_holder"}, fails)
            for branch, b in block.items():
                if isinstance(b, dict):
                    unknown(f"{label} when.global.{branch}", b, WHEN_KEYS, fails)
        else:
            if isinstance(block, dict):
                unknown(f"{label} when.{name}", block, WHEN_KEYS, fails)
            if name not in r.get("params", {}):
                fails.append(("A4-2a", f"{label}: when.{name} names a parameter that is not declared"))

    # A4-2b: every command is a tool: name, with no path
    cmds = []
    if isinstance(r.get("command"), str):
        cmds.append(("command", r["command"]))
    for name, m in r.get("mcp", {}).items():
        if isinstance(m, dict) and "command" in m:
            cmds.append((f"mcp.{name}.command", m["command"]))
    pf = r.get("preflight")
    if isinstance(pf, dict) and "command" in pf:
        c0 = pf["command"][0] if isinstance(pf["command"], list) and pf["command"] else pf["command"]
        cmds.append(("preflight.command", c0))
    for where, v in cmds:
        if not isinstance(v, str) or not v.startswith("tool:") or "/" in v:
            fails.append(("A4-2b", f"{label}: {where} = {v!r} must be a tool: name with no path"))

    # placeholders: only the closed set
    for s in walk_strings({k: v for k, v in r.items() if k != "params"}):
        for m in PLACEHOLDER.findall(s):
            if m in KNOWN_PLACEHOLDERS:
                continue
            if m.startswith("param:") and m[6:] in r.get("params", {}):
                continue
            if m.startswith("var:"):
                continue  # refused by R2-B below, with its own message
            fails.append(("A4-2a", f"{label}: unknown placeholder \"{{{m}}}\""))

    # R2-B: one source for the domain
    for where, table in env_tables(r):
        if DOMAIN in table:
            fails.append(("R2-B", f"{label}: {where} names {DOMAIN}"))
    if "holder" in when.get("global", {}):
        fails.append(("R2-B", f"{label}: there is a [when.global.holder] block"))
    for s in walk_strings(r):
        if "{var:" in s:
            fails.append(("R2-B", f"{label}: a value holds a {{var: placeholder ({s!r})"))

    # C7: no bus credential or bus-state name in env, unset or mcp
    scan = {"env": r.get("env", {}), "mcp": r.get("mcp", {}), "when": when}
    for s in walk_strings(scan):
        for name in CREDENTIAL_NAMES:
            if name in s:
                fails.append(("C7", f"{label}: names {name}"))
    for s in walk_strings(r.get("preflight", {})):
        for name in CREDENTIAL_NAMES:
            if name in s:
                fails.append(("C7", f"{label}: preflight names {name}"))

    # T-A-4: the non-holder block unsets exactly the two global levers
    nh = when.get("global", {}).get("non_holder", {})
    got = nh.get("unset", []) if isinstance(nh, dict) else []
    want = [DOMAIN, GROLE]
    for name in want:
        if name not in got:
            fails.append(("T-A-4", f"{label}: when.global.non_holder.unset lacks {name}"))
    for name in got:
        if name not in want:
            fails.append(("T-A-4", f"{label}: when.global.non_holder.unset names {name}, which is not one of {DOMAIN}, {GROLE}"))
    if len(got) != len(set(got)):
        fails.append(("T-A-4", f"{label}: when.global.non_holder.unset repeats a name"))


def lint_switch_list(lib, fails):
    path = lib / "switch-list.toml"
    if not path.is_file():
        fails.append(("R2-C", "switch-list.toml is missing"))
        return
    s = load(path)
    roles = s.get("roles", {})
    for role in HELD_ROLES:
        if roles.get(role) != HELD:
            fails.append(("R2-C", f"switch-list.toml: {role} is {roles.get(role)!r}, not {HELD!r}"))
    if roles.get("builder") != SWITCHABLE:
        fails.append(("R2-C", f"switch-list.toml: builder is {roles.get('builder')!r}, not {SWITCHABLE!r}"))
    for role, status in roles.items():
        if status not in (HELD, SWITCHABLE):
            fails.append(("R2-C", f"switch-list.toml: {role} has the status {status!r}"))


def main():
    if len(sys.argv) != 2:
        print("usage: recipes_lint.py <library-dir>", file=sys.stderr)
        return 2
    lib = Path(sys.argv[1])
    if not lib.is_dir():
        print(f"recipes_lint.py: {lib} is not a directory", file=sys.stderr)
        return 2
    fails = []
    company = lint_company(lib, fails)
    recipes = sorted((lib / "recipes").glob("*.toml")) if (lib / "recipes").is_dir() else []
    if not recipes:
        fails.append(("A4-2a", "recipes/ holds no recipe file"))
    for p in recipes:
        lint_recipe(p, company, fails)
    lint_switch_list(lib, fails)
    for check, msg in fails:
        print(f"FAIL {check}: {msg}")
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
