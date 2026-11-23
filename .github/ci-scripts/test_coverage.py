"""Regression tests for coverage.py (stdlib unittest; run: make test-ci-scripts)."""
import contextlib
import io
import os
import re
import sys
import tempfile
import unittest
import xml.etree.ElementTree as ET

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
sys.dont_write_bytecode = True
import coverage  # noqa: E402

ROOT_MOD = "example.com/app"
CORE_MOD = "example.com/app/modules/core"
RULES_MOD = "example.com/app/rules"

GO_A = "package a\n\nfunc F() {\n\tx := 1\n\t// comment\n\t_ = x\n}\n"
GO_RAW = "package b\n\nvar s = `\n/* not a comment\n`\n\n/*\nreal comment\n*/\nvar t = 1\n"


class Fixture(unittest.TestCase):
    def setUp(self):
        self._td = tempfile.TemporaryDirectory()
        self.addCleanup(self._td.cleanup)
        self.root = self._td.name
        self.write("go.mod", "module %s\n\ngo 1.22\n" % ROOT_MOD)
        self.write("modules/core/go.mod", "module %s\n" % CORE_MOD)
        self.write("rules/go.mod", "module %s\n" % RULES_MOD)
        self.write("exclude.txt", "# c\ntestutils/\nmodules/core/testutils/\n**/gen_*.go\n")

    def write(self, rel, text):
        path = os.path.join(self.root, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text)
        return path

    def profile(self, name, *lines, mode="atomic"):
        return self.write(name, "mode: %s\n%s\n" % (mode, "\n".join(lines)))

    def run_main(self, *args):
        argv = ["--root", self.root, "--exclude", os.path.join(self.root, "exclude.txt")] + list(args)
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            try:
                rc = coverage.main(argv)
            except SystemExit as e:
                rc = e.code
        return rc, out.getvalue(), err.getvalue()

    def total(self, out):
        return float(re.search(r"TOTAL_LINE_COVERAGE=([\d.]+)", out).group(1))


class MergeTest(Fixture):
    def test_same_block_in_several_profiles_is_ored_and_counted_once(self):
        self.write("a/a.go", GO_A)
        blk = "%s/a/a.go:3.10,7.2 4 %d"
        p1 = self.profile("p1.out", blk % (ROOT_MOD, 0))
        p2 = self.profile("p2.out", blk % (ROOT_MOD, 3))
        p3 = self.profile("p3.out", blk % (ROOT_MOD, 5))
        mods = coverage.module_map(self.root)
        blocks = coverage.read_profiles([p1, p2, p3], mods)
        self.assertEqual(len(blocks), 1)
        self.assertEqual(list(blocks.values()), [8])
        merged = os.path.join(self.root, "merged.out")
        rc, out, _ = self.run_main(p1, p2, p3, "--merged", merged)
        self.assertEqual(rc, 0)
        self.assertEqual(self.total(out), 100.0)
        # code lines 3,4,6,7 (line 5 is a comment); statements 4, not 12
        self.assertIn("(4 of 4 lines)", out)
        self.assertIn("100.00% (4 of 4)", out)

    def test_mixed_count_modes_all_accepted(self):
        self.write("a/a.go", GO_A)
        blk = "%s/a/a.go:3.10,7.2 4 %d" % (ROOT_MOD, 2)
        p1 = self.profile("set.out", blk.rsplit(" ", 1)[0] + " 1", mode="set")
        p2 = self.profile("count.out", blk, mode="count")
        p3 = self.profile("atomic.out", blk, mode="atomic")
        merged = os.path.join(self.root, "merged.out")
        rc, _, _ = self.run_main(p1, p2, p3, "--merged", merged)
        self.assertEqual(rc, 0)
        with open(merged, encoding="utf-8") as fh:
            lines = fh.read().splitlines()
        self.assertEqual(lines[0], "mode: atomic")
        self.assertEqual(len(lines), 2)
        self.assertTrue(lines[1].endswith(" 4 5"), lines[1])

    def test_bad_line_and_unknown_module_exit(self):
        mods = coverage.module_map(self.root)
        bad = self.profile("bad.out", "garbage")
        with self.assertRaises(SystemExit):
            coverage.read_profiles([bad], mods)
        other = self.profile("other.out", "github.com/other/x/f.go:1.1,2.2 1 1")
        with self.assertRaises(SystemExit):
            coverage.read_profiles([other], mods)


class PathMappingTest(Fixture):
    def test_three_modules_map_to_repo_relative(self):
        mods = coverage.module_map(self.root)
        cases = {
            ROOT_MOD + "/a/a.go": "a/a.go",
            ROOT_MOD + "/main.go": "main.go",
            CORE_MOD + "/model/x.go": "modules/core/model/x.go",
            RULES_MOD + "/parser/p.go": "rules/parser/p.go",
        }
        for name, want in cases.items():
            self.assertEqual(coverage.to_repo_path(name, mods), want, name)
        self.assertIsNone(coverage.to_repo_path("example.com/appx/y.go", mods))
        self.assertIsNone(coverage.to_repo_path("other.org/z.go", mods))

    def test_module_of(self):
        self.assertEqual(coverage.module_of("modules/core/x/y.go"), "modules/core")
        self.assertEqual(coverage.module_of("rules/y.go"), "rules")
        self.assertEqual(coverage.module_of("cmd/y.go"), "root")


class ExcludeTest(Fixture):
    def matches(self, path):
        pats = coverage.compile_patterns(coverage.load_patterns(os.path.join(self.root, "exclude.txt")))
        return any(r.match(path) for r in pats)

    def test_root_only_vs_nested(self):
        self.assertTrue(self.matches("testutils/x.go"))
        self.assertTrue(self.matches("testutils/sub/x.go"))
        self.assertTrue(self.matches("modules/core/testutils/x.go"))
        # `testutils/` is anchored at the repository root
        self.assertFalse(self.matches("cmd/testutils/x.go"))
        self.assertFalse(self.matches("modules/core/other/x.go"))
        self.assertFalse(self.matches("testutilsx/x.go"))

    def test_double_star(self):
        self.assertTrue(self.matches("gen_a.go"))
        self.assertTrue(self.matches("pkg/deep/gen_a.go"))
        self.assertFalse(self.matches("pkg/gen.go"))

    def test_excluded_files_leave_the_denominator(self):
        self.write("a/a.go", GO_A)
        self.write("testutils/t.go", GO_A)
        p = self.profile("p.out",
                         "%s/a/a.go:3.10,7.2 4 1" % ROOT_MOD,
                         "%s/testutils/t.go:3.10,7.2 4 0" % ROOT_MOD)
        merged = os.path.join(self.root, "merged.out")
        rc, out, _ = self.run_main(p, "--merged", merged)
        self.assertEqual(rc, 0)
        self.assertEqual(self.total(out), 100.0)
        with open(merged, encoding="utf-8") as fh:
            self.assertNotIn("testutils", fh.read())
        rc, out, _ = self.run_main(p, "--no-exclude")
        self.assertLess(self.total(out), 100.0)


class CodeLinesTest(Fixture):
    def lines(self, src):
        return sorted(coverage.code_lines(self.write("x.go", src)))

    def test_blank_and_comment_only_lines_skipped(self):
        self.assertEqual(self.lines(GO_A), [1, 3, 4, 6, 7])

    def test_raw_string_with_comment_opener(self):
        self.assertEqual(self.lines(GO_RAW), [1, 3, 4, 5, 10])

    def test_strings_and_runes(self):
        src = 'var b = "/*" // x\nvar c = \'/\'\nvar d = "a\\"/*"\n'
        self.assertEqual(self.lines(src), [1, 2, 3])

    def test_missing_file(self):
        self.assertIsNone(coverage.code_lines(os.path.join(self.root, "nope.go")))

    def test_self_test_passes(self):
        coverage.self_test()

    def test_block_spanning_lines_and_overlap(self):
        self.write("a/a.go", GO_A)
        # block 1 covers lines 3-5 (uncovered), block 2 covers lines 5-7 (covered):
        # line 5 is a comment (not counted); lines 3,4 miss; 6,7 hit -> 2 of 4.
        p = self.profile("p.out",
                         "%s/a/a.go:3.10,5.2 2 0" % ROOT_MOD,
                         "%s/a/a.go:5.2,7.2 2 1" % ROOT_MOD)
        rc, out, _ = self.run_main(p)
        self.assertEqual(rc, 0)
        self.assertEqual(self.total(out), 50.0)
        self.assertIn("(2 of 4 lines)", out)
        self.assertIn("statement coverage: 50.00%", out)


class ThresholdTest(Fixture):
    def setUp(self):
        super().setUp()
        self.write("a/a.go", GO_A)  # code lines 3,4,6,7 inside the block
        # 4 lines, 3 covered by a first block, 1 by an uncovered one -> 75%
        self.p = self.profile("p.out",
                              "%s/a/a.go:3.10,4.2 1 1" % ROOT_MOD + "\n" +
                              "%s/a/a.go:4.2,4.10 1 1" % ROOT_MOD + "\n" +
                              "%s/a/a.go:6.2,6.8 1 1" % ROOT_MOD + "\n" +
                              "%s/a/a.go:7.1,7.2 1 0" % ROOT_MOD)

    def test_exactly_at_minimum_passes(self):
        rc, out, _ = self.run_main(self.p, "--min", "75")
        self.assertEqual(self.total(out), 75.0)
        self.assertEqual(rc, 0)

    def test_just_below_fails(self):
        rc, _, err = self.run_main(self.p, "--min", "75.01")
        self.assertEqual(rc, 1)
        self.assertIn("below the minimum", err)

    def test_no_min_never_fails(self):
        rc, _, _ = self.run_main(self.p)
        self.assertEqual(rc, 0)

    def test_generated_file_not_excluded_fails(self):
        self.write("g/g.go", "// Code generated by x. DO NOT EDIT.\npackage g\n\nvar a = 1\n")
        p = self.profile("g.out", "%s/g/g.go:4.1,4.10 1 1" % ROOT_MOD)
        rc, _, err = self.run_main(p)
        self.assertEqual(rc, 1)
        self.assertIn("generated files missing", err)


class OutputTest(Fixture):
    def test_cobertura_shape_and_merged_profile(self):
        self.write("a/a.go", GO_A)
        self.write("modules/core/m/m.go", GO_A)
        self.write("rules/r/r.go", GO_A)
        p = self.profile("p.out",
                         "%s/a/a.go:3.10,7.2 4 1" % ROOT_MOD,
                         "%s/m/m.go:3.10,7.2 4 0" % CORE_MOD,
                         "%s/r/r.go:3.10,7.2 4 1" % RULES_MOD)
        cob = os.path.join(self.root, "cobertura.xml")
        merged = os.path.join(self.root, "merged.out")
        rc, _, _ = self.run_main(p, "--cobertura", cob, "--merged", merged)
        self.assertEqual(rc, 0)
        tree = ET.parse(cob).getroot()
        self.assertAlmostEqual(float(tree.get("line-rate")), 8 / 12, places=5)
        self.assertEqual((tree.get("lines-covered"), tree.get("lines-valid")), ("8", "12"))
        classes = {c.get("filename"): c for c in tree.iter("class")}
        self.assertEqual(sorted(classes), ["a/a.go", "modules/core/m/m.go", "rules/r/r.go"])
        self.assertEqual(float(classes["modules/core/m/m.go"].get("line-rate")), 0.0)
        self.assertEqual(float(classes["a/a.go"].get("line-rate")), 1.0)
        pkgs = sorted(p.get("name") for p in tree.iter("package"))
        self.assertEqual(pkgs, ["a", "modules/core/m", "rules/r"])
        nums = [int(l.get("number")) for l in classes["a/a.go"].iter("line")]
        self.assertEqual(nums, [3, 4, 6, 7])
        with open(merged, encoding="utf-8") as fh:
            body = fh.read().splitlines()
        self.assertEqual(body[0], "mode: atomic")
        self.assertEqual(len(body), 4)
        for ln in body[1:]:
            self.assertRegex(ln, r"^\./[^:]+:\d+\.\d+,\d+\.\d+ \d+ \d+$")
        self.assertIn("./modules/core/m/m.go:3.10,7.2 4 0", body)

    def test_summary_written(self):
        self.write("a/a.go", GO_A)
        p = self.profile("p.out", "%s/a/a.go:3.10,7.2 4 1" % ROOT_MOD)
        summ = os.path.join(self.root, "summary.md")
        self.run_main(p, "--summary", summ, "--top", "5")
        with open(summ, encoding="utf-8") as fh:
            self.assertIn("Total line coverage: 100.00%", fh.read())


class CodecovSyncTest(Fixture):
    def codecov(self, entries):
        body = "coverage:\n  x: 1\nignore:\n%s\n\nother: 1\n" % "\n".join('  - "%s"' % e for e in entries)
        return self.write("codecov.yml", body)

    def test_in_sync_passes(self):
        pats = coverage.load_patterns(os.path.join(self.root, "exclude.txt"))
        coverage.check_codecov(self.codecov(pats), pats)

    def test_drift_fails(self):
        pats = coverage.load_patterns(os.path.join(self.root, "exclude.txt"))
        with self.assertRaises(SystemExit) as cm:
            coverage.check_codecov(self.codecov(pats[:-1] + ["extra/"]), pats)
        self.assertIn("extra/", str(cm.exception))

    def test_repository_files_are_in_sync(self):
        repo = os.path.abspath(os.path.join(HERE, "..", ".."))
        pats = coverage.load_patterns(os.path.join(HERE, "coverage-exclude.txt"))
        coverage.check_codecov(os.path.join(repo, "codecov.yml"), pats)


if __name__ == "__main__":
    unittest.main()
