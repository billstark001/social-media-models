import hashlib
import json
import struct
import tempfile
import unittest
from pathlib import Path

import lz4.frame
import numpy as np

from smp_bindings.trajectory import TrajectoryReader, load_trajectory_chunk


def make_chunk(opinion_dtype: str, sum_dtype: str, version: int = 3) -> bytes:
  opinions = np.array([[1 / 3, -0.75]], dtype=opinion_dtype)
  counts = np.array([[[1, 2, 3, 4], [5, 6, 7, 8]]], dtype="<i2")
  sums = np.array([[[1 / 3, 0, 0, 0], [-1 / 7, 2, 3, 4]]], dtype=sum_dtype)
  data = bytearray(b"SMPTRJ03" if version == 3 else b"SMPTRJ02")
  data.extend(struct.pack("<II", 1, 2))
  if version == 3:
    data.extend(struct.pack("<BB", opinions.dtype.itemsize, sums.dtype.itemsize))
  for values in (opinions, counts, sums):
    payload = lz4.frame.compress(values.tobytes())
    data.extend(struct.pack("<BI", 0, len(payload)))
    data.extend(payload)
  return bytes(data)


class TrajectoryPrecisionTest(unittest.TestCase):

  def test_all_channel_precision_combinations(self):
    for opinion_level in ("float16", "float32", "float64"):
      for sum_level in ("float16", "float32", "float64"):
        with self.subTest(opinions=opinion_level, opinion_sums=sum_level):
          raw = make_chunk(opinion_level, sum_level)
          chunk = load_trajectory_chunk(raw)
          self.assertEqual(chunk["opinions"].dtype, np.dtype(opinion_level))
          self.assertEqual(chunk["agent_opinion_sums"].dtype, np.dtype(sum_level))
          self.assertEqual(chunk["agent_numbers"].dtype, np.dtype("int16"))
          self.assertEqual(chunk["opinions"][0, 0], np.array(1 / 3, dtype=opinion_level))
          self.assertEqual(chunk["agent_opinion_sums"][0, 1, 0], np.array(-1 / 7, dtype=sum_level))

  def test_reader_reports_mixed_legacy_precision(self):
    old = make_chunk("float32", "float32", version=2)
    new = make_chunk("float16", "float16")
    with tempfile.TemporaryDirectory() as folder:
      root = Path(folder)
      chunks = []
      for step, raw in enumerate((old, new)):
        name = f"chunk-{step}.smpc"
        (root / name).write_bytes(raw)
        chunks.append({"start": step, "end": step, "file": name,
                       "sha256": hashlib.sha256(raw).hexdigest()})
      (root / "trajectory.json").write_text(json.dumps({
          "version": 3, "agents": 2, "next_step": 2,
          "precision": {"opinions": "float16", "opinion_sums": "float16"},
          "legacy_chunks": 1, "chunks": chunks,
      }))
      reader = TrajectoryReader(root)
      self.assertEqual(reader.channel("opinions").dtype, np.dtype("float32"))
      self.assertEqual(reader.row("opinions", 0).dtype, np.dtype("float32"))
      self.assertEqual(reader.row("opinions", 1).dtype, np.dtype("float32"))


if __name__ == "__main__":
  unittest.main()
