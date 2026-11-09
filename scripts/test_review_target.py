"""Exercise the protected-default workflow's metadata-only target resolver."""
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
const outputs = {};
const core = {setOutput: (key, value) => {outputs[key] = value;}, info() {}};
process.env.CODEX_REVIEW_PR_INPUT = data.input || '';
process.env.CODEX_REVIEW_REF_PROTECTED = data.protected ? 'true' : 'false';
(async () => {
  let error = '';
  try {
    const AsyncFunction = Object.getPrototypeOf(async function(){}).constructor;
    await new AsyncFunction('context', 'core', data.script)(data.context, core);
  } catch (failure) { error = failure.message; }
  process.stdout.write(JSON.stringify({outputs, error}));
})();
"""


@unittest.skipUnless(NODE, "Node.js is needed for workflow resolver tests")
class ReviewTargetTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = (ROOT / ".github/workflows/openai-review.yml").read_text()
        match = re.search(
            r"(?ms)^  resolve_review_target:.*?^          script: \|\n(.*?)(?=^  review:)",
            cls.workflow,
        )
        if not match:
            raise AssertionError("Review target script was not found")
        cls.script = textwrap.dedent(match.group(1))

    def context(self, event, **payload):
        return {"eventName": event, "ref": "refs/heads/main",
                "payload": {"repository": {"default_branch": "main"}, **payload}}

    def resolve(self, context, request="", protected=True):
        result = subprocess.run(
            [NODE, "-e", HARNESS], cwd=ROOT, text=True, capture_output=True, check=True,
            input=json.dumps({"script": self.script, "context": context,
                              "input": request, "protected": protected}),
        )
        return json.loads(result.stdout)

    def test_target_lifecycle_including_sync_always_resolves_its_pr(self):
        for action in ("opened", "synchronize", "edited", "ready_for_review"):
            with self.subTest(action=action):
                context = self.context("pull_request_target", action=action, pull_request={"number": 29})
                result = self.resolve(context)
                self.assertEqual(result["outputs"], {"number": "29"})
                self.assertEqual(result["error"], "")

    def test_comment_target_stays_the_comment_pr(self):
        context = self.context("issue_comment", issue={"number": 29, "pull_request": {}})
        result = self.resolve(context, request="77")
        self.assertEqual(result["outputs"], {"number": "29"})

    def test_manual_target_requires_a_positive_safe_integer(self):
        context = self.context("workflow_dispatch")
        self.assertEqual(self.resolve(context, request="29")["outputs"], {"number": "29"})
        for invalid in ("", "0", "-1", "1.5", "9007199254740992", "not-a-number"):
            with self.subTest(invalid=invalid):
                self.assertIn("positive safe integer", self.resolve(context, request=invalid)["error"])

    def test_unprotected_default_ref_cannot_start_secret_bearing_review(self):
        result = self.resolve(self.context("workflow_dispatch"), request="29", protected=False)
        self.assertEqual(result["outputs"], {"number": ""})
        self.assertEqual(result["error"], "")

    def test_protected_nondefault_ref_is_also_rejected(self):
        context = self.context("workflow_dispatch")
        context["ref"] = "refs/heads/proposed-workflow"
        self.assertEqual(self.resolve(context, request="29")["outputs"], {"number": ""})

    def test_missing_default_branch_is_a_safe_noop(self):
        context = self.context("workflow_dispatch")
        context["payload"]["repository"] = {}
        self.assertEqual(self.resolve(context, request="29")["outputs"], {"number": ""})

    def test_push_event_cannot_invoke_the_reviewer(self):
        events = self.workflow.split("\npermissions:", 1)[0]
        self.assertNotRegex(events, r"(?m)^  push:")
        self.assertIn("Unsupported review event", self.resolve(self.context("push"))["error"])


if __name__ == "__main__":
    unittest.main()
