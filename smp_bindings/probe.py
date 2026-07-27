"""Python bindings for the Go ``smp-probe`` counterfactual evaluator.

Python reconstructs frozen network/opinion/post states from simulation output.
All recommendation and force evaluation remains in Go.
"""

from __future__ import annotations

import os
import subprocess
from dataclasses import dataclass
from typing import Any, Dict, Iterable, List, Mapping, Optional, Sequence, TYPE_CHECKING

import msgpack
import networkx as nx

from smp_bindings.events_db import (
  PostEventBody,
  PostRecord,
  batch_load_event_bodies,
  get_events_by_step_range,
)

if TYPE_CHECKING:
  from smp_bindings.record import RawSimulationRecord


@dataclass(frozen=True)
class FrozenSimulationState:
  """Minimal input needed by the Go probe evaluator at one completed step."""

  step: int
  graph: nx.DiGraph
  opinions: Sequence[float]
  posts: Mapping[int, Sequence[PostRecord]]

  def to_wire(self) -> Dict[str, Any]:
    return {
        "step": int(self.step),
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


def _graph_to_wire(graph: nx.DiGraph) -> Dict[str, Any]:
  if not graph.is_directed():
    raise ValueError("smp-probe requires a directed graph")
  nodes = {
      int(node): dict(attributes)
      for node, attributes in graph.nodes(data=True)
  }
  adjacency: Dict[int, Dict[int, Dict[str, Any]]] = {
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


def freeze_record(
    record: "RawSimulationRecord",
    steps: Iterable[int],
) -> List[FrozenSimulationState]:
  """Reconstruct post history and network snapshots for selected steps.

  Normal posts are implied by the accumulated opinion rows. Repost events are
  read from SQLite and replace the corresponding normal post. Therefore a run
  with nonzero ``RepostRate`` must have ``PostEvent`` collection enabled.
  """

  requested_steps = sorted(set(int(step) for step in steps))
  if not requested_steps:
    raise ValueError("at least one probe step is required")
  for step in requested_steps:
    if step < 0 or step > record.max_step:
      raise ValueError(
          f"invalid probe step {step}; available range is [0, {record.max_step}]"
      )

  dynamics_type = record.metadata.get("DynamicsType")
  if dynamics_type not in ("HK", "Deffuant"):
    raise ValueError(
        f"smp-probe supports HK and Deffuant, got {dynamics_type!r}"
    )
  params_value = record.metadata.get(f"{dynamics_type}Params")
  params = params_value if isinstance(params_value, Mapping) else {}
  repost_rate = float(params.get("RepostRate", 0.0))
  post_events_enabled = bool(record.metadata.get("PostEvent", False))
  max_requested = requested_steps[-1]
  if max_requested > 0 and repost_rate > 0 and not post_events_enabled:
    raise ValueError(
        "cannot reconstruct posts: RepostRate is nonzero but PostEvent "
        "collection was disabled"
    )

  rewiring_rate = float(params.get("RewiringRate", 0.0))
  rewiring_events_enabled = bool(record.metadata.get("RewiringEvent", False))
  graph_paths = getattr(record, "graph_paths", {})
  missing_exact_graphs = [
      step for step in requested_steps if step not in graph_paths
  ]
  if (
      rewiring_rate > 0
      and not rewiring_events_enabled
      and missing_exact_graphs
  ):
    raise ValueError(
        "cannot reconstruct networks at steps "
        f"{missing_exact_graphs}: RewiringRate is nonzero but "
        "RewiringEvent collection was disabled and no exact graph dump exists"
    )

  reposts_by_step: Dict[int, Dict[int, PostRecord]] = {}
  if max_requested > 0 and post_events_enabled:
    events = get_events_by_step_range(
        record.events_db,
        1,
        max_requested + 1,
        type_="Post",
    )
    batch_load_event_bodies(record.events_db, events, event_type="Post")
    for event in events:
      if not isinstance(event.body, PostEventBody) or not event.body.is_repost:
        continue
      step_events = reposts_by_step.setdefault(int(event.step), {})
      if event.agent_id in step_events:
        raise ValueError(
            f"multiple Post events for agent {event.agent_id} at step {event.step}"
        )
      step_events[int(event.agent_id)] = event.body.record

  retain_count = max(int(record.metadata.get("PostRetainCount", 0)), 1)
  posts: Dict[int, List[PostRecord]] = {
      agent_id: [
          PostRecord(
              agent_id=agent_id,
              step=-1,
              opinion=float(record.opinions[0, agent_id]),
          )
      ]
      for agent_id in range(record.agents)
  }

  states: List[FrozenSimulationState] = []
  requested = set(requested_steps)

  def append_state(step: int) -> None:
    states.append(FrozenSimulationState(
        step=step,
        graph=record.get_graph(step).copy(),
        opinions=[float(value) for value in record.opinions[step]],
        posts={
            agent_id: list(records)
            for agent_id, records in posts.items()
        },
    ))

  if 0 in requested:
    append_state(0)
  for step in range(1, max_requested + 1):
    step_reposts = reposts_by_step.get(step, {})
    for agent_id in range(record.agents):
      post = step_reposts.get(agent_id)
      if post is None:
        post = PostRecord(
            agent_id=agent_id,
            step=step,
            opinion=float(record.opinions[step, agent_id]),
        )
      history = posts[agent_id]
      history.append(post)
      if len(history) > retain_count:
        del history[:-retain_count]
    if step in requested:
      append_state(step)

  return states


def run_probe(
    states: Sequence[FrozenSimulationState],
    metadata: Mapping[str, Any],
    *,
    h: float,
    minimum: float = -1.0,
    maximum: float = 1.0,
    replicates: int = 1,
    anchor_ids: Optional[Sequence[int]] = None,
    rng: Optional[Mapping[str, str]] = None,
    binary_path: Optional[str] = None,
) -> Dict[str, Any]:
  """Run the Go evaluator and return its decoded response."""

  if not states:
    raise ValueError("at least one frozen state is required")
  dynamics_type = str(metadata.get("DynamicsType", ""))
  if dynamics_type not in ("HK", "Deffuant"):
    raise ValueError(
        f"smp-probe supports HK and Deffuant, got {dynamics_type!r}"
    )
  if replicates <= 0:
    raise ValueError("replicates must be positive")

  if rng is None:
    metadata_rng = metadata.get("RNG")
    rng = metadata_rng if isinstance(metadata_rng, Mapping) else {}

  request = {
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
      "anchor_ids": [int(agent_id) for agent_id in (anchor_ids or [])],
      "states": [state.to_wire() for state in states],
  }
  encoded = msgpack.packb(request, use_bin_type=True)

  executable = (
      binary_path
      or os.environ.get("SMP_PROBE_BINARY")
      or "./smp-probe"
  )
  completed = subprocess.run(
      [os.fspath(executable)],
      input=encoded,
      stdout=subprocess.PIPE,
      stderr=subprocess.PIPE,
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
    raise RuntimeError("smp-probe returned a non-object response")
  return response
