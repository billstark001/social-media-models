"""Inherited clients must never operate or reap a parent's native worker."""

import io
import json
import subprocess
import gc
import weakref
from types import SimpleNamespace

import pytest

import smp_bindings.probe as binding


def worker_fixture(monkeypatch):
  pid = [100]
  actions = []
  reply = {"line": 1, "request_id": "case", "status": "ok", "result": {"version": 1}}
  worker = SimpleNamespace(stdin=io.StringIO(), stdout=io.StringIO(json.dumps(reply) + "\n"), returncode=None)
  def poll():
    actions.append("poll")
    return worker.returncode
  def wait(timeout=None):
    actions.append("wait")
    worker.returncode = 0
    return 0
  worker.poll, worker.wait = poll, wait
  monkeypatch.setattr(binding.os, "getpid", lambda: pid[0])
  monkeypatch.setattr(binding.subprocess, "Popen", lambda *args, **kwargs: worker)
  monkeypatch.setattr(binding, "terminate_process", lambda *args, **kwargs: actions.append("signal"))
  return pid, worker, actions


def test_inherited_client_rejects_before_touching_inherited_lock(monkeypatch):
  pid, _, actions = worker_fixture(monkeypatch)
  client = binding.ProbeClient("unused")
  class InheritedLock:
    def __enter__(self):
      pytest.fail("child touched a possibly locked parent mutex")
    def __exit__(self, *args):
      return False
  client._lock = InheritedLock()
  pid[0] = 101
  with pytest.raises(RuntimeError, match="different process"):
    client.run({})
  client.close()
  assert not actions


def test_child_close_leaves_parent_client_usable_and_owner_close_reaps(monkeypatch):
  pid, worker, actions = worker_fixture(monkeypatch)
  client = binding.ProbeClient("unused")
  assert client.run({"request_id": "case"}) == {"version": 1}
  pid[0] = 101
  client.close()
  assert not worker.stdin.closed and not worker.stdout.closed and not actions
  pid[0] = 100
  client.close()
  assert worker.stdin.closed and worker.stdout.closed
  assert "wait" in actions and "signal" not in actions


@pytest.mark.parametrize("resume", [False, True])
def test_inherited_job_generator_neither_flushes_nor_reaps(monkeypatch, resume):
  pid, worker, actions = worker_fixture(monkeypatch)
  jobs = binding.run_probe_jobs([{"request_id": "case"}, {"request_id": "next"}], binary_path="unused")
  next(jobs)
  pid[0] = 101
  if resume:
    with pytest.raises(RuntimeError, match="different process"):
      next(jobs)
  else:
    jobs.close()
  assert not actions and not worker.stdin.closed and not worker.stdout.closed
  # Fake handles do not have an operating system process-exit cleanup.
  worker.stdin.close()
  worker.stdout.close()


def test_exhausting_bulk_jobs_bounds_eof_wait_and_reaps(monkeypatch):
  _, worker, actions = worker_fixture(monkeypatch)
  def wait(timeout=None):
    assert worker.stdin.closed and timeout == 1
    actions.append("wait")
    raise subprocess.TimeoutExpired("worker", timeout)
  def terminate(process, *, process_group):
    assert process is worker and process_group
    actions.append("signal")
    worker.returncode = -1
  worker.wait = wait
  monkeypatch.setattr(binding, "terminate_process", terminate)
  jobs = binding.run_probe_jobs([{"request_id": "case"}], binary_path="unused")
  next(jobs)
  with pytest.raises(RuntimeError, match="did not exit"):
    next(jobs)
  assert actions.count("wait") == 2 and actions.count("signal") == 1
  assert worker.stdout.closed


def test_idle_client_does_not_retain_completed_input_or_response(monkeypatch):
  worker_fixture(monkeypatch)
  class Payload(dict):
    pass
  input_payload = Payload(large=[0] * 1000)
  input_ref = weakref.ref(input_payload)
  original_loads = binding.json.loads
  output_refs = []
  def decode(line):
    response = original_loads(line)
    response['result'] = Payload(response['result'])
    output_refs.append(weakref.ref(response['result']))
    return response
  monkeypatch.setattr(binding.json, 'loads', decode)
  with binding.ProbeClient('unused') as client:
    result = client.run({'request_id': 'case', 'inline_payload': input_payload})
    assert result == {'version': 1}
    del input_payload, result
    gc.collect()
    assert input_ref() is None
    assert output_refs[0]() is None


def test_worker_start_failure_releases_queued_request(monkeypatch):
  def fail(*args, **kwargs):
    raise FileNotFoundError('missing native worker')
  monkeypatch.setattr(binding.subprocess, 'Popen', fail)
  client = binding.ProbeClient('unused')
  with pytest.raises(FileNotFoundError, match='missing native'):
    client.run({'inline_payload': {'large': [0] * 1000}})
  assert not client._pending
  with pytest.raises(RuntimeError, match='closed'):
    client.run({})
  client.close()
