import json
import os
import re
from bisect import bisect_left, bisect_right
from collections.abc import Iterable, Mapping, Sequence
from typing import Any

import networkx as nx
import numpy as np
from numpy.typing import NDArray

from smp_bindings.events_db import iter_rewiring_events, iter_view_posts, load_events_db
from smp_bindings.model_state import (
  load_accumulative_model_state,
  load_gonum_graph_dump,
)

re_graph = re.compile(r'graph-(\d+)\.msgpack(?:\.lz4)?$')


class RawSimulationRecord:

  def __init__(self, base_dir: str, metadata: dict):
    self.unique_name = metadata['UniqueName']
    full_path = os.path.join(base_dir, self.unique_name)
    persisted_metadata_path = os.path.join(full_path, "metadata.json")
    resolved_metadata = dict(metadata)
    if os.path.isfile(persisted_metadata_path):
      with open(persisted_metadata_path, "r", encoding="utf-8") as metadata_file:
        persisted_metadata = json.load(metadata_file)
      if isinstance(persisted_metadata, dict):
        resolved_metadata.update(persisted_metadata)
    self.metadata: dict = resolved_metadata

    file_list = os.listdir(full_path)
    file_list.sort()
    # finished mark
    self.is_finished = any(x for x in file_list if x.startswith('finished'))
    has_events_db = 'events.db' in file_list
    self.trajectory_path = os.path.join(full_path, 'trajectory.json')
    self.has_trajectory = 'trajectory.json' in file_list
    acc_state_list = [x for x in file_list if x.startswith('acc-state-')]
    graph_list = [x for x in file_list if re_graph.fullmatch(x)]

    self.is_sanitized = has_events_db and (self.has_trajectory or len(
        acc_state_list) > 0) and len(graph_list) > 0
    if not self.is_finished or not self.is_sanitized:
      return

    self.events_db_path = os.path.join(full_path, 'events.db')
    self.acc_state_path = os.path.join(full_path, acc_state_list[-1]) if acc_state_list else None
    self.graph_paths = {}
    for graph_name in graph_list:
      step_index = int(re_graph.match(graph_name).group(1))  # type: ignore
      self.graph_paths[step_index] = os.path.join(full_path, graph_name)

    self.max_step: int = 0
  def _get_stored_graph(self, step: int) -> nx.DiGraph:
    """按需从文件加载并缓存 graphs_stored 中的图（懒加载）。"""
    if step not in self.graphs_stored:
      self.graphs_stored[step] = load_gonum_graph_dump(self.graph_paths[step])  # type: ignore
    return self.graphs_stored[step]

  def load(self):
    if self.has_trajectory:
      from smp_bindings.trajectory import TrajectoryReader
      self.trajectory = TrajectoryReader(os.path.dirname(self.trajectory_path))
      acc_state = {
          'steps': self.trajectory.steps - 1,
          'agents': self.trajectory.agents,
          'opinions': self.trajectory.channel('opinions'),
          'agent_numbers': self.trajectory.channel('agent_numbers'),
          'agent_opinion_sums': self.trajectory.channel('agent_opinion_sums'),
      }
    else:
      acc_state = load_accumulative_model_state(
        self.acc_state_path,
        with_agent_numbers=True,
        with_agent_opinion_sums=True,
      )
    events_db = load_events_db(self.events_db_path)

    self.acc_state = acc_state
    self.events_db = events_db
    self.graphs_stored: dict[int, nx.DiGraph] = {}  # lazy-loaded cache
    self.graphs: dict[int, nx.DiGraph] = {}
    self.graph_steps: list[int] = []
    self.graph_steps.extend(self.graph_paths.keys())
    self.graph_steps.sort()

    # store acc state data locally
    self.opinions: NDArray = self.acc_state['opinions']
    self.agent_numbers: NDArray = self.acc_state['agent_numbers']
    self.agent_opinion_sums: NDArray = self.acc_state['agent_opinion_sums']
    self.agents = int(self.acc_state['agents'])
    self.max_step = int(acc_state['steps'])
    self._count_channel_available = self.metadata.get('AgentNumber')

    g0 = self._get_stored_graph(0)
    follow_counts = [len(g0.out_edges(x)) for x in range(self.agents)]
    self.followers = np.array(follow_counts)

  def dispose(self):
    self.events_db.close()
    self.graphs = {}
    self.graphs_stored = {}
    self.graph_steps = []
    self.graph_steps.extend(self.graph_paths.keys())
    self.graph_steps.sort()

  def __enter__(self):
    self.load()
    return self

  def __exit__(self, exc_type, exc_value, traceback):
    self.dispose()

  def get_graph(self, step: int):
    # the step is invalid
    if step > self.max_step or step < 0:
      raise ValueError("invalid step")

    # the step is already available
    if step in self.graph_paths and step not in self.graphs_stored:
      # stored graph not yet loaded — skip to lazy-load path below
      pass
    elif step in self.graphs_stored:
      return self.graphs_stored[step]
    elif step in self.graphs:
      return self.graphs[step]

    # the step needs to be parsed (or is a stored graph not yet loaded)

    # if the step matches a stored file exactly, lazy-load and return
    if step in self.graph_paths:
      return self._get_stored_graph(step)

    # get graph
    nearest_available_step_idx = bisect_left(self.graph_steps, step) - 1
    if nearest_available_step_idx < 0:
      raise ValueError('bad dump data (graph)')
    nearest_available_step = self.graph_steps[nearest_available_step_idx]
    nearest_available_graph: nx.DiGraph = (
        self._get_stored_graph(nearest_available_step)
        if nearest_available_step in self.graph_paths else
        self.graphs[nearest_available_step]
    ).copy()  # type: ignore

    self._advance_graph(nearest_available_graph, nearest_available_step, step)

    # store the applied graphs
    self.graphs[step] = nearest_available_graph
    self.graph_steps.append(step)
    self.graph_steps.sort()

    return nearest_available_graph

  def _advance_graph(self, graph: nx.DiGraph, start: int, step: int) -> None:
    dynamics = self.metadata.get("DynamicsType", "HK")
    params = self.metadata.get(f"{dynamics}Params", {})
    if params.get("RewiringRate") == 0:
      return
    if self.metadata.get("RewiringEvent") is not True:
      raise ValueError(
          f"cannot reconstruct graph at step {step}: rewiring-event collection "
          "is disabled or unknown and no exact graph snapshot is available"
      )
    # Legacy logs can include attempts blocked by an existing target edge.
    for event_id, agent_id, unfollow, follow in iter_rewiring_events(self.events_db, start + 1, step + 1):
      has_unfollow = graph.has_edge(agent_id, unfollow)
      if not has_unfollow and self.has_trajectory:
        raise ValueError(f"rewiring event {event_id} removes a missing edge")
      if graph.has_edge(agent_id, follow):
        continue
      if has_unfollow:
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
        graph = cached.copy() if cached is not None else load_gonum_graph_dump(self.graph_paths[anchor])
        current = anchor
      assert graph is not None
      if current < step:
        self._advance_graph(graph, current, step)
        current = step
      yield step, graph

  def iter_feed_events(self, start: int, stop: int):
    """Yield actual logged feeds for update steps in [start, stop)."""
    yield from iter_view_posts(self.events_db, start, stop)

  def feed_counts(self, step: int):
    """Return one agent-count row, or None if feeds were not counted."""
    if step <= 0 or step > self.max_step:
      return None
    collected = self.metadata.get("AgentNumber")
    if collected is None:
      collected = getattr(self, "_count_channel_available", None)
      if collected is None:
        # A missing historical flag provides no guarantee that a zero channel
        # was collected. Scan once with bounded memory for positive evidence.
        collected = any(np.any(self.agent_numbers[i] > 0) for i in range(1, self.max_step + 1))
        self._count_channel_available = collected
    if collected is True:
      return self.agent_numbers[step]
    # Old view logs retain the confidence buckets even without AgentNumbers.
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

  def freeze(self, steps: Iterable[int]):
    """Reconstruct frozen Go-probe inputs for selected completed steps."""
    from smp_bindings.probe import freeze_record
    return freeze_record(self, steps)

  def evaluate_probe(
      self,
      steps: Iterable[int],
      *,
      h: float,
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
  ) -> dict[str, Any]:
    """Freeze selected steps and evaluate F_probe in the Go implementation."""
    from smp_bindings.probe import run_probe
    states = self.freeze(steps)
    return run_probe(
        states,
        self.metadata,
        h=h,
        minimum=minimum,
        maximum=maximum,
        replicates=replicates,
        anchor_ids=anchor_ids,
        rng=rng,
        binary_path=binary_path,
        measurements=measurements,
        energy_scale=energy_scale,
        energy_epsilon=energy_epsilon,
        per_anchor=per_anchor,
    )
