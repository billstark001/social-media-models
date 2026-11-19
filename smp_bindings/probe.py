"""Python bindings for the Go ``smp-probe`` counterfactual evaluator.

Go reads persistent runs and checkpoints directly. Explicit frozen states remain
available for synthetic inputs. All measurement and recommendation logic is Go.
"""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
from collections.abc import Iterable, Iterator, Mapping, Sequence
from dataclasses import dataclass
from typing import Any

import msgpack
import networkx as nx

from smp_bindings.process import process_group_kwargs, terminate_process

from smp_bindings.events_db import PostRecord


@dataclass(frozen=True)
class FrozenSimulationState:
  """Minimal input needed by the Go probe evaluator at one completed step."""

  step: int
  graph: nx.DiGraph
  opinions: Sequence[float]
  posts: Mapping[int, Sequence[PostRecord]]
  precision: str = "inline_f64"

  def to_wire(self) -> dict[str, Any]:
    return {
        "step": int(self.step),
        "precision": self.precision,
        "graph": _graph_to_wire(self.graph),
        "opinions": [float(value) for value in self.opinions],
        "posts": {
            int(agent_id): [
                {
                    "AgentID": int(post.agent_id),
                    "Step": int(post.step),
                    "Opinion": float(post.opinion),
                }
                for post in records
            ]
            for agent_id, records in self.posts.items()
        },
    }


def _graph_to_wire(graph: nx.DiGraph) -> dict[str, Any]:
  if not graph.is_directed():
    raise ValueError("smp-probe requires a directed graph")
  nodes = {
      int(node): dict(attributes)
      for node, attributes in graph.nodes(data=True)
  }
  adjacency: dict[int, dict[int, dict[str, Any]]] = {
      int(node): {} for node in graph.nodes
  }
  for source, target, attributes in graph.edges(data=True):
    adjacency[int(source)][int(target)] = dict(attributes)
  return {
      "adjacency": adjacency,
      "directed": True,
      "nodes": nodes,
      "graph": dict(graph.graph),
  }


def run_probe(
    states: Sequence[FrozenSimulationState],
    metadata: Mapping[str, Any] | None = None,
    *,
    h: float = 0.02,
    minimum: float = -1.0,
    maximum: float = 1.0,
    replicates: int = 1,
    anchor_ids: Sequence[int] | None = None,
    rng: Mapping[str, str] | None = None,
    binary_path: str | None = None,
    measurements: Sequence[str] | None = None,
    energy_scale: float = 1.0,
    energy_epsilon: float | None = None,
    per_anchor: bool = False,
    epsilon: float | None = None,
    bins: Sequence[float] | None = None,
    terminal: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
  """Run the Go evaluator and return its decoded response."""

  if not states:
    raise ValueError("at least one frozen state is required")
  metadata = metadata or {}
  dynamics_type = str(metadata.get("DynamicsType", ""))

  if rng is None:
    metadata_rng = metadata.get("RNG")
    rng = metadata_rng if isinstance(metadata_rng, Mapping) else {}

  request = {
      "version": 1,
      "rng": dict(rng),
      "dynamics_type": dynamics_type,
      "hk_params": (
          dict(metadata["HKParams"])
          if isinstance(metadata.get("HKParams"), Mapping)
          else {}
      ),
      "deffuant_params": (
          dict(metadata["DeffuantParams"])
          if isinstance(metadata.get("DeffuantParams"), Mapping)
          else {}
      ),
      "recsys_factory_type": metadata.get("RecsysFactoryType") or "Random",
      "recsys_params": (
          dict(metadata["RecSysParams"])
          if isinstance(metadata.get("RecSysParams"), Mapping)
          else {}
      ),
      "recsys_count": int(metadata.get("RecsysCount", 10)),
      "post_retain_count": int(metadata.get("PostRetainCount", 3)),
      "grid": {
          "min": float(minimum),
          "max": float(maximum),
          "step": float(h),
      },
      "replicates": int(replicates),
      "anchor_ids": [int(agent_id) for agent_id in ([] if anchor_ids is None else anchor_ids)],
      "states": [state.to_wire() for state in states],
      "measurements": list(measurements) if measurements is not None else [],
      "energy_scale": float(energy_scale),
      "energy_epsilon": energy_epsilon,
      "per_anchor": per_anchor,
      "epsilon": epsilon,
      "bins": [] if bins is None else list(bins),
      "terminal": dict(terminal) if terminal is not None else None,
  }
  return _send_probe_request(request, binary_path)


def run_probe_checkpoints(
    checkpoint_paths: Sequence[str],
    *,
    measurements: Sequence[str],
    h: float = 0.02,
    minimum: float = -1.0,
    maximum: float = 1.0,
    replicates: int = 1,
    anchor_ids: Sequence[int] | None = None,
    energy_scale: float = 1.0,
    energy_epsilon: float | None = None,
    per_anchor: bool = False,
    binary_path: str | None = None,
    epsilon: float | None = None,
    bins: Sequence[float] | None = None,
    terminal: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
  """Measure exact float64 checkpoints directly in Go without Python state copies."""
  if not checkpoint_paths:
    raise ValueError("at least one checkpoint is required")
  request = {
      "version": 1,
      "checkpoint_paths": [os.fspath(path) for path in checkpoint_paths],
      "measurements": list(measurements),
      "grid": {"min": minimum, "max": maximum, "step": h},
      "replicates": replicates,
      "anchor_ids": [int(value) for value in ([] if anchor_ids is None else anchor_ids)],
      "energy_scale": energy_scale,
      "energy_epsilon": energy_epsilon,
      "per_anchor": per_anchor,
      "epsilon": epsilon,
      "bins": [] if bins is None else list(bins),
      "terminal": dict(terminal) if terminal is not None else None,
  }
  return _send_probe_request(request, binary_path)


def run_probe_run(
    directory: str | os.PathLike[str],
    steps: Sequence[int] | None = None,
    *,
    measurements: Sequence[str],
    metadata: Mapping[str, Any] | None = None,
    epsilon: float | None = None,
    bins: Sequence[float] | None = None,
    terminal: Mapping[str, Any] | None = None,
    h: float = 0.02,
    minimum: float = -1.0,
    maximum: float = 1.0,
    replicates: int = 1,
    anchor_ids: Sequence[int] | None = None,
    rng: Mapping[str, str] | None = None,
    energy_scale: float = 1.0,
    energy_epsilon: float | None = None,
    per_anchor: bool = False,
    binary_path: str | None = None,
) -> dict[str, Any]:
  """Measure a saved run in Go; omitted steps select its last recorded state."""
  request = {
      "version": 1,
      "run_directory": os.fspath(directory),
      "steps": [] if steps is None else list(steps),
      "measurements": list(measurements),
      "epsilon": epsilon,
      "bins": [] if bins is None else list(bins),
      "terminal": dict(terminal) if terminal is not None else None,
      "grid": {"min": minimum, "max": maximum, "step": h},
      "replicates": replicates,
      "anchor_ids": [] if anchor_ids is None else list(anchor_ids),
      "rng": dict(rng or {}),
      "energy_scale": energy_scale,
      "energy_epsilon": energy_epsilon,
      "per_anchor": per_anchor,
  }
  if metadata is not None:
    request["run_metadata"] = dict(metadata)
  return _send_probe_request(request, binary_path)


def run_probe_jobs(
    requests: Iterable[Mapping[str, Any]],
    *,
    binary_path: str | None = None,
) -> Iterator[dict[str, Any]]:
  """Evaluate independent native requests through one sequential JSONL worker.

  Each request can select inline states, a run directory, or checkpoints. Jobs
  own their probe RNG; states within one job share the same RNG lifecycle.
  """
  executable = binary_path or os.environ.get("SMP_PROBE_BINARY") or "./smp-probe"
  with tempfile.TemporaryFile(mode="w+", encoding="utf8") as diagnostics:
    process = subprocess.Popen(
        [os.fspath(executable), "--format", "jsonl"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=diagnostics,
        text=True, encoding="utf8", bufsize=1, **process_group_kwargs(),
    )
    try:
      assert process.stdin is not None and process.stdout is not None
      for index, original in enumerate(requests, 1):
        request = dict(original)
        request.setdefault("version", 1)
        request.setdefault("request_id", f"probe-{index}")
        request_id = request["request_id"]
        if not isinstance(request_id, str) or not request_id.strip():
          raise ValueError("probe request_id must be a non-empty string")
        process.stdin.write(json.dumps(request, allow_nan=False, separators=(",", ":")) + "\n")
        process.stdin.flush()
        line = process.stdout.readline()
        if not line:
          diagnostics.seek(0)
          raise RuntimeError(f"smp-probe ended before job {index}: {diagnostics.read().strip()}")
        response = json.loads(line)
        if not isinstance(response, dict) or type(response.get("line")) is not int or response["line"] != index:
          raise RuntimeError(f"smp-probe returned an uncorrelated reply for job {index}")
        response_id = response.get("request_id")
        if response.get("status") == "error":
          if response_id not in (None, "") and response_id != request_id:
            raise RuntimeError(f"smp-probe returned an uncorrelated reply for job {index}")
          raise RuntimeError(f"smp-probe job {index}: {response.get('error', 'invalid response')}")
        if response.get("status") != "ok":
          raise RuntimeError(f"smp-probe job {index}: invalid response status {response.get('status')!r}")
        if response_id != request_id:
          raise RuntimeError(f"smp-probe returned an uncorrelated reply for job {index}")
        result = response.get("result")
        if not isinstance(result, dict) or result.get("version") != 1:
          raise RuntimeError("smp-probe returned an invalid result")
        yield result
      process.stdin.close()
      if process.wait() != 0:
        diagnostics.seek(0)
        raise RuntimeError(f"smp-probe failed: {diagnostics.read().strip()}")
    finally:
      terminate_process(process, process_group=True)
      if process.stdin is not None and not process.stdin.closed:
        process.stdin.close()
      if process.stdout is not None:
        process.stdout.close()


def _send_probe_request(request: Mapping[str, Any], binary_path: str | None) -> dict[str, Any]:
  encoded = msgpack.packb(request, use_bin_type=True)

  executable = (
      binary_path
      or os.environ.get("SMP_PROBE_BINARY")
      or "./smp-probe"
  )
  completed = subprocess.run(
      [os.fspath(executable)],
      input=encoded,
      capture_output=True,
      check=False,
  )
  if completed.returncode != 0:
    diagnostic = completed.stderr.decode("utf-8", errors="replace").strip()
    raise RuntimeError(
        f"smp-probe failed with exit code {completed.returncode}"
        + (f": {diagnostic}" if diagnostic else "")
    )
  try:
    response = msgpack.unpackb(
        completed.stdout,
        raw=False,
        strict_map_key=False,
    )
  except Exception as exc:
    raise RuntimeError("smp-probe returned invalid msgpack") from exc
  if not isinstance(response, dict):
    raise TypeError("smp-probe returned a non-object response")
  if response.get("version") != 1:
    raise RuntimeError(
        f"smp-probe returned unsupported protocol version {response.get('version', 0)!r}"
    )
  return response
