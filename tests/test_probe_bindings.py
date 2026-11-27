import json
import os
import tempfile
import unittest

import networkx as nx
from smp_bindings.events_db import PostRecord
from smp_bindings.probe import FrozenSimulationState, run_probe
from smp_bindings.record import RawSimulationRecord
from smp_bindings.domain import parse_metadata


def synthetic_state():
  graph = nx.DiGraph([(0, 1), (1, 2), (2, 0)])
  opinions = [-0.3, 0.2, 0.3]
  return FrozenSimulationState(
      step=2, graph=graph, opinions=opinions,
      posts={agent: [PostRecord(agent, 2, opinion)] for agent, opinion in enumerate(opinions)},
  )


class ProbeBindingsTest(unittest.TestCase):

  def test_synthetic_input_preserves_post_origin(self):
    state = synthetic_state()
    state.posts[0].append(PostRecord(2, -1, 0.5))
    wire = state.to_wire()
    self.assertEqual(wire["precision"], "inline_f64")
    self.assertEqual(wire["posts"][0][-1], {"AgentID": 2, "Step": -1, "Opinion": 0.5})

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
      record = RawSimulationRecord(base_dir, parse_metadata({"UniqueName": "run"}))
      self.assertEqual(record.metadata.DataVersion, 1)
      self.assertEqual(record.metadata.RNG.Seed1, "0x1")

  def test_requested_flags_never_fill_missing_recorded_collection_evidence(self):
    with tempfile.TemporaryDirectory() as base_dir:
      run_dir = os.path.join(base_dir, "run")
      os.makedirs(run_dir)
      with open(os.path.join(run_dir, "metadata.json"), "w", encoding="utf8") as stream:
        json.dump({"UniqueName": "run"}, stream)
      record = RawSimulationRecord(base_dir, parse_metadata({"UniqueName": "run", "AgentNumber": True,
                                                            "RewiringEvent": True}))
      self.assertIsNone(record.metadata.AgentNumber)
      self.assertIsNone(record.metadata.RewiringEvent)

  @unittest.skipUnless(
      os.path.isfile(os.environ.get("SMP_PROBE_TEST_BINARY", "")),
      "set SMP_PROBE_TEST_BINARY to run the binding integration test",
  )
  def test_go_probe_round_trip_for_hk_and_deffuant(self):
    for dynamics_type in ("HK", "Deffuant"):
      metadata = {
          "DynamicsType": dynamics_type,
          f"{dynamics_type}Params": {"Tolerance": 0.75, "Influence": 0.5},
          "RecsysCount": 1, "PostRetainCount": 2, "RecsysFactoryType": "Random",
      }
      response = run_probe(
          [synthetic_state()],
          metadata,
          h=1.0,
          binary_path=os.environ["SMP_PROBE_TEST_BINARY"],
      )
      self.assertEqual(response["version"], 1)
      self.assertEqual(response["dynamics_type"], dynamics_type)
      self.assertEqual(len(response["results"]), 1)
      self.assertEqual(len(response["results"][0]["points"]), 3)


if __name__ == "__main__":
  unittest.main()
