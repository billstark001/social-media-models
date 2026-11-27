"""The binding forwards native sources and jobs without rebuilding states."""

import hashlib
import json
import os
import sqlite3
import struct
import subprocess
from contextlib import closing
from pathlib import Path

import lz4.frame
import msgpack
import networkx as nx
import pytest

from smp_bindings.probe import (
    FrozenSimulationState,
    run_probe,
    run_probe_jobs,
    run_probe_run,
)
from smp_bindings.record import RawSimulationRecord
from smp_bindings.domain import parse_metadata, to_wire


@pytest.fixture(scope="module")
def probe_binary(tmp_path_factory):
  configured = os.environ.get("SMP_PROBE_TEST_BINARY")
  if configured:
    return configured
  binary = tmp_path_factory.mktemp("native-probe") / "smp-probe"
  subprocess.run(["go", "build", "-o", str(binary), "./cmd/smp-probe"], cwd=Path(__file__).resolve().parents[1], check=True)
  return str(binary)


@pytest.fixture
def legacy_run(tmp_path):
  metadata = {
      "DynamicsType": "HK", "UniqueName": tmp_path.name, "NodeCount": 2,
      "HKParams": {"Tolerance": 0.45, "Influence": 0.1, "RewiringRate": 0, "RepostRate": 0},
      "PostRetainCount": 2, "RecsysCount": 1, "RecsysFactoryType": "Random",
      "RNG": {"Algorithm": "pcg64-dxsm-v1", "Seed1": "0x1", "Seed2": "0x2"},
  }
  (tmp_path / "metadata.json").write_text(json.dumps(metadata))
  opinions = (-0.5, 0.5, -0.4, 0.4, -0.3, 0.3)
  payload = struct.pack("<ii6f", 3, 2, *opinions) + bytes(3 * 2 * (8 + 16))
  (tmp_path / "acc-state-2.lz4").write_bytes(lz4.frame.compress(payload))
  graph = {
      "directed": True, "nodes": {0: {}, 1: {}},
      "adjacency": {0: {1: {}}, 1: {0: {}}}, "graph": {},
  }
  (tmp_path / "graph-0.msgpack").write_bytes(msgpack.packb(graph, use_bin_type=True))
  return tmp_path


def test_native_run_source_without_python_freeze(probe_binary, legacy_run):
  result = run_probe_run(
      legacy_run, [0, 2], measurements=["energy", "pathway", "bins", "terminal"],
      bins=[-1, 0, 1], binary_path=probe_binary,
  )
  assert [row["step"] for row in result["results"]] == [0, 2]
  assert all(row["precision"] == "history_f32" for row in result["results"])
  assert all(row["pathway"]["separation"] == 1 for row in result["results"])
  assert all(row["terminal"] is not None and row["binned"] is not None for row in result["results"])
  force = run_probe_run(legacy_run, [2], measurements=["force"], h=1, binary_path=probe_binary)
  assert len(force["results"][0]["points"]) == 3
  record = RawSimulationRecord(str(legacy_run.parent), parse_metadata({"UniqueName": legacy_run.name}))
  static = record.evaluate_probe([2], measurements=["pathway"], binary_path=probe_binary)
  assert to_wire(static.results[0].pathway) == result["results"][-1]["pathway"]


def test_independent_native_jobs_share_one_transport(probe_binary, legacy_run):
  requests = [
      {"request_id": "terminal", "states": [{"step": 0, "opinions": [0, 0]}], "measurements": ["terminal"], "terminal": {"epsilon": 0, "major_mass": 0.02}},
      {"request_id": "run", "run_directory": str(legacy_run), "steps": [2], "measurements": ["pathway"]},
  ]
  results = list(run_probe_jobs(requests, binary_path=probe_binary))
  assert results[0]["results"][0]["terminal"]["k_major"] == 1
  assert results[1]["results"][0]["pathway"]["separation"] == 1
  with pytest.raises(RuntimeError, match="unknown measurement"):
    list(run_probe_jobs([{"states": [{"opinions": [0]}], "measurements": ["old_alias"]}], binary_path=probe_binary))


def test_static_frozen_binding_does_not_require_dynamics(probe_binary):
  state = FrozenSimulationState(step=0, graph=nx.DiGraph(), opinions=[0, 0], posts={})
  result = run_probe([state], measurements=["terminal"], epsilon=0, binary_path=probe_binary)
  assert result["results"][0]["terminal"]["k_major"] == 1


def test_explicit_archived_metadata_keeps_native_run_read_only(probe_binary, legacy_run):
  metadata_path = legacy_run / "metadata.json"
  metadata = json.loads(metadata_path.read_text())
  with pytest.raises(RuntimeError, match="metadata"):
    run_probe_run(legacy_run, [2], measurements=["pathway"], metadata=metadata, binary_path=probe_binary)
  metadata_path.unlink()
  result = run_probe_run(legacy_run, [2], measurements=["force", "pathway"], metadata=metadata, h=1, binary_path=probe_binary)
  assert result["source_max_step"] == 2
  assert result["results"][0]["pathway"]["separation"] == 1
  assert len(result["results"][0]["points"]) == 3
  record = RawSimulationRecord(str(legacy_run.parent), parse_metadata({**metadata, "UniqueName": legacy_run.name}))
  observed = record.evaluate_probe([2], measurements=["pathway"], binary_path=probe_binary)
  assert to_wire(observed.results[0].pathway) == result["results"][0]["pathway"]
  assert not metadata_path.exists()


def test_native_jobs_allow_early_first_passage_stop(probe_binary):
  def requests():
    yield {"states": [{"step": 0, "opinions": [0, 0]}], "measurements": ["terminal"], "epsilon": 0}
    raise AssertionError("the worker consumed a source after early stop")

  with closing(run_probe_jobs(requests(), binary_path=probe_binary)) as results:
    assert next(results)["results"][0]["terminal"]["k_major"] == 1


def test_multistep_legacy_static_run_matches_python_graphs_and_native_jobs(probe_binary, legacy_run, monkeypatch):
  # Historical metadata omitted these fields; explicit HKParams still establish
  # that no rewiring replay is required. The native reader must not write defaults.
  metadata = {"UniqueName": legacy_run.name, "HKParams": {"Tolerance": .45, "RewiringRate": 0}, "AgentNumber": True}
  (legacy_run / "metadata.json").write_text(json.dumps(metadata))
  (legacy_run / "finished").touch()
  with sqlite3.connect(legacy_run / "events.db"):
    pass
  def hashes():
    return {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in legacy_run.iterdir()}
  before = hashes()
  steps = [0, 1, 2]
  with RawSimulationRecord(str(legacy_run.parent), parse_metadata({"UniqueName": legacy_run.name})) as record:
    states = [FrozenSimulationState(step=step, graph=graph.copy(), opinions=record.opinions[step].copy(),
                                    posts={}, precision="history_f32") for step, graph in record.iter_graphs(steps)]
  options = {"measurements": ["energy", "pathway", "bins", "terminal"], "epsilon": .45,
             "bins": [-1, 0, 1], "binary_path": probe_binary}
  explicit = run_probe(states, {"DynamicsType": "HK"}, **options)

  def forbidden(*args, **kwargs):
    pytest.fail("saved-run static measurement must not reconstruct Python graphs or inline state payloads")
  monkeypatch.setattr(RawSimulationRecord, "iter_graphs", forbidden)
  monkeypatch.setattr(FrozenSimulationState, "to_wire", forbidden)
  native = run_probe_run(legacy_run, steps, **options)
  assert native["source_max_step"] == 2
  assert native["version"] == 1 and native["dynamics_type"] == "HK"
  assert native["results"] == explicit["results"]
  assert all(row["precision"] == "history_f32" for row in native["results"])
  request = {"request_id": "whole-run", "run_directory": str(legacy_run), "steps": steps,
             "measurements": options["measurements"], "epsilon": .45, "bins": [-1, 0, 1]}
  assert list(run_probe_jobs([request, {**request, "request_id": "repeat-run"}], binary_path=probe_binary)) == [native, native]
  assert hashes() == before
