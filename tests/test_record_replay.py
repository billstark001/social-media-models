import sqlite3
from types import SimpleNamespace

import networkx as nx
import numpy as np
import pytest

from smp_bindings.record import RawSimulationRecord


def test_graph_replay_respects_blocked_replacements_and_new_anchors(monkeypatch):
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.has_trajectory = True
  record.metadata = {"RewiringEvent": True}
  record.max_step = 4
  record.graph_paths = {0: "initial", 3: "anchor"}
  record.graphs_stored = {0: nx.DiGraph([(0, 1), (0, 2)])}
  record.graphs = {}
  record.graph_steps = [0, 3]
  anchor = nx.DiGraph([(0, 2), (0, 3)])
  monkeypatch.setattr("smp_bindings.record.load_gonum_graph_dump", lambda _: anchor.copy())

  with sqlite3.connect(":memory:") as db:
    record.events_db = db
    db.executescript("""
      CREATE TABLE events(id INTEGER PRIMARY KEY, type TEXT, agent_id INTEGER, step INTEGER);
      CREATE TABLE rewiring_events(event_id INTEGER PRIMARY KEY, unfollow INTEGER, follow INTEGER);
      INSERT INTO events VALUES
        (1, 'Rewiring', 0, 1), (2, 'Rewiring', 0, 2), (3, 'Rewiring', 0, 4);
      INSERT INTO rewiring_events VALUES (1, 1, 2), (2, 1, 3), (3, 1, 2);
    """)
    points = {step: set(graph.edges) for step, graph in record.iter_graphs((0, 1, 2, 3))}
    assert points == {0: {(0, 1), (0, 2)}, 1: {(0, 1), (0, 2)}, 2: {(0, 2), (0, 3)}, 3: {(0, 2), (0, 3)}}
    assert list(record.graphs_stored) == [0]
    assert set(record.get_graph(2).edges) == points[2]
    with pytest.raises(ValueError, match="removes a missing edge"):
      list(record.iter_graphs((4,)))



@pytest.mark.parametrize("flags", [
    {"RewiringEvent": False, "HKParams": {"RewiringRate": 0.1}},
    {"HKParams": {"RewiringRate": 0.1}},
    {},
])
def test_graph_replay_requires_collection_evidence_for_unsaved_steps(flags):
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.metadata = flags
  record.max_step = 2
  record.graph_paths = {0: "initial"}
  record.graphs_stored = {0: nx.DiGraph([(0, 1)])}
  record.graphs = {}
  record.graph_steps = [0]
  assert set(record.get_graph(0).edges) == {(0, 1)}
  assert [(step, set(graph.edges)) for step, graph in record.iter_graphs((0,))] == [(0, {(0, 1)})]
  with pytest.raises(ValueError, match="cannot reconstruct graph"):
    record.get_graph(1)
  with pytest.raises(ValueError, match="cannot reconstruct graph"):
    list(record.iter_graphs((1,)))
  record.metadata = {**flags, "HKParams": {"RewiringRate": 0}}
  assert set(record.get_graph(1).edges) == {(0, 1)}
  assert [(step, set(graph.edges)) for step, graph in record.iter_graphs((1, 2))] == [
      (1, {(0, 1)}), (2, {(0, 1)}),
  ]


def test_feed_counts_missing_flag_requires_positive_channel_evidence():
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.metadata = {}
  record.max_step = 2
  record.agents = 2
  rows = np.zeros((3, 2, 4), dtype=int)
  reads = []

  class CountChannel:
    def __getitem__(self, step):
      reads.append(step)
      return rows[step]

  record.agent_numbers = CountChannel()
  record.iter_feed_events = lambda *_: iter(())
  assert record.feed_counts(1) is None
  assert record.feed_counts(2) is None
  assert reads == [1, 2]

  del record._count_channel_available
  rows[2, 0, 1] = 1
  assert np.array_equal(record.feed_counts(1), rows[1])
  assert np.array_equal(record.feed_counts(2), rows[2])

  record.metadata = {"AgentNumber": True}
  rows[:] = 0
  assert np.array_equal(record.feed_counts(1), rows[1])

  record.metadata = {"AgentNumber": False}
  rows[:] = 1
  assert record.feed_counts(1) is None


def test_unknown_zero_count_channel_can_use_complete_view_posts():
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.metadata = {}
  record.max_step = 1
  record.agents = 2
  record.agent_numbers = np.zeros((2, 2, 4), dtype=int)
  body = SimpleNamespace(
      neighbor_concordant=[], recommended_concordant=[],
      neighbor_discordant=[], recommended_discordant=[],
  )
  events = [SimpleNamespace(agent_id=i, body=body) for i in range(2)]
  record.iter_feed_events = lambda *_: iter(events)
  assert record.feed_counts(1).tolist() == [[0, 0, 0, 0], [0, 0, 0, 0]]
  events.pop()
  assert record.feed_counts(1) is None
