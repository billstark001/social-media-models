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


def write_reader(root: Path, raw: bytes, *, agents: int = 2, precision: str = "float32") -> TrajectoryReader:
  (root / "chunk.smpc").write_bytes(raw)
  (root / "trajectory.json").write_text(json.dumps({
      "version": 3, "agents": agents, "next_step": 1,
      "precision": {"opinions": precision, "opinion_sums": precision},
      "chunks": [{"start": 0, "end": 0, "file": "chunk.smpc", "sha256": hashlib.sha256(raw).hexdigest()}],
  }))
  return TrajectoryReader(root)


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

  def test_reader_rejects_manifest_and_chunk_mismatches(self):
    raw = make_chunk("float32", "float32")
    with tempfile.TemporaryDirectory() as folder:
      root = Path(folder)
      reader = write_reader(root, raw, agents=3)
      with self.assertRaisesRegex(ValueError, "shape mismatch"):
        reader.row("opinions", 0)
      reader = write_reader(root, raw, precision="float16")
      with self.assertRaisesRegex(ValueError, "precision mismatch"):
        reader.row("opinions", 0)

  def test_empty_channel_selections_preserve_shape_and_dtype(self):
    with tempfile.TemporaryDirectory() as folder:
      root = Path(folder)
      reader = write_reader(root, make_chunk("float32", "float32"))
      for field, shape in (("opinions", (0, 2)), ("agent_numbers", (0, 2, 4))):
        channel = reader.channel(field)
        self.assertEqual(channel[0:0].shape, shape)
        self.assertEqual(channel[[]].shape, shape)
        self.assertEqual(channel[0:0].dtype, channel.dtype)
      (root / "trajectory.json").write_text(json.dumps({
          "version": 3, "agents": 2, "next_step": 0, "chunks": [],
      }))
      self.assertEqual(np.asarray(TrajectoryReader(root).channel("opinions")).shape, (0, 2))

  def test_decoder_rejects_truncated_headers_and_payloads(self):
    raw = make_chunk("float32", "float32")
    for invalid in (raw[:10], raw[:17], raw[:20], raw[:-1]):
      with self.subTest(length=len(invalid)), self.assertRaisesRegex(ValueError, "truncated"):
        load_trajectory_chunk(invalid)


if __name__ == "__main__":
  unittest.main()
