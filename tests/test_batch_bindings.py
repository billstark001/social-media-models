import os
import unittest

from smp_bindings.batch import (
    BatchClient,
    BatchExecutionError,
    run_batch,
    run_batch_parallel,
)

BATCH_BINARY = os.environ.get("SMP_BATCH_TEST_BINARY", "")


def _request(request_id, *, node_count=20):
  return {
      "request_id": request_id,
      "metadata": {
          "UniqueName": request_id,
          "DynamicsType": "HK",
          "HKParams": {
              "Tolerance": 0.45,
              "Influence": 0.05,
              "RewiringRate": 0.05,
              "RepostRate": 0.0,
          },
          "RNG": {
              "Algorithm": "pcg64-dxsm-v1",
              "Seed1": "0x0000000000000001",
              "Seed2": "0x0000000000000002",
          },
          "NodeCount": node_count,
          "NodeFollowCount": 5,
          "MaxSimulationStep": 4,
      },
      "output": {"final_opinions": False},
  }


@unittest.skipUnless(
    bool(BATCH_BINARY) and os.path.isfile(BATCH_BINARY),
    "set SMP_BATCH_TEST_BINARY to run integration tests",
)
class BatchBindingsTest(unittest.TestCase):

  def test_long_lived_client_handles_multiple_requests(self):
    with BatchClient(BATCH_BINARY) as client:
      first = client.run(_request("first"))
      second = client.run(_request("second"))
    self.assertEqual(first["request_id"], "first")
    self.assertEqual(second["request_id"], "second")
    self.assertEqual(first["result"]["opinions"]["count"], 20)

  def test_run_batch_decodes_shared_progress(self):
    events = []
    responses = run_batch(
        BATCH_BINARY,
        [_request("progress")],
        progress=events.append,
        progress_step_interval=2,
    )
    self.assertEqual(responses[0]["status"], "ok")
    self.assertEqual(events[0]["type"], "start")
    self.assertEqual(events[-1]["type"], "done")
    self.assertTrue(any(event["type"] == "progress" for event in events))
    self.assertEqual(events[0]["request_index"], 1)

  def test_request_error_can_raise_or_be_returned(self):
    with self.assertRaises(BatchExecutionError):
      run_batch(BATCH_BINARY, [_request("bad", node_count=2)])
    response = run_batch(
        BATCH_BINARY,
        [_request("bad-returned", node_count=2)],
        check=False,
    )[0]
    self.assertEqual(response["status"], "error")

  def test_parallel_runner_preserves_request_order(self):
    requests = [_request(f"parallel-{index}") for index in range(4)]
    responses = run_batch_parallel(
        BATCH_BINARY,
        requests,
        processes=2,
    )
    self.assertEqual(
        [response["request_id"] for response in responses],
        [request["request_id"] for request in requests],
    )


if __name__ == "__main__":
  unittest.main()
