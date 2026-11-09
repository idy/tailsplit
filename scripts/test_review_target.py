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
const outputs = {}, dispatches = [];
const core = {setOutput: (key, value) => {outputs[key] = value;}, info() {}};
const github = {rest: {
  pulls: {get: async () => ({data: data.pr || {state:'open',draft:false,head:{sha:'current-head'},base:{ref:'release',sha:'release-base'}}})},
  repos: {get: async () => ({data: {default_branch:'main'}})},
  actions: {
    getWorkflowRun: async () => ({data: data.source || {id:100,event:'pull_request_target',head_sha:'current-head',workflow_id:11}}),
    createWorkflowDispatch: async (value) => {dispatches.push(value);},
  },
}};
process.env.CODEX_REVIEW_PR_INPUT = data.input || '';
process.env.CODEX_REVIEW_REF_PROTECTED = data.protected ? 'true' : 'false';
process.env.CODEX_DISPATCH_SOURCE_RUN = data.source_id || '';
process.env.CODEX_DISPATCH_BASE_REF = data.original_ref || '';
process.env.CODEX_DISPATCH_BASE_SHA = data.original_sha || '';
process.env.GITHUB_RUN_ATTEMPT = String(data.attempt || 1);
(async () => {
  let error = '';
  try {
    const AsyncFunction = Object.getPrototypeOf(async function(){}).constructor;
    await new AsyncFunction('github', 'context', 'core', data.script)(github, data.context, core);
  } catch (failure) { error = failure.message; }
  process.stdout.write(JSON.stringify({outputs, dispatches, error}));
})();
"""


@unittest.skipUnless(NODE, "Node.js is needed for workflow resolver tests")
class ReviewTargetTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = (ROOT / ".github/workflows/codex-review.yml").read_text()
        match = re.search(
            r"(?ms)^  resolve_review_target:.*?^          script: \|\n(.*?)(?=^  review:)",
            cls.workflow,
        )
        if not match:
            raise AssertionError("Review target script was not found")
        cls.script = textwrap.dedent(match.group(1))
        fallback = re.search(
            r"(?ms)^  dispatch_nondefault_review:.*?^          script: \|\n(.*?)(?=^  resolve_linked_prs:)",
            cls.workflow,
        )
        if not fallback:
            raise AssertionError("Non-default fallback script was not found")
        cls.fallback = textwrap.dedent(fallback.group(1))

    def context(self, event, **payload):
        return {"eventName": event, "ref": "refs/heads/main",
                "repo": {"owner": "idy", "repo": "tailsplit"}, "runId": 900,
                "payload": {"repository": {"default_branch": "main"}, **payload}}

    def resolve(self, context, request="", protected=True, fallback=False, **fixtures):
        result = subprocess.run(
            [NODE, "-e", HARNESS], cwd=ROOT, text=True, capture_output=True, check=True,
            input=json.dumps({"script": self.fallback if fallback else self.script, "context": context,
                              "input": request, "protected": protected, **fixtures}),
        )
        return json.loads(result.stdout)

    def test_target_lifecycle_including_sync_always_resolves_its_pr(self):
        for action in ("opened", "synchronize", "edited", "ready_for_review"):
            with self.subTest(action=action):
                context = self.context("pull_request_target", action=action, pull_request={"number": 29, "base": {"ref": "main"}})
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

    def fallback_context(self):
        context = self.context("pull_request_target", pull_request={
            "number": 29, "base": {"ref": "release", "sha": "release-base"}, "head": {"sha": "current-head"},
        })
        context.update(ref="refs/heads/release", repo={"owner": "idy", "repo": "tailsplit"}, runId=100)
        return context

    def test_nondefault_target_never_runs_local_secret_job(self):
        self.assertEqual(self.resolve(self.fallback_context())["outputs"], {"number": ""})

    def test_nondefault_target_dispatches_only_to_default_branch(self):
        result = self.resolve(self.fallback_context(), fallback=True)
        self.assertEqual(result["error"], "")
        self.assertEqual(result["dispatches"], [{
            "owner": "idy", "repo": "tailsplit", "workflow_id": 11, "ref": "main",
            "inputs": {"pull_request_number": "29", "dispatch_source_run_id": "100",
                       "dispatch_base_ref": "release", "dispatch_base_sha": "release-base"},
        }])

    def test_nondefault_stale_closed_and_draft_requests_are_skipped(self):
        for pr in (
            {"state": "open", "draft": False, "head": {"sha": "older-head"}},
            {"state": "closed", "draft": False, "head": {"sha": "current-head"}},
            {"state": "open", "draft": True, "head": {"sha": "current-head"}},
            {"state": "open", "draft": False, "head": {"sha": "current-head"}, "base": {"ref": "main"}},
        ):
            result = self.resolve(self.fallback_context(), fallback=True, pr=pr)
            self.assertEqual(result["dispatches"], [])
            self.assertEqual(result["error"], "")

    def test_nondefault_rerun_and_wrong_source_are_rejected(self):
        result = self.resolve(self.fallback_context(), fallback=True, attempt=2)
        self.assertIn("fresh", result["error"])
        self.assertEqual(result["dispatches"], [])
        result = self.resolve(self.fallback_context(), fallback=True, source={
            "id": 100, "event": "pull_request_target", "head_sha": "older-head", "workflow_id": 11,
        })
        self.assertIn("trusted review source", result["error"])
        self.assertEqual(result["dispatches"], [])

    def test_captured_base_is_rechecked_after_workflow_queue_admission(self):
        context = self.context("workflow_dispatch")
        captured = {"source_id": "100", "original_ref": "release", "original_sha": "release-base"}
        result = self.resolve(context, request="29", **captured)
        self.assertEqual(result["outputs"], {"number": "29"})
        for ref, sha in (("main", "release-base"), ("other-release", "release-base"), ("release", "new-base")):
            result = self.resolve(context, request="29", pr={
                "state": "open", "draft": False, "base": {"ref": ref, "sha": sha},
            }, **captured)
            self.assertEqual(result["outputs"], {"number": ""})

    def test_legacy_dispatch_cannot_overwrite_default_target_review(self):
        context = self.context("workflow_dispatch")
        result = self.resolve(context, request="29", source_id="100")
        self.assertEqual(result["outputs"], {"number": "29"}, "legacy non-default callers remain compatible")
        result = self.resolve(context, request="29", source_id="100", pr={
            "state": "open", "draft": False, "base": {"ref": "main", "sha": "base"},
        })
        self.assertEqual(result["outputs"], {"number": ""})
        result = self.resolve(context, request="29", source_id="100", original_ref="release")
        self.assertEqual(result["outputs"], {"number": ""}, "partial base captures fail closed")

    def test_whole_workflow_queue_is_shared_by_target_and_dispatch(self):
        queue = self.workflow.split("\njobs:", 1)[0].split("\nconcurrency:", 1)[1]
        self.assertIn("github.event.pull_request.number || github.event.issue.number || inputs.pull_request_number", queue)
        self.assertIn("cancel-in-progress: false", queue)

    def test_push_event_cannot_invoke_the_reviewer(self):
        events = self.workflow.split("\npermissions:", 1)[0]
        self.assertNotRegex(events, r"(?m)^  push:")
        self.assertIn("Unsupported review event", self.resolve(self.context("push"))["error"])


if __name__ == "__main__":
    unittest.main()
