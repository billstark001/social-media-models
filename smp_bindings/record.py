from typing import List, Dict
from numpy.typing import NDArray

import os
import re

import numpy as np
import networkx as nx

from smp_bindings.model_state import load_accumulative_model_state, load_gonum_graph_dump
from smp_bindings.events_db import get_events_by_step_range, get_rewiring_event_body, load_events_db


re_graph = re.compile(r'graph-(\d+).msgpack')


def _last_less_than(arr: np.ndarray, val: int) -> int:
  """返回 arr 中最后一个严格小于 val 的元素的下标，未找到返回 -1。"""
  idx = np.searchsorted(arr, val, side='left') - 1
  return int(idx)


class RawSimulationRecord:

  def __init__(self, base_dir: str, metadata: dict):
    self.unique_name = metadata['UniqueName']
    full_path = os.path.join(base_dir, self.unique_name)
    file_list = os.listdir(full_path)
    file_list.sort()
    # finished mark
    self.is_finished = any(x for x in file_list if x.startswith('finished'))
    has_events_db = 'events.db' in file_list
    acc_state_list = [x for x in file_list if x.startswith('acc-state-')]
    graph_list = [x for x in file_list if x.startswith('graph-')]

    self.is_sanitized = has_events_db and len(
        acc_state_list) > 0 and len(graph_list) > 0
    if not self.is_finished or not self.is_sanitized:
      return

    self.events_db_path = os.path.join(full_path, 'events.db')
    self.acc_state_path = os.path.join(full_path, acc_state_list[-1])
    self.graph_paths = {}
    for graph_name in graph_list:
      step_index = int(re_graph.match(graph_name).group(1))  # type: ignore
      self.graph_paths[step_index] = os.path.join(full_path, graph_name)

    self.max_step: int = 0
    self.metadata: dict = dict(**metadata)  # type: ignore

  def load(self):
    acc_state = load_accumulative_model_state(self.acc_state_path)
    events_db = load_events_db(self.events_db_path)
    graphs: Dict[int, nx.DiGraph] = {}
    for graph_name, graph_path in self.graph_paths.items():
      graphs[graph_name] = load_gonum_graph_dump(graph_path)  # type: ignore

    self.acc_state = acc_state
    self.events_db = events_db
    self.graphs_stored = graphs
    self.graphs: Dict[int, nx.DiGraph] = {}
    self.graph_steps: List[int] = []
    self.graph_steps.extend(self.graphs_stored.keys())
    self.graph_steps.sort()

    # store acc state data locally
    self.opinions: NDArray = self.acc_state['opinions']
    self.agent_numbers: NDArray = self.acc_state['agent_numbers']
    self.agent_opinion_sums: NDArray = self.acc_state['agent_opinion_sums']
    self.agents = int(self.acc_state['agents'])
    self.max_step = int(acc_state['steps'])

    g0 = self.graphs_stored[0]
    follow_counts = [len(g0.out_edges(x)) for x in range(self.agents)]
    self.followers = np.array(follow_counts)

  def dispose(self):
    self.events_db.close()
    self.graphs = {}
    self.graph_steps = []
    self.graph_steps.extend(self.graphs_stored.keys())
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
    if step in self.graphs_stored:
      return self.graphs_stored[step]
    if step in self.graphs:
      return self.graphs[step]

    # the step needs to be parsed

    # get graph
    nearest_available_step_idx = _last_less_than(
        np.array(self.graph_steps), step)
    if nearest_available_step_idx < 0:
      raise ValueError('bad dump data (graph)')
    nearest_available_step = self.graph_steps[nearest_available_step_idx]
    nearest_available_graph: nx.DiGraph = (
        self.graphs_stored[nearest_available_step]
        if nearest_available_step in self.graphs_stored else
        self.graphs[nearest_available_step]
    ).copy()  # type: ignore

    # get events
    # since we want all events till the step ends, so step + 1
    rewiring_events = get_events_by_step_range(
        self.events_db, nearest_available_step + 1, step + 1, "Rewiring"
    )
    # this assures the events to be sequential
    rewiring_events.sort(key=lambda x: x.step)

    # apply events
    for e in rewiring_events:
      body = get_rewiring_event_body(self.events_db, e.id)
      if body is None:
        raise ValueError("bad dump data (event)")
      try:
        nearest_available_graph.remove_edge(e.agent_id, body.unfollow)
      except nx.NetworkXError as ex:
        # very occasional data corruption, attempt to remove inexistent edges
        # this essentially does not affect the collective pattern
        pass
      nearest_available_graph.add_edge(e.agent_id, body.follow)

    # store the applied graphs
    self.graphs[step] = nearest_available_graph
    self.graph_steps.append(step)
    self.graph_steps.sort()

    return nearest_available_graph
