import json
import os
import re
import sqlite3
from bisect import bisect_left, bisect_right, insort
from collections.abc import Iterable, Mapping, Sequence
from datetime import datetime, timezone
from smp_bindings.domain import AccumulatedState, ScenarioMetadata, ProbeResponse, parse_metadata, parse_dataclass, to_wire
from typing import Any

import networkx as nx
import numpy as np

from smp_bindings.events_db import iter_rewiring_events, iter_view_posts, load_events_db
from smp_bindings.model_state import (
  load_accumulative_model_state,
  load_gonum_graph_dump,
)

re_graph = re.compile(r'graph-(\d+)\.msgpack(?:\.lz4)?$')
re_acc_state = re.compile(r'acc-state-(\d+|\d{4}-\d{2}-\d{2}T\d{6}Z)\.lz4$')


def _accumulated_state_files(file_list):
  """Order one explicit filename scheme by step or by its UTC timestamp."""
  records = []
  schemes = set()
  for name in file_list:
    if not (name.startswith("acc-state-") and name.endswith(".lz4")):
      continue
    match = re_acc_state.fullmatch(name)
    if match is None:
      raise ValueError(f"unsupported accumulated-state filename: {name}")
    stamp = match.group(1)
    if stamp.isdigit():
      schemes.add("step")
      key = int(stamp)
    else:
      schemes.add("utc")
      key = datetime.strptime(stamp, "%Y-%m-%dT%H%M%SZ").replace(tzinfo=timezone.utc)
    records.append((key, name))
  if len(schemes) > 1:
    raise ValueError("mixed step and UTC accumulated-state filenames have no common ordering")
  if len({key for key, _ in records}) != len(records):
    raise ValueError("multiple accumulated-state files have the same generation")
  return sorted(records)


class RawSimulationRecord:

  def __init__(self, base_dir: str, metadata: ScenarioMetadata):
    self.unique_name = metadata.UniqueName
    full_path = os.path.join(base_dir, self.unique_name)
    self.directory = full_path
    self.events_db: sqlite3.Connection | None = None
    self.graph_paths: dict[int, str] = {}
    self.max_step = 0
    persisted_metadata_path = os.path.join(full_path, "metadata.json")
    resolved_metadata = metadata
    if os.path.isfile(persisted_metadata_path):
      with open(persisted_metadata_path, "r", encoding="utf-8") as metadata_file:
        persisted_metadata = parse_metadata(json.load(metadata_file))
      if persisted_metadata.UniqueName != self.unique_name:
        raise ValueError("persisted metadata UniqueName differs from the requested run directory")
      resolved_metadata = persisted_metadata
    self.metadata = resolved_metadata

    file_list = os.listdir(full_path)
    file_list.sort()
    self.is_finished = any(x for x in file_list if x.startswith('finished'))
    has_events_db = 'events.db' in file_list
    self.trajectory_path = os.path.join(full_path, 'trajectory.json')
    self.has_trajectory = 'trajectory.json' in file_list
    acc_state_list = _accumulated_state_files(file_list)
    graph_list = [x for x in file_list if re_graph.fullmatch(x)]

    self.is_sanitized = has_events_db and (self.has_trajectory or len(
        acc_state_list) > 0) and len(graph_list) > 0
    if not self.is_finished or not self.is_sanitized:
      return

    self.events_db_path = os.path.join(full_path, 'events.db')
    self.acc_state_path = os.path.join(full_path, acc_state_list[-1][1]) if acc_state_list else None
    for graph_name in graph_list:
      step_index = int(re_graph.match(graph_name).group(1))  # type: ignore
      if step_index in self.graph_paths:
        raise ValueError(f"multiple graph dumps at step {step_index}")
      self.graph_paths[step_index] = os.path.join(full_path, graph_name)

  def _load_graph(self, step: int) -> nx.DiGraph:
    graph = load_gonum_graph_dump(self.graph_paths[step], check_sanity=False)
    if not graph.is_directed() or any(not isinstance(node, int) or node < 0 or node >= self.agents for node in graph):
      raise ValueError("graph nodes must belong to the recorded agent population")
    # Older graph dumps omitted isolated agents; the opinion channel gives N.
    graph.add_nodes_from(range(self.agents))
    return graph

  def _get_stored_graph(self, step: int) -> nx.DiGraph:
    """Load and cache one stored graph on demand."""
    if step not in self.graphs_stored:
      self.graphs_stored[step] = self._load_graph(step)
    return self.graphs_stored[step]

  def load(self):
    if not self.is_finished or not self.is_sanitized:
      raise ValueError("raw simulation record is incomplete")
    self.dispose()
    if self.has_trajectory:
      from smp_bindings.trajectory import TrajectoryReader
      self.trajectory = TrajectoryReader(os.path.dirname(self.trajectory_path))
      acc_state = AccumulatedState(
          steps=self.trajectory.steps - 1, agents=self.trajectory.agents,
          opinions=self.trajectory.channel('opinions'),
          agent_numbers=self.trajectory.channel('agent_numbers'),
          agent_opinion_sums=self.trajectory.channel('agent_opinion_sums'),
      )
    else:
      acc_state = load_accumulative_model_state(
        self.acc_state_path,
        with_agent_numbers=True,
        with_agent_opinion_sums=True,
      )
    declared_agents = self.metadata.NodeCount
    if declared_agents is not None and declared_agents != int(acc_state.agents):
      raise ValueError("metadata NodeCount differs from the raw state population")
    events_db = load_events_db(self.events_db_path)

    self.acc_state = acc_state
    self.events_db = events_db
    self.opinions = self.acc_state.opinions
    self.agent_numbers = self.acc_state.agent_numbers
    self.agent_opinion_sums = self.acc_state.agent_opinion_sums
    self.agents = int(self.acc_state.agents)
    self.max_step = int(acc_state.steps)

    try:
      if 0 not in self.graph_paths:
        raise ValueError("raw simulation record has no initial graph snapshot")
      g0 = self._get_stored_graph(0)
      self.followers = np.array([g0.out_degree(x) for x in range(self.agents)])
    except BaseException:
      self.dispose()
      raise

  def dispose(self):
    if self.events_db is not None:
      self.events_db.close()
      self.events_db = None
    self.graphs: dict[int, nx.DiGraph] = {}
    self.graphs_stored: dict[int, nx.DiGraph] = {}
    self.graph_steps = sorted(self.graph_paths)

  def __enter__(self):
    self.load()
    return self

  def __exit__(self, exc_type, exc_value, traceback):
    self.dispose()

  def get_graph(self, step: int):
    if step > self.max_step or step < 0:
      raise ValueError("invalid step")

    if step in self.graph_paths:
      return self._get_stored_graph(step)
    if step in self.graphs:
      return self.graphs[step]

    nearest_available_step_idx = bisect_left(self.graph_steps, step) - 1
    if nearest_available_step_idx < 0:
      raise ValueError('bad dump data (graph)')
    nearest_available_step = self.graph_steps[nearest_available_step_idx]
    nearest_available_graph: nx.DiGraph = (
        self._get_stored_graph(nearest_available_step)
        if nearest_available_step in self.graph_paths else
        self.graphs[nearest_available_step]
    ).copy()

    self._advance_graph(nearest_available_graph, nearest_available_step, step)

    self.graphs[step] = nearest_available_graph
    insort(self.graph_steps, step)

    return nearest_available_graph

  def _advance_graph(self, graph: nx.DiGraph, start: int, step: int) -> None:
    params = self.metadata.dynamics_parameters()
    if params is not None and params.RewiringRate == 0:
      return
    if self.metadata.RewiringEvent is not True:
      raise ValueError(
          f"cannot reconstruct graph at step {step}: rewiring-event collection "
          "is disabled or unknown and no exact graph snapshot is available"
      )
    if self.events_db is None:
      raise ValueError("raw simulation record is not loaded")
    # Legacy logs can include attempts blocked by an existing target edge.
    for event_id, agent_id, unfollow, follow in iter_rewiring_events(self.events_db, start + 1, step + 1):
      if graph.has_edge(agent_id, follow):
        continue
      if not graph.has_edge(agent_id, unfollow):
        raise ValueError(f"rewiring event {event_id} removes a missing edge")
      graph.remove_edge(agent_id, unfollow)
      graph.add_edge(agent_id, follow)

  def iter_graphs(self, steps: Iterable[int]):
    """Yield completed snapshots in increasing order without caching reconstructed graphs.

    The graph is advanced in place. Consumers retaining a snapshot must copy it.
    """
    current = -1
    graph = None
    anchors = sorted(self.graph_paths)
    for step in steps:
      if step <= current or step < 0 or step > self.max_step:
        raise ValueError("expected increasing completed steps")
      anchor_index = bisect_right(anchors, step) - 1
      if anchor_index < 0:
        raise ValueError("bad dump data (graph)")
      anchor = anchors[anchor_index]
      if anchor > current:
        cached = self.graphs_stored.get(anchor)
        graph = cached.copy() if cached is not None else self._load_graph(anchor)
        current = anchor
      assert graph is not None
      if current < step:
        self._advance_graph(graph, current, step)
        current = step
      yield step, graph

  def iter_feed_events(self, start: int, stop: int):
    """Yield actual logged feeds for update steps in [start, stop)."""
    if self.events_db is None:
      raise ValueError("raw simulation record is not loaded")
    yield from iter_view_posts(self.events_db, start, stop)

  def feed_counts(self, step: int):
    """Return one agent-count row, or None if feeds were not counted."""
    if step <= 0 or step > self.max_step:
      return None
    if self.metadata.AgentNumber is True:
      return self.agent_numbers[step]
    if not (self.metadata.ViewPostsEvent is True or self.metadata.ViewPostsDetails is True):
      return None
    # An explicitly selected view-log channel records the same confidence bins.
    rows = {}
    for event in self.iter_feed_events(step, step+1):
      if event.agent_id in rows:
        raise ValueError(f"duplicate feed at step {step} for agent {event.agent_id}")
      feed = event.body
      rows[event.agent_id] = (
          len(feed.neighbor_concordant), len(feed.recommended_concordant),
          len(feed.neighbor_discordant), len(feed.recommended_discordant),
      )
    if set(rows) != set(range(self.agents)):
      return None
    return np.array([rows[i] for i in range(self.agents)], dtype=int)

  def evaluate_probe(
      self,
      steps: Iterable[int],
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
  ) -> ProbeResponse:
    """Evaluate selected saved steps through the native Go run reader."""
    from smp_bindings.probe import run_probe_run
    requested_steps = sorted({int(step) for step in steps})
    if not requested_steps:
      raise ValueError("at least one probe step is required")
    return parse_dataclass(ProbeResponse, run_probe_run(
        self.directory,
        requested_steps,
        metadata=to_wire(self.metadata) if not os.path.isfile(os.path.join(self.directory, "metadata.json")) else None,
        h=h,
        minimum=minimum,
        maximum=maximum,
        replicates=replicates,
        anchor_ids=anchor_ids,
        rng=rng,
        binary_path=binary_path,
        measurements=("force",) if measurements is None else measurements,
        energy_scale=energy_scale,
        energy_epsilon=energy_epsilon,
        per_anchor=per_anchor,
        epsilon=epsilon,
        bins=bins,
        terminal=terminal,
    ))
