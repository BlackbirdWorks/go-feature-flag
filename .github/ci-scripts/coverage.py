#!/usr/bin/env python3
"""Merge Go coverage profiles, apply the coverage exclusions, report line coverage.

Used by CI (job `coverage`) and `make coverage`, so both print the same number.
Standard library only; no third-party converter to pin.

  coverage.py [options] PROFILE [PROFILE ...]

  --root DIR            repository root (default: two levels above this script)
  --exclude FILE        exclusion patterns (default: coverage-exclude.txt next to this script)
  --merged FILE         write the merged, filtered Go profile
  --cobertura FILE      write the Cobertura XML report (what GitHub's native coverage ingests)
  --summary FILE        write a markdown report (modules, top-level dirs, packages)
  --top N               limit the package table to the N packages with most uncovered lines
  --min PERCENT         exit 1 when total LINE coverage is below PERCENT
  --check-codecov FILE  fail unless the `ignore:` list of this codecov.yml equals the exclusion file

Merging: a block (file, range, statement count) present in several profiles is
covered when ANY profile covers it (OR), and counted once. Profile paths are Go
import paths; the three Go modules of the repository (root, modules/core, rules)
are mapped to repository-relative files through their go.mod `module` lines.

Line coverage follows the semantics of the usual Go -> Cobertura converter
(gocover-cobertura): every line spanned by a coverage block is an instrumented
line, and it is covered when any block touching it ran. Blank and comment-only
lines inside a block are not counted. GitHub only reads LINE coverage from the
Cobertura file; statement coverage (what `go tool cover` prints) is reported too.
"""
import argparse
import collections
import fnmatch
import os
import re
import sys
from xml.sax.saxutils import quoteattr

GENERATED_RE = re.compile(r"^// Code generated .* DO NOT EDIT\.?$")
BLOCK_RE = re.compile(r"^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$")


def load_patterns(path):
    pats = []
    with open(path, encoding="utf-8") as fh:
        for raw in fh:
            line = raw.split("#", 1)[0].strip()
            if line:
                pats.append(line)
    return pats


def glob_to_re(pat):
    """gitignore-like glob: `**` crosses directories, `*` and `?` do not; a
    trailing `/` or a bare directory name excludes everything below it."""
    out, i = "", 0
    while i < len(pat):
        c = pat[i]
        if pat.startswith("**/", i):
            out += "(?:.*/)?"
            i += 3
        elif pat.startswith("**", i):
            out += ".*"
            i += 2
        elif c == "*":
            out += "[^/]*"
            i += 1
        elif c == "?":
            out += "[^/]"
            i += 1
        else:
            out += re.escape(c)
            i += 1
    return out


def compile_patterns(pats):
    res = []
    for p in pats:
        base = p.rstrip("/")
        res.append(re.compile("^" + glob_to_re(base) + "(?:/.*)?$"))
    return res


def module_map(root):
    mods = {}
    for d in (".", "modules/core", "rules"):
        gomod = os.path.join(root, d, "go.mod")
        with open(gomod, encoding="utf-8") as fh:
            m = re.search(r"^module\s+(\S+)", fh.read(), re.M)
        mods[m.group(1)] = d
    return mods


def to_repo_path(name, mods):
    best = None
    for mp in mods:
        if name == mp or name.startswith(mp + "/"):
            if best is None or len(mp) > len(best):
                best = mp
    if best is None:
        return None
    rel = name[len(best):].lstrip("/")
    d = mods[best]
    return rel if d == "." else d + "/" + rel


def module_of(path):
    for d in ("modules/core", "rules"):
        if path.startswith(d + "/"):
            return d
    return "root"


def read_profiles(paths, mods):
    blocks = {}  # (file, sl, sc, el, ec, nstmt) -> count
    for p in paths:
        with open(p, encoding="utf-8") as fh:
            for ln, raw in enumerate(fh, 1):
                raw = raw.rstrip("\n")
                if not raw or raw.startswith("mode:"):
                    continue
                m = BLOCK_RE.match(raw)
                if not m:
                    sys.exit("%s:%d: bad profile line: %r" % (p, ln, raw))
                rel = to_repo_path(m.group(1), mods)
                if rel is None:
                    sys.exit("%s:%d: file outside the known modules: %s" % (p, ln, m.group(1)))
                key = (rel,) + tuple(int(m.group(i)) for i in (2, 3, 4, 5, 6))
                blocks[key] = blocks.get(key, 0) + int(m.group(7))
    return blocks


def code_lines(path):
    """Set of line numbers holding code (not blank, not comment-only).

    A small lexer, so `/*` inside a string or raw string is not a comment and
    raw-string lines count as code."""
    ok = set()
    state = None  # None | "block" (/* */) | "raw" (`...`)
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            for n, raw in enumerate(fh, 1):
                code = state == "raw"
                i, ln = 0, len(raw)
                while i < ln:
                    c = raw[i]
                    if state == "block":
                        j = raw.find("*/", i)
                        if j < 0:
                            break
                        state, i = None, j + 2
                    elif state == "raw":
                        j = raw.find("`", i)
                        if j < 0:
                            break
                        state, i = None, j + 1
                    elif raw.startswith("//", i):
                        break
                    elif raw.startswith("/*", i):
                        state, i = "block", i + 2
                    elif c == "`":
                        code, state, i = True, "raw", i + 1
                    elif c in "\"'":
                        code = True
                        i += 1
                        while i < ln and raw[i] != c:
                            i += 2 if raw[i] == "\\" else 1
                        i += 1
                    else:
                        if not c.isspace():
                            code = True
                        i += 1
                if code:
                    ok.add(n)
    except FileNotFoundError:
        return None
    return ok


def self_test():
    import tempfile
    src = "package p\n/* c */\n// c\nvar a = `\n/* not a comment\n`\n/*\nblock\n*/\nvar b = \"/*\" // x\nvar c = '/'\n"
    with tempfile.TemporaryDirectory() as d:
        f = os.path.join(d, "x.go")
        with open(f, "w", encoding="utf-8") as fh:
            fh.write(src)
        got = sorted(code_lines(f))
    assert got == [1, 4, 5, 6, 10, 11], got


def is_generated(path):
    try:
        with open(path, encoding="utf-8", errors="replace") as fh:
            for _ in range(40):
                line = fh.readline()
                if not line:
                    break
                if GENERATED_RE.match(line.strip()):
                    return True
    except FileNotFoundError:
        pass
    return False


def pct(a, b):
    return 100.0 * a / b if b else 100.0


def check_codecov(codecov, pats):
    ign, inside = [], False
    with open(codecov, encoding="utf-8") as fh:
        for raw in fh:
            if re.match(r"^ignore:\s*$", raw):
                inside = True
                continue
            if inside:
                m = re.match(r"^\s+-\s+\"?([^\"#]+?)\"?\s*(?:#.*)?$", raw)
                if m:
                    ign.append(m.group(1).strip())
                elif raw.strip() and not raw.startswith(" "):
                    break
    a, b = set(ign), set(pats)
    if a != b:
        sys.exit("codecov.yml ignore list differs from the exclusion file:\n  only in codecov.yml: %s\n  only in exclusion file: %s"
                 % (sorted(a - b), sorted(b - a)))


def main(argv=None):
    here = os.path.dirname(os.path.abspath(__file__))
    ap = argparse.ArgumentParser()
    ap.add_argument("profiles", nargs="+")
    ap.add_argument("--root", default=os.path.abspath(os.path.join(here, "..", "..")))
    ap.add_argument("--exclude", default=os.path.join(here, "coverage-exclude.txt"))
    ap.add_argument("--merged")
    ap.add_argument("--cobertura")
    ap.add_argument("--summary")
    ap.add_argument("--top", type=int, default=0)
    ap.add_argument("--min", type=float)
    ap.add_argument("--check-codecov")
    ap.add_argument("--no-exclude", action="store_true", help="measure without the exclusion list")
    a = ap.parse_args(argv)
    self_test()

    pats = load_patterns(a.exclude)
    if a.check_codecov:
        check_codecov(a.check_codecov, pats)
    excl = [] if a.no_exclude else compile_patterns(pats)
    mods = module_map(a.root)
    blocks = read_profiles(a.profiles, mods)

    files = collections.defaultdict(list)
    for (f, sl, sc, el, ec, ns), cnt in blocks.items():
        files[f].append((sl, sc, el, ec, ns, cnt))

    excluded, missing, generated_unlisted = {}, [], []
    stats = {}  # file -> dict
    for f in sorted(files):
        full = os.path.join(a.root, f)
        if any(r.match(f) for r in excl):
            excluded[f] = files[f]
            continue
        if not a.no_exclude and is_generated(full):
            generated_unlisted.append(f)
        code = code_lines(full)
        if code is None:
            missing.append(f)
            continue
        hits = {}
        stm_t = stm_c = 0
        for sl, sc, el, ec, ns, cnt in files[f]:
            stm_t += ns
            if cnt > 0:
                stm_c += ns
            for l in range(sl, el + 1):
                if l in code:
                    hits[l] = hits.get(l, 0) + cnt
        stats[f] = {"hits": hits, "stm_t": stm_t, "stm_c": stm_c}

    if missing:
        sys.exit("profile references files missing from the checkout: %s" % missing[:5])

    if a.merged:
        with open(a.merged, "w", encoding="utf-8") as fh:
            fh.write("mode: atomic\n")
            for (f, sl, sc, el, ec, ns), cnt in sorted(blocks.items()):
                if f in stats:
                    # "./" makes `go tool cover -func` resolve the file from the
                    # repository root; bare names are read as import paths.
                    fh.write("./%s:%d.%d,%d.%d %d %d\n" % (f, sl, sc, el, ec, ns, cnt))

    # Aggregate
    def agg(keyf):
        d = collections.defaultdict(lambda: [0, 0, 0, 0])
        for f, s in stats.items():
            k = keyf(f)
            d[k][0] += len(s["hits"])
            d[k][1] += sum(1 for h in s["hits"].values() if h > 0)
            d[k][2] += s["stm_t"]
            d[k][3] += s["stm_c"]
        return d

    total = [sum(v[i] for v in agg(lambda f: 0).values()) for i in range(4)]
    lines_t, lines_c, stm_t, stm_c = total
    line_pct, stm_pct = pct(lines_c, lines_t), pct(stm_c, stm_t)
    # Lines to cover to reach 90.0 % (documented target)
    need90 = max(0, -(-(90 * lines_t) // 100) - lines_c) if lines_t else 0

    def table(title, d, top=0, first="Name"):
        rows = sorted(d.items(), key=lambda kv: (-(kv[1][0] - kv[1][1]), kv[0]))
        if top:
            rows = rows[:top]
        out = ["### %s" % title, "", "| %s | lines | covered | line %% | uncovered | stmt %% |" % first, "|---|---:|---:|---:|---:|---:|"]
        for k, v in rows:
            out.append("| `%s` | %d | %d | %.1f | %d | %.1f |" % (k, v[0], v[1], pct(v[1], v[0]), v[0] - v[1], pct(v[3], v[2])))
        return "\n".join(out) + "\n"

    pkg = lambda f: os.path.dirname(f) or "."
    md = ["## Coverage", "",
          "**Total line coverage: %.2f%%** (%d of %d lines) | statement coverage: %.2f%% (%d of %d)%s"
          % (line_pct, lines_c, lines_t, stm_pct, stm_c, stm_t,
             "" if a.no_exclude else " | %d files excluded (see `.github/ci-scripts/coverage-exclude.txt`)" % len(excluded)),
          "", "Covered lines still needed for 90.0%%: **%d**" % need90, "",
          table("Per module", agg(lambda f: module_of(f)), first="Module"),
          table("Per package%s" % (" (top %d by uncovered lines)" % a.top if a.top else ""), agg(pkg), a.top, "Package")]
    text = "\n".join(md)
    if generated_unlisted:
        text += "\n**Generated files not in the exclusion list:** %s\n" % ", ".join("`%s`" % g for g in generated_unlisted)
    if a.summary:
        with open(a.summary, "w", encoding="utf-8") as fh:
            fh.write(text)
    print(text)

    if a.cobertura:
        write_cobertura(a.cobertura, stats, lines_c, lines_t)

    rc = 0
    if generated_unlisted:
        print("::error::generated files missing from coverage-exclude.txt: %s" % generated_unlisted, file=sys.stderr)
        rc = 1
    if a.min is not None and line_pct < a.min:
        print("::error::total line coverage %.2f%% is below the minimum %.2f%% (COVERAGE_MIN)" % (line_pct, a.min), file=sys.stderr)
        rc = 1
    print("TOTAL_LINE_COVERAGE=%.2f" % line_pct)
    return rc


def write_cobertura(path, stats, lines_c, lines_t):
    bypkg = collections.defaultdict(list)
    for f in sorted(stats):
        bypkg[os.path.dirname(f) or "."].append(f)
    with open(path, "w", encoding="utf-8") as o:
        o.write('<?xml version="1.0" encoding="UTF-8"?>\n')
        o.write('<!DOCTYPE coverage SYSTEM "http://cobertura.sourceforge.net/xml/coverage-04.dtd">\n')
        o.write('<coverage line-rate="%.6f" branch-rate="0" lines-covered="%d" lines-valid="%d" '
                'branches-covered="0" branches-valid="0" complexity="0" version="1" timestamp="0">\n'
                % (lines_c / lines_t if lines_t else 1, lines_c, lines_t))
        o.write("  <sources>\n    <source>.</source>\n  </sources>\n  <packages>\n")
        for p in sorted(bypkg):
            pl = sum(len(stats[f]["hits"]) for f in bypkg[p])
            pc = sum(1 for f in bypkg[p] for h in stats[f]["hits"].values() if h > 0)
            o.write('    <package name=%s line-rate="%.6f" branch-rate="0" complexity="0">\n      <classes>\n'
                    % (quoteattr(p), pc / pl if pl else 1))
            for f in bypkg[p]:
                h = stats[f]["hits"]
                c = sum(1 for v in h.values() if v > 0)
                o.write('        <class name=%s filename=%s line-rate="%.6f" branch-rate="0" complexity="0">\n'
                        % (quoteattr(f), quoteattr(f), c / len(h) if h else 1))
                o.write("          <methods/>\n          <lines>\n")
                for n in sorted(h):
                    o.write('            <line number="%d" hits="%d" branch="false"/>\n' % (n, min(h[n], 2**31 - 1)))
                o.write("          </lines>\n        </class>\n")
            o.write("      </classes>\n    </package>\n")
        o.write("  </packages>\n</coverage>\n")


if __name__ == "__main__":
    sys.exit(main())
