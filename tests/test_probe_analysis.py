"""Native lifecycle analysis keeps dense observations and isolated job state."""

import json
import subprocess
import io
from types import SimpleNamespace

import networkx as nx
import pytest

import smp_bindings.probe as binding
from smp_bindings.domain import ProbeResponse, parse_dataclass, parse_metadata, to_wire
from smp_bindings.record import RawSimulationRecord
from probe_analysis_fixtures import analysis_run, probe_binary


def test_analysis_helpers_forward_complete_options_and_fixed_cohorts(monkeypatch):
  sent = []
  monkeypatch.setattr(binding, "_send_probe_request", lambda request, binary: sent.append(request) or request)
  cohorts = ((0, 1), (2, 3))
  group_opts, bridge_opts = {"complete": "group-options"}, {"complete": "bridge-options"}
  state = binding.FrozenSimulationState(0, nx.DiGraph(), [0], {})
  binding.run_probe([state], measurements=["groups"], groups=group_opts, bridges=bridge_opts, bridge_cores=cohorts)
  binding.run_probe_checkpoints(["checkpoint.msgpack"], measurements=["bridges"], groups=group_opts, bridges=bridge_opts, bridge_cores=cohorts)
  binding.run_probe_run("saved", measurements=["bridges"], groups=group_opts, bridges=bridge_opts, bridge_cores=cohorts)
  binding.analyze_run("saved", groups=group_opts, bridges=bridge_opts, bridge_cores=cohorts,
                      start=2, end=8, output_stride=5, collection=binding.CollectionSpec(),
                      output=binding.OutputSpec("output", metadata={"run_id": "example"}),
                      source_compatibility="legacy_header_actor")
  assert all(request["groups"] == group_opts and request["bridges"] == bridge_opts
             and request["bridge_cores"] == [[0, 1], [2, 3]] for request in sent)
  assert sent[2]["steps"] == [] and sent[2]["scan"] is None
  assert sent[3]["scan"] == {"start": 2, "end": 8, "output_stride": 5}
  assert sent[3]["collection"] == {"profile": "plot"}
  assert sent[3]["output"]["metadata"] == {"run_id": "example"}
  assert sent[3]["output"]["archive_native"] is False
  assert sent[3]["source_compatibility"] == "legacy_header_actor"


@pytest.mark.parametrize("error", [False, True])
def test_job_cleanup_sends_eof_before_wait_and_avoids_signal(monkeypatch, error):
  actions = []
  class Input(io.StringIO):
    def close(self):
      actions.append("eof")
      super().close()
  reply = {"line": 1, "request_id": "case", "status": "error" if error else "ok"}
  reply.update({"error": "bad job"} if error else {"result": {"version": 1}})
  worker = SimpleNamespace(stdin=Input(), stdout=io.StringIO(json.dumps(reply) + "\n"), returncode=None)
  worker.poll = lambda: worker.returncode
  def wait(timeout=None):
    assert worker.stdin.closed
    actions.append("wait")
    worker.returncode = 0
    return 0
  worker.wait = wait
  monkeypatch.setattr(binding.subprocess, "Popen", lambda *args, **kwargs: worker)
  monkeypatch.setattr(binding, "terminate_process", lambda *args, **kwargs: pytest.fail("EOF-ready worker was signalled"))
  jobs = binding.run_probe_jobs([{"request_id": "case"}])
  if error:
    with pytest.raises(RuntimeError, match="bad job"):
      next(jobs)
  else:
    assert next(jobs) == {"version": 1}
    jobs.close()
  assert actions == ["eof", "wait"]
  assert worker.stdout.closed


def test_job_cleanup_reaps_worker_that_does_not_exit_on_eof(monkeypatch):
  reply = {"line": 1, "request_id": "case", "status": "ok", "result": {"version": 1}}
  worker = SimpleNamespace(stdin=io.StringIO(), stdout=io.StringIO(json.dumps(reply) + "\n"), returncode=None)
  worker.poll = lambda: worker.returncode
  actions = []
  def wait(timeout):
    assert worker.stdin.closed and timeout == 1
    actions.append("wait")
    raise subprocess.TimeoutExpired("worker", timeout)
  def terminate(process, *, process_group):
    assert process is worker and process_group and worker.stdin.closed
    actions.append("terminate")
    worker.returncode = -1
  worker.wait = wait
  monkeypatch.setattr(binding.subprocess, "Popen", lambda *args, **kwargs: worker)
  monkeypatch.setattr(binding, "terminate_process", terminate)
  jobs = binding.run_probe_jobs([{"request_id": "case"}])
  next(jobs)
  jobs.close()
  assert actions == ["wait", "terminate"] and worker.stdout.closed


def test_native_analysis_thinning_typed_records_and_fixed_namespace(probe_binary, analysis_run):
  options = {"epsilon": .65, "detail_output": True}
  dense = binding.analyze_run(analysis_run, bridges=options, bridge_cores=[[0], [26]], output_stride=1, binary_path=probe_binary)
  thin = binding.analyze_run(analysis_run, bridges=options, bridge_cores=[[0], [26]], output_stride=3, binary_path=probe_binary)
  assert dense["analysis"] == thin["analysis"]
  assert [frame["step"] for frame in thin["results"]] == [0, 3, 6, 7]
  assert thin["analysis"]["observations"] == 8 and not thin["analysis"]["sampled"]
  assert thin["analysis"]["reference_mode"] == "fixed_cohorts"
  assert not thin["results"][0]["groups"]["groups"]
  assert thin["results"][0]["bridges"]["pairs"][0]["group_ids"] == [1, 2]
  assert thin["analysis"]["bridge_episodes"] and thin["analysis"]["bridge_events"]
  typed = parse_dataclass(ProbeResponse, thin)
  assert to_wire(typed) == thin
  record = RawSimulationRecord(str(analysis_run.parent), parse_metadata({"UniqueName": analysis_run.name}))
  observed = record.evaluate_probe(measurements=["groups", "bridges"], bridges=options,
                                   bridge_cores=[[0], [26]], scan={"output_stride": 3}, binary_path=probe_binary)
  assert to_wire(observed) == thin
  assert typed.results[-1].bridges.union is not None
  assert typed.analysis.epochs[0].core_members is not None


def test_probe_client_reuses_worker_with_isolated_jobs_and_closes(probe_binary, analysis_run, monkeypatch):
  popen = binding.subprocess.Popen
  starts = []
  def count_start(*args, **kwargs):
    process = popen(*args, **kwargs)
    starts.append(process)
    return process
  monkeypatch.setattr(binding.subprocess, "Popen", count_start)
  with binding.ProbeClient(probe_binary) as client:
    first = binding.analyze_run(analysis_run, epsilon=.65, client=client)
    second = binding.analyze_run(analysis_run, epsilon=.65, client=client)
    assert first == second
    assert first["analysis"]["group_events"][0]["group_ids"] == [1]
    assert len(starts) == 1
  assert starts[0].poll() is not None
  with pytest.raises(RuntimeError, match="closed"):
    client.run({})
  client.close()
  with binding.ProbeClient(probe_binary) as client:
    with pytest.raises(RuntimeError, match="unknown measurement"):
      client.run({"measurements": ["no_such_measurement"]})
    with pytest.raises(RuntimeError, match="closed"):
      client.run({})
  assert all(process.poll() is not None for process in starts)


def test_native_analysis_options_and_missing_posts(probe_binary, analysis_run):
  with pytest.raises(RuntimeError, match="groups"):
    binding.analyze_run(analysis_run, groups={"epsilon": .65}, binary_path=probe_binary)
  with pytest.raises(RuntimeError, match="distinct"):
    binding.analyze_run(analysis_run, epsilon=.65, bridge_cores=[[0], [0]], binary_path=probe_binary)
  path = analysis_run / "metadata.json"
  metadata = json.loads(path.read_text())
  metadata["HKParams"]["RepostRate"] = .5
  path.write_text(json.dumps(metadata))
  result = binding.analyze_run(analysis_run, epsilon=.65, bridge_cores=[[0], [26]],
                               bridges={"epsilon": .65, "detail_output": True}, binary_path=probe_binary)
  assert result["analysis"]["posts_unavailable_reason"]
  holder = result["results"][-1]["bridges"]["pairs"][0]["holders"][52]
  assert holder["o"][0]["status"]["known"] and holder["o"][0]["status"]["active"]
  assert not holder["p"][0]["source_known"] and not holder["p"][0]["status"]["known"]
  assert to_wire(parse_dataclass(ProbeResponse, result)) == result


def test_cli_scan_keeps_dense_events_and_omitted_steps_remain_last(probe_binary, analysis_run):
  def run(*extra):
    return json.loads(subprocess.run([probe_binary, "--run-directory", str(analysis_run),
                                     "--measurements", "groups,bridges", *extra],
                                    check=True, capture_output=True, text=True).stdout)
  last = run()
  dense = run("--scan", "--output-stride", "1")
  thin = run("--scan", "--output-stride", "4", "--scan-end", "6")
  assert [row["step"] for row in last["results"]] == [7]
  assert dense["analysis"]["observations"] == 8
  assert thin["analysis"]["observations"] == 7
  assert [row["step"] for row in thin["results"]] == [0, 4, 6]
  closed = [epoch for epoch in thin["analysis"]["epochs"] if epoch["state"] == "closed"]
  assert closed and all(epoch["reason"] == "analysis_horizon" for epoch in closed)
