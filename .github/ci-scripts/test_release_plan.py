"""Tests for release-plan.sh (stdlib unittest; run: make test-ci-scripts)."""
import os
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(HERE, "release-plan.sh")
MAIN = "refs/heads/main"


class Plan(unittest.TestCase):
    def setUp(self):
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        self.dir = td.name
        self.git("init", "-q")
        self.git("commit", "-q", "--allow-empty", "-m", "c")

    def git(self, *args):
        # Isolated from the user's git configuration (signing, hooks, templates): the fixture
        # must not depend on a signer being available.
        env = {"PATH": os.environ["PATH"], "HOME": self.dir, "GIT_CONFIG_GLOBAL": os.devnull,
               "GIT_CONFIG_SYSTEM": os.devnull, "GIT_CONFIG_COUNT": "4",
               "GIT_CONFIG_KEY_0": "user.name", "GIT_CONFIG_VALUE_0": "t",
               "GIT_CONFIG_KEY_1": "user.email", "GIT_CONFIG_VALUE_1": "t@t",
               "GIT_CONFIG_KEY_2": "commit.gpgsign", "GIT_CONFIG_VALUE_2": "false",
               "GIT_CONFIG_KEY_3": "tag.gpgsign", "GIT_CONFIG_VALUE_3": "false"}
        subprocess.run(["git", *args], cwd=self.dir, env=env, check=True, capture_output=True)

    def tags(self, *names):
        for n in names:
            self.git("tag", n)

    def run_plan(self, **env):
        outfile = os.path.join(self.dir, "out.txt")
        e = {"PATH": os.environ["PATH"], "HOME": self.dir, "GIT_CONFIG_GLOBAL": os.devnull,
             "GIT_CONFIG_SYSTEM": os.devnull, "GITHUB_OUTPUT": outfile,
             "GITHUB_STEP_SUMMARY": os.path.join(self.dir, "sum.txt")}
        e.update(env)
        p = subprocess.run(["bash", SCRIPT], cwd=self.dir, env=e, capture_output=True, text=True)
        out = {}
        if p.returncode == 0:
            with open(outfile) as f:
                out = dict(line.strip().split("=", 1) for line in f if "=" in line)
        return p, out

    def dispatch(self, bump="minor", dry="false", ref=MAIN, release_tag=""):
        return self.run_plan(EVENT_NAME="workflow_dispatch", REF=ref, REF_NAME=ref.split("/", 2)[-1],
                             INPUT_BUMP=bump, INPUT_DRY_RUN=dry, INPUT_RELEASE_TAG=release_tag)

    def ok(self, p, out, tag, dry, create):
        self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        self.assertEqual(out, {"dry-run": dry, "tag": tag, "create-tag": create})

    def refused(self, p, text):
        self.assertEqual(p.returncode, 1, p.stdout + p.stderr)
        self.assertIn("::error::", p.stdout)
        self.assertIn(text, p.stdout)

    def test_first_release_is_v010_for_every_bump(self):
        for bump in ("minor", "major"):
            with self.subTest(bump=bump):
                self.ok(*self.dispatch(bump=bump), "v0.1.0", "false", "true")

    def test_bumps_from_v010(self):
        self.tags("v0.1.0")
        for bump, want in (("minor", "v0.2.0"), ("major", "v1.0.0")):
            with self.subTest(bump=bump):
                self.ok(*self.dispatch(bump=bump), want, "false", "true")

    def test_highest_stable_tag_wins_and_prereleases_are_ignored(self):
        self.tags("v0.9.0", "v0.10.0", "v0.11.0-rc.1", "v0.10.1-beta.2", "v2-junk")
        self.ok(*self.dispatch(bump="minor"), "v0.11.0", "false", "true")

    def test_major_2_refused(self):
        self.tags("v1.2.3")
        self.ok(*self.dispatch(bump="minor"), "v1.3.0", "false", "true")
        self.refused(self.dispatch(bump="major")[0], "major >= 2")

    def test_unknown_bump_refused(self):
        self.refused(self.dispatch(bump="huge")[0], "unknown bump")
        self.tags("v0.1.0")
        self.refused(self.dispatch(bump="huge")[0], "unknown bump")

    def test_patch_bump_refused(self):
        # Refused with and without a previous tag, and on a dry run.
        self.refused(self.dispatch(bump="patch")[0], "'patch' is not offered")
        self.tags("v0.1.0")
        self.refused(self.dispatch(bump="patch")[0], "'patch' is not offered")
        self.refused(self.dispatch(bump="patch", dry="true")[0], "'patch' is not offered")

    def test_bump_defaults_to_minor(self):
        self.tags("v0.1.0")
        self.ok(*self.run_plan(EVENT_NAME="workflow_dispatch", REF=MAIN, REF_NAME="main",
                               INPUT_DRY_RUN="false"), "v0.2.0", "false", "true")

    def test_dry_run_creates_nothing_and_works_from_any_ref(self):
        self.tags("v0.1.0")
        self.ok(*self.dispatch(bump="minor", dry="true", ref="refs/heads/topic"), "v0.2.0", "true", "false")

    def test_release_only_from_main(self):
        self.refused(self.dispatch(ref="refs/heads/topic")[0], "from main only")
        self.refused(self.dispatch(ref="refs/tags/v0.1.0")[0], "from main only")

    def test_tag_push_publishes_without_creating(self):
        for tag in ("v0.1.0", "v0.2.0-rc.1", "v1.0.0-beta.3"):
            with self.subTest(tag=tag):
                p, out = self.run_plan(EVENT_NAME="push", REF="refs/tags/" + tag, REF_NAME=tag)
                self.ok(p, out, tag, "false", "false")

    def test_tag_push_with_bad_version_refused(self):
        for tag in ("v1.0", "v1.0.0-foo.1", "v01.0.0", "v1.0.0-rc"):
            with self.subTest(tag=tag):
                p, _ = self.run_plan(EVENT_NAME="push", REF="refs/tags/" + tag, REF_NAME=tag)
                self.refused(p, "not a release version")

    def test_release_tag_republish(self):
        self.tags("v0.1.0", "v0.2.0-rc.1")
        for tag in ("v0.1.0", "v0.2.0-rc.1"):
            with self.subTest(tag=tag):
                self.ok(*self.dispatch(ref="refs/tags/" + tag, release_tag=tag), tag, "false", "false")

    def test_release_tag_missing_refused(self):
        self.refused(self.dispatch(ref="refs/tags/v0.1.0", release_tag="v0.1.0")[0], "does not exist")

    def test_release_tag_needs_dry_run_false_and_run_on_the_tag(self):
        self.tags("v0.1.0")
        self.refused(self.dispatch(dry="true", ref="refs/tags/v0.1.0", release_tag="v0.1.0")[0], "set dry_run=false")
        self.refused(self.dispatch(release_tag="v0.1.0")[0], "started on the tag")

    def test_release_tag_must_be_a_release_version(self):
        self.tags("v0.1.0-foo.1")
        self.refused(self.dispatch(ref="refs/tags/v0.1.0-foo.1", release_tag="v0.1.0-foo.1")[0], "not a release version")


if __name__ == "__main__":
    unittest.main()
