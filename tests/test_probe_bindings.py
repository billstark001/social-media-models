import json
import os
import sqlite3
import tempfile
import unittest

import networkx as nx
import numpy as np

from smp_bindings.probe import freeze_record, run_probe
from smp_bindings.record import RawSimulationRecord


class _FakeRecord:

  def __init__(self, dynamics_type="HK"):
    self.max_step = 2
    self.agents = 3
    self.opinions = np.array([
        [-0.5, 0.0, 0.5],
        [-0.4, 0.1, 0.4],
        [-0.3, 0.2, 0.3],
    ])
    self.metadata = {
        "DynamicsType": dynamics_type,
        f"{dynamics_type}Params": {
            "Tolerance": 0.75,
            "Influence": 0.5,
            "RepostRate": 0.0,
        },
        "PostEvent": False,
        "PostRetainCount": 2,
        "RecsysCount": 1,
        "RecsysFactoryType": "Random",
    }

  def get_graph(self, step):
    graph = nx.DiGraph()
    graph.add_nodes_from(range(self.agents))
    graph.add_edges_from([(0, 1), (1, 2), (2, 0)])
    return graph


class ProbeBindingsTest(unittest.TestCase):

  def test_freeze_reconstructs_normal_post_history(self):
    states = freeze_record(_FakeRecord(), [0, 2])
    self.assertEqual([state.step for state in states], [0, 2])
    self.assertEqual(states[0].posts[0][0].step, -1)
    self.assertEqual(
        [post.step for post in states[1].posts[0]],
        [1, 2],
    )
    self.assertAlmostEqual(states[1].posts[0][-1].opinion, -0.3)

  def test_freeze_reconstructs_repost_origin(self):
    record = _FakeRecord()
    record.metadata["HKParams"]["RepostRate"] = 0.5
    record.metadata["PostEvent"] = True
    record.events_db = sqlite3.connect(":memory:")
    record.events_db.execute(
        "CREATE TABLE events "
        "(id INTEGER, type TEXT, agent_id INTEGER, step INTEGER)"
    )
    record.events_db.execute(
        "CREATE TABLE post_events "
        "(event_id INTEGER, agent_id INTEGER, step INTEGER, "
        "opinion REAL, is_repost INTEGER)"
    )
    record.events_db.execute(
        "INSERT INTO events VALUES (1, 'Post', 0, 1)"
    )
    record.events_db.execute(
        "INSERT INTO post_events VALUES (1, 2, -1, 0.5, 1)"
    )
    states = freeze_record(record, [1])
    repost = states[0].posts[0][-1]
    self.assertEqual(repost.agent_id, 2)
    self.assertEqual(repost.step, -1)
    self.assertAlmostEqual(repost.opinion, 0.5)
    record.events_db.close()

  def test_record_prefers_persisted_resolved_metadata(self):
    with tempfile.TemporaryDirectory() as base_dir:
      run_dir = os.path.join(base_dir, "run")
      os.makedirs(run_dir)
      with open(
          os.path.join(run_dir, "metadata.json"),
          "w",
          encoding="utf-8",
      ) as metadata_file:
        json.dump({
            "UniqueName": "run",
            "DataVersion": 1,
            "RNG": {
                "Algorithm": "pcg64-dxsm-v1",
                "Seed1": "0x1",
                "Seed2": "0x2",
            },
        }, metadata_file)
      record = RawSimulationRecord(base_dir, {"UniqueName": "run"})
      self.assertEqual(record.metadata["DataVersion"], 1)
      self.assertEqual(record.metadata["RNG"]["Seed1"], "0x1")

  @unittest.skipUnless(
      os.path.isfile("./smp-probe"),
      "build ./smp-probe to run the binding integration test",
  )
  def test_go_probe_round_trip_for_hk_and_deffuant(self):
    for dynamics_type in ("HK", "Deffuant"):
      record = _FakeRecord(dynamics_type)
      states = freeze_record(record, [2])
      response = run_probe(
          states,
          record.metadata,
          h=1.0,
          binary_path="./smp-probe",
      )
      self.assertEqual(response["dynamics_type"], dynamics_type)
      self.assertEqual(len(response["results"]), 1)
      self.assertEqual(len(response["results"][0]["points"]), 3)


if __name__ == "__main__":
  unittest.main()
