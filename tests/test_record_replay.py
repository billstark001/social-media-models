import sqlite3
from types import SimpleNamespace

import networkx as nx
import numpy as np
import pytest

from smp_bindings.record import RawSimulationRecord
from smp_bindings.domain import parse_metadata, ProbeResponse


def test_graph_replay_respects_blocked_replacements_and_new_anchors(monkeypatch):
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.has_trajectory = True
  record.agents = 4
  record.metadata = parse_metadata({"UniqueName": "test", "RewiringEvent": True})
  record.max_step = 4
  record.graph_paths = {0: "initial", 3: "anchor"}
  record.graphs_stored = {0: nx.DiGraph([(0, 1), (0, 2)])}
  record.graphs = {}
  record.graph_steps = [0, 3]
  anchor = nx.DiGraph([(0, 2), (0, 3)])
  monkeypatch.setattr("smp_bindings.record.load_gonum_graph_dump", lambda _, **kwargs: anchor.copy())

  with sqlite3.connect(":memory:") as db:
    record.events_db = db
    db.executescript("""
      CREATE TABLE events(id INTEGER PRIMARY KEY, type TEXT, agent_id INTEGER, step INTEGER);
      CREATE TABLE rewiring_events(event_id INTEGER PRIMARY KEY, unfollow INTEGER, follow INTEGER);
      INSERT INTO events VALUES
        (1, 'Rewiring', 0, 1), (2, 'Rewiring', 0, 2), (3, 'Rewiring', 0, 4);
      INSERT INTO rewiring_events VALUES (1, 1, 2), (2, 1, 3), (3, 1, 1);
    """)
    points = {step: set(graph.edges) for step, graph in record.iter_graphs((0, 1, 2, 3))}
    assert points == {0: {(0, 1), (0, 2)}, 1: {(0, 1), (0, 2)}, 2: {(0, 2), (0, 3)}, 3: {(0, 2), (0, 3)}}
    assert list(record.graphs_stored) == [0]
    assert set(record.get_graph(2).edges) == points[2]
    with pytest.raises(ValueError, match="removes a missing edge"):
      list(record.iter_graphs((4,)))



@pytest.mark.parametrize("has_trajectory", [True, False])
def test_replay_never_turns_missing_unfollow_into_edge_creation(has_trajectory):
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.has_trajectory = has_trajectory
  record.metadata = parse_metadata({"UniqueName": "test", "RewiringEvent": True})
  graph = nx.DiGraph([(0, 1)])
  with sqlite3.connect(":memory:") as db:
    record.events_db = db
    db.executescript("""
      CREATE TABLE events(id INTEGER PRIMARY KEY, type TEXT, agent_id INTEGER, step INTEGER);
      CREATE TABLE rewiring_events(event_id INTEGER PRIMARY KEY, unfollow INTEGER, follow INTEGER);
      INSERT INTO events VALUES (1, 'Rewiring', 0, 1), (2, 'Rewiring', 0, 2);
      INSERT INTO rewiring_events VALUES (1, 2, 1), (2, 2, 3);
    """)
    # The first logged attempt is blocked by its existing target edge.
    record._advance_graph(graph, 0, 1)
    assert set(graph.edges) == {(0, 1)}
    with pytest.raises(ValueError, match="event 2 removes a missing edge"):
      record._advance_graph(graph, 1, 2)
    assert set(graph.edges) == {(0, 1)}


@pytest.mark.parametrize("flags", [
    {"RewiringEvent": False, "HKParams": {"RewiringRate": 0.1}},
    {"HKParams": {"RewiringRate": 0.1}},
    {},
    {"HKParams": None},
])
def test_graph_replay_requires_collection_evidence_for_unsaved_steps(flags):
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.metadata = parse_metadata({"UniqueName": "test", **flags})
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
  record.metadata = parse_metadata({"UniqueName": "test", **flags, "HKParams": {"RewiringRate": 0}})
  assert set(record.get_graph(1).edges) == {(0, 1)}
  assert [(step, set(graph.edges)) for step, graph in record.iter_graphs((1, 2))] == [
      (1, {(0, 1)}), (2, {(0, 1)}),
  ]


def test_feed_counts_missing_flag_never_infers_collection_from_channel_values():
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.metadata = parse_metadata({"UniqueName": "test"})
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
  assert reads == []

  rows[2, 0, 1] = 1
  assert record.feed_counts(1) is None
  assert record.feed_counts(2) is None
  assert reads == []

  record.metadata = parse_metadata({"UniqueName": "test", "AgentNumber": True})
  rows[:] = 0
  assert np.array_equal(record.feed_counts(1), rows[1])

  record.metadata = parse_metadata({"UniqueName": "test", "AgentNumber": False})
  rows[:] = 1
  assert record.feed_counts(1) is None


def test_feed_counts_uses_only_explicitly_selected_view_log():
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.metadata = parse_metadata({"UniqueName": "test"})
  record.max_step = 1
  record.agents = 2
  record.agent_numbers = np.zeros((2, 2, 4), dtype=int)
  body = SimpleNamespace(
      neighbor_concordant=[], recommended_concordant=[],
      neighbor_discordant=[], recommended_discordant=[],
  )
  events = [SimpleNamespace(agent_id=i, body=body) for i in range(2)]
  record.iter_feed_events = lambda *_: iter(events)
  assert record.feed_counts(1) is None
  record.metadata = parse_metadata({"UniqueName": "test", "ViewPostsEvent": True})
  assert record.feed_counts(1).tolist() == [[0, 0, 0, 0], [0, 0, 0, 0]]
  events.pop()
  assert record.feed_counts(1) is None


def test_record_probe_uses_native_run_source(monkeypatch, tmp_path):
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.directory = str(tmp_path)
  record.metadata = parse_metadata({"UniqueName": tmp_path.name})
  calls = []

  def run(directory, steps, **options):
    calls.append((directory, steps, options))
    return {"version": 1, "rng": {"Algorithm": "pcg64-dxsm-v1", "Seed1": "0x1", "Seed2": "0x2"}, "dynamics_type": "HK", "results": []}

  monkeypatch.setattr("smp_bindings.probe.run_probe_run", run)
  response = record.evaluate_probe([2, 0, 2], minimum=-0.5, maximum=0.5, measurements=["bins"], bins=[-1, 0, 1])
  assert isinstance(response, ProbeResponse) and response.results == ()
  assert calls[0][0:2] == (str(tmp_path), [0, 2])
  assert calls[0][2]["minimum"] == -0.5
  assert calls[0][2]["maximum"] == 0.5
  assert calls[0][2]["bins"] == [-1, 0, 1]
  with pytest.raises(ValueError, match="at least one probe step"):
    record.evaluate_probe([])


def test_probe_integer_anchor_maps_decode_once_at_json_boundary(monkeypatch, tmp_path):
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.directory = str(tmp_path)
  record.metadata = parse_metadata({"UniqueName": tmp_path.name})
  wire = {"version": 1, "rng": {"Algorithm": "pcg64-dxsm-v1", "Seed1": "0x1", "Seed2": "0x2"},
          "dynamics_type": "HK", "results": [{"step": 0, "precision": "history_f64", "points": None,
             "counterfactual_neighbor_energy": {"12": [0., 1.]}, "counterfactual_neighbor_valid": {"12": True}}]}
  monkeypatch.setattr("smp_bindings.probe.run_probe_run", lambda *args, **kwargs: wire)
  response = record.evaluate_probe([0], measurements=["counterfactual_neighbor_energy"])
  assert response.results[0].counterfactual_neighbor_energy[12] == (0., 1.)
  assert response.results[0].counterfactual_neighbor_valid[12] is True


@pytest.mark.parametrize("keys", [{"01": True}, {"-0": True}, {"1": True, 1: False}])
def test_probe_anchor_maps_reject_ambiguous_integer_identifiers(monkeypatch, tmp_path, keys):
  record = RawSimulationRecord.__new__(RawSimulationRecord)
  record.directory = str(tmp_path)
  record.metadata = parse_metadata({"UniqueName": tmp_path.name})
  wire = {"version": 1, "rng": {"Algorithm": "pcg64-dxsm-v1", "Seed1": "0x1", "Seed2": "0x2"},
          "dynamics_type": "HK", "results": [{"step": 0, "precision": "history_f64", "points": None,
             "counterfactual_neighbor_valid": keys}]}
  monkeypatch.setattr("smp_bindings.probe.run_probe_run", lambda *args, **kwargs: wire)
  with pytest.raises(ValueError):
    record.evaluate_probe([0], measurements=["counterfactual_neighbor_energy"])
