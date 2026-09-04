import json
import unittest

from smp_bindings.codec import (
    BATCH_SCHEMA_VERSION,
    ProtocolError,
    decode_batch_response,
    decode_progress_line,
    encode_batch_request,
)


class CodecTest(unittest.TestCase):

  def test_progress_codec_expands_compact_keys(self):
    event = decode_progress_line(
        '{"v":1,"id":"case","t":"done","s":8,"m":10,"r":"halt"}'
    )
    self.assertEqual(event, {
        "version": 1,
        "request_id": "case",
        "type": "done",
        "step": 8,
        "max_step": 10,
        "stop_reason": "halt",
    })

  def test_progress_codec_ignores_diagnostics_and_legacy_lines(self):
    self.assertIsNone(decode_progress_line("ordinary diagnostic"))
    self.assertIsNone(
        decode_progress_line("TASK:case;TYPE:PROGRESS;STEP:2;")
    )

  def test_progress_codec_rejects_identified_unknown_version(self):
    with self.assertRaises(ProtocolError):
      decode_progress_line('{"v":2,"id":"case","t":"start"}')

  def test_batch_request_adds_schema_version_without_mutating_input(self):
    request = {"request_id": "case", "metadata": {}, "output": {}}
    encoded = json.loads(encode_batch_request(request))
    self.assertEqual(encoded["schema_version"], BATCH_SCHEMA_VERSION)
    self.assertNotIn("schema_version", request)

  def test_batch_request_rejects_wrong_schema_version(self):
    with self.assertRaises(ProtocolError):
      encode_batch_request({
          "schema_version": 2,
          "request_id": "case",
          "metadata": {},
      })

  def test_batch_response_is_correlated(self):
    response = decode_batch_response(
        '{"schema_version":1,"request_id":"case","status":"ok",'
        '"result":{}}',
        expected_request_id="case",
    )
    self.assertEqual(response["status"], "ok")
    with self.assertRaises(ProtocolError):
      decode_batch_response(
          '{"schema_version":1,"request_id":"other","status":"ok",'
          '"result":{}}',
          expected_request_id="case",
      )


if __name__ == "__main__":
  unittest.main()
