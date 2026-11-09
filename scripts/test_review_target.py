"""Exercise the workflow's metadata-only review target resolver."""
import copy
import json
from pathlib import Path
import re
import shutil
import subprocess
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[1]
NODE = shutil.which("node")
HARNESS = r"""
const fs = require('node:fs');
const data = JSON.parse(fs.readFileSync(0, 'utf8'));
const outputs = {}, calls = [];
const github = { rest: { repos: { listPullRequestsAssociatedWithCommit: async (args) => {
  calls.push(args); return {data: data.pulls || []};
} } } };
const core = {setOutput: (key, value) => {outputs[key] = value;}, info() {}};
process.env.CODEX_REVIEW_PR_INPUT = data.input || '';
(async () => {
  let error = '';
  try {
    const AsyncFunction = Object.getPrototypeOf(async function(){}).constructor;
    await new AsyncFunction('github', 'context', 'core', data.script)(github, data.context, core);
  } catch (failure) { error = failure.message; }
  process.stdout.write(JSON.stringify({outputs, calls, error}));
})();
"""


@unittest.skipUnless(NODE, "Node.js is needed for workflow resolver tests")
class ReviewTargetTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        workflow = (ROOT / ".github/workflows/openai-review.yml").read_text()
        match = re.search(
            r"(?ms)^  resolve_review_target:.*?^          script: \|\n(.*?)(?=^  review:)",
            workflow,
        )
        if not match:
            raise AssertionError("Review target script was not found")
        cls.script = textwrap.dedent(match.group(1))

    def resolve(self, context, pulls=(), request=""):
        result = subprocess.run(
            [NODE, "-e", HARNESS], cwd=ROOT, text=True, capture_output=True, check=True,
            input=json.dumps({"script": self.script, "context": context,
                              "pulls": list(pulls), "input": request}),
        )
        return json.loads(result.stdout)

    def push(self):
        return {"eventName": "push", "repo": {"owner": "idy", "repo": "tailsplit"},
                "sha": "current-head", "payload": {"repository": {"default_branch": "main"}}}

    def pull(self):
        return {"number": 29, "state": "open", "draft": False,
                "head": {"sha": "current-head", "repo": {"full_name": "idy/tailsplit"}},
                "base": {"ref": "main", "repo": {"full_name": "idy/tailsplit"}}}

    def test_current_internal_push_resolves_exact_pr(self):
        result = self.resolve(self.push(), [self.pull()])
        self.assertEqual(result["outputs"], {"number": "29"})
        self.assertEqual(result["error"], "")
        self.assertEqual(result["calls"][0]["commit_sha"], "current-head")

    def test_stale_draft_closed_and_foreign_pushes_do_not_review(self):
        changes = [
            lambda pr: pr["head"].update(sha="older-head"),
            lambda pr: pr.update(draft=True),
            lambda pr: pr.update(state="closed"),
            lambda pr: pr["head"]["repo"].update(full_name="fork/tailsplit"),
            lambda pr: pr["base"]["repo"].update(full_name="other/repo"),
            lambda pr: pr["base"].update(ref="other-base"),
        ]
        for change in changes:
            with self.subTest(change=changes.index(change)):
                pr = self.pull()
                change(pr)
                result = self.resolve(self.push(), [pr])
                self.assertEqual(result["outputs"], {"number": ""})
                self.assertEqual(result["error"], "")

    def test_no_open_pr_is_a_safe_noop(self):
        self.assertEqual(self.resolve(self.push())["outputs"], {"number": ""})

    def test_ambiguous_push_is_rejected(self):
        other = copy.deepcopy(self.pull())
        other["number"] = 30
        self.assertIn("Ambiguous", self.resolve(self.push(), [self.pull(), other])["error"])

    def test_missing_default_branch_is_rejected(self):
        context = self.push()
        context["payload"]["repository"] = {}
        self.assertIn("default branch", self.resolve(context, [self.pull()])["error"])

    def test_target_event_uses_its_pr_metadata(self):
        result = self.resolve({"eventName": "pull_request_target", "payload": {"pull_request": {"number": 29}}})
        self.assertEqual(result["outputs"], {"number": "29"})
        self.assertEqual(result["calls"], [])

    def test_comment_target_remains_the_comment_pr(self):
        context = {"eventName": "issue_comment", "payload": {"issue": {"number": 29, "pull_request": {}}}}
        result = self.resolve(context, request="77")
        self.assertEqual(result["outputs"], {"number": "29"})
        self.assertEqual(result["calls"], [])

    def test_manual_target_requires_a_positive_safe_integer(self):
        context = {"eventName": "workflow_dispatch", "payload": {}}
        self.assertEqual(self.resolve(context, request="29")["outputs"], {"number": "29"})
        for invalid in ("", "0", "-1", "1.5", "9007199254740992", "not-a-number"):
            with self.subTest(invalid=invalid):
                self.assertIn("positive safe integer", self.resolve(context, request=invalid)["error"])


if __name__ == "__main__":
    unittest.main()
