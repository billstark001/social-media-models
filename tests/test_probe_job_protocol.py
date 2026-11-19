import io
import json
from types import SimpleNamespace

import pytest

from smp_bindings.probe import run_probe_jobs


def fake_worker(monkeypatch, response):
  worker = SimpleNamespace(
      stdin=io.StringIO(), stdout=io.StringIO(json.dumps(response) + "\n"),
      poll=lambda: 0, wait=lambda: 0,
  )
  monkeypatch.setattr("smp_bindings.probe.subprocess.Popen", lambda *args, **kwargs: worker)
  return worker


@pytest.mark.parametrize("request_id", [None, ""])
def test_error_without_recoverable_id_uses_exact_line(monkeypatch, request_id):
  response = {"line": 1, "status": "error", "error": "request line exceeds limit"}
  if request_id is not None:
    response["request_id"] = request_id
  worker = fake_worker(monkeypatch, response)
  with pytest.raises(RuntimeError, match="job 1: request line exceeds limit"):
    list(run_probe_jobs([{"request_id": "case"}]))
  assert worker.stdin.closed and worker.stdout.closed


@pytest.mark.parametrize("status", ["ok", "error"])
def test_reply_with_conflicting_id_is_rejected(monkeypatch, status):
  fake_worker(monkeypatch, {"line": 1, "status": status, "request_id": "other"})
  with pytest.raises(RuntimeError, match="uncorrelated"):
    list(run_probe_jobs([{"request_id": "case"}]))


@pytest.mark.parametrize("request_id", ["", "   ", None, 3])
def test_job_requires_nonempty_string_id(monkeypatch, request_id):
  fake_worker(monkeypatch, {})
  with pytest.raises(ValueError, match="request_id must be a non-empty string"):
    list(run_probe_jobs([{"request_id": request_id}]))


def test_normal_reply_still_requires_matching_id(monkeypatch):
  result = {"version": 1, "results": []}
  fake_worker(monkeypatch, {"line": 1, "status": "ok", "request_id": "case", "result": result})
  assert list(run_probe_jobs([{"request_id": "case"}])) == [result]
