import os
import tempfile
import threading
import unittest

from smp_bindings.simulation import is_simulation_finished, run_simulation

SMP_BINARY = os.environ.get("SMP_TEST_BINARY", "")


@unittest.skipUnless(
    bool(SMP_BINARY) and os.path.isfile(SMP_BINARY),
    "set SMP_TEST_BINARY to run integration tests",
)
class SimulationBindingsTest(unittest.TestCase):

  def test_persistent_runner_decodes_shared_json_progress(self):
    metadata = {
        "UniqueName": "json-progress",
        "DynamicsType": "HK",
        "HKParams": {
            "Tolerance": 0.45,
            "Influence": 0.05,
            "RewiringRate": 0.05,
            "RepostRate": 0.0,
        },
        "NodeCount": 20,
        "NodeFollowCount": 5,
        "MaxSimulationStep": 4,
    }
    with tempfile.TemporaryDirectory() as base_path:
      name = run_simulation(
          SMP_BINARY,
          base_path,
          metadata,
          False,
          threading.Lock(),
      )
      self.assertEqual(name, "json-progress")
      self.assertEqual(metadata["RNG"]["Algorithm"], "pcg64-dxsm-v1")
      self.assertTrue(is_simulation_finished(base_path, metadata))


if __name__ == "__main__":
  unittest.main()
