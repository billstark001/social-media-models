"""Lazy readers for version-2 and version-3 trajectory channels."""

from __future__ import annotations

import hashlib
import json
import struct
from pathlib import Path
from typing import Any

import numpy as np
import lz4.frame
import zstandard


def load_trajectory_chunk(raw: bytes) -> dict[str, np.ndarray]:
  if raw[:8] not in (b"SMPTRJ02", b"SMPTRJ03"):
    raise ValueError("unknown trajectory chunk format")
  if len(raw) < 16:
    raise ValueError("truncated trajectory header")
  steps, agents = struct.unpack_from("<II", raw, 8)
  if steps == 0 or agents == 0 or steps * agents > 1 << 30:
    raise ValueError("invalid trajectory dimensions")
  offset = 16
  opinion_size, sum_size = 4, 4
  if raw[:8] == b"SMPTRJ03":
    if len(raw) < offset + 2:
      raise ValueError("truncated trajectory precision")
    opinion_size, sum_size = struct.unpack_from("<BB", raw, offset)
    offset += 2
    if opinion_size not in (2, 4, 8) or sum_size not in (2, 4, 8):
      raise ValueError("invalid trajectory float precision")
  channels = []
  specs = [(f"<u{opinion_size}", (steps, agents)),
           ("<u2", (steps, agents, 4)),
           (f"<u{sum_size}", (steps, agents, 4))]
  for dtype, shape in specs:
    if len(raw) < offset + 5:
      raise ValueError("truncated trajectory channel header")
    codec, length = struct.unpack_from("<BI", raw, offset)
    offset += 5
    if codec not in (0, 1, 2, 3, 4, 5):
      raise ValueError("unknown trajectory channel codec")
    if length > len(raw) - offset:
      raise ValueError("truncated trajectory channel payload")
    if codec == 2:
      if length != 0:
        raise ValueError("zero channel has a payload")
      channels.append(np.zeros(shape, dtype=dtype))
      continue
    payload = raw[offset:offset + length]
    offset += length
    if codec in (4, 5):
      words = int(np.prod(shape))
      word_size = np.dtype(dtype).itemsize
      expected = word_size * words if codec == 4 else word_size * 8 * ((words + 7) // 8)
      block = zstandard.ZstdDecompressor().decompress(
          payload, max_output_size=expected)
      if len(block) != expected:
        raise ValueError("shuffled trajectory channel length mismatch")
      if codec == 4:
        bytes_ = np.frombuffer(block, dtype=np.uint8).reshape(word_size, words)
        restored = bytes_.T.copy().reshape(-1)
      else:
        planes = np.frombuffer(block, dtype=np.uint8).reshape(word_size * 8, -1)
        bits = np.unpackbits(planes, axis=1, bitorder="little")[:, :words]
        restored = np.packbits(bits.T.copy(), axis=1, bitorder="little").reshape(-1)
      channels.append(restored.view(dtype).reshape(shape))
      continue
    if codec == 3:
      if len(channels) != 0:
        raise ValueError("bitset codec is only valid for opinions")
      block = lz4.frame.decompress(payload)
      packed = np.frombuffer(block, dtype=np.uint8)
      if len(packed) != (steps * agents + 7) // 8:
        raise ValueError("bitset length mismatch")
      bits = np.unpackbits(packed, bitorder="little")[:steps * agents]
      float_type = np.dtype(dtype.replace("u", "f"))
      channels.append(bits.astype(float_type).view(dtype).reshape(shape))
      continue
    block = lz4.frame.decompress(payload)
    if len(block) != np.dtype(dtype).itemsize * int(np.prod(shape)):
      raise ValueError("trajectory channel length mismatch")
    arr = np.frombuffer(block, dtype=dtype).reshape(shape)
    if codec == 1:
      arr = np.bitwise_xor.accumulate(arr, axis=0)
    elif codec != 0:
      raise ValueError("unknown trajectory channel codec")
    channels.append(arr)
  if offset != len(raw):
    raise ValueError("trailing trajectory bytes")
  return {
      "opinions": channels[0].view(f"<f{opinion_size}"),
      "agent_numbers": channels[1].view("<i2"),
      "agent_opinion_sums": channels[2].view(f"<f{sum_size}"),
  }


class TrajectoryReader:
  def __init__(self, directory: str | Path):
    self.directory = Path(directory)
    manifest = json.loads((self.directory / "trajectory.json").read_text())
    if manifest.get("version") not in (2, 3):
      raise ValueError("unsupported trajectory version")
    self.agents = int(manifest["agents"])
    self.steps = int(manifest["next_step"])
    if self.agents <= 0 or self.steps < 0:
      raise ValueError("invalid trajectory dimensions")
    self.chunks = list(manifest["chunks"])
    precision = manifest.get("precision", {"opinions": "float32", "opinion_sums": "float32"})
    self.precision = precision
    legacy_chunks = int(manifest.get("legacy_chunks", len(self.chunks) if manifest["version"] == 2 else 0))
    if legacy_chunks < 0 or legacy_chunks > len(self.chunks):
      raise ValueError("invalid legacy chunk count")
    self.legacy_end_step = int(self.chunks[legacy_chunks - 1]["end"]) if legacy_chunks else -1
    self.dtypes = {"agent_numbers": np.dtype("int16")}
    for field, key in (("opinions", "opinions"), ("agent_opinion_sums", "opinion_sums")):
      value = precision[key]
      if value not in ("float16", "float32", "float64"):
        raise ValueError(f"invalid trajectory {key} precision")
      dtype = np.dtype(value)
      if legacy_chunks:
        dtype = np.result_type(dtype, np.float32)
      self.dtypes[field] = dtype
    next_step = 0
    for chunk in self.chunks:
      if int(chunk["start"]) != next_step or int(chunk["end"]) < next_step:
        raise ValueError("non-contiguous trajectory chunks")
      next_step = int(chunk["end"]) + 1
    if next_step != self.steps:
      raise ValueError("trajectory length mismatch")
    self._cached_index = -1
    self._cached: dict[str, Any] | None = None

  def _chunk(self, step: int) -> tuple[dict[str, Any], int]:
    if step < 0 or step >= self.steps:
      raise IndexError(step)
    # Locate the containing chunk without reading unrelated blocks.
    lo, hi = 0, len(self.chunks)
    while lo < hi:
      mid = (lo + hi) // 2
      if int(self.chunks[mid]["end"]) < step:
        lo = mid + 1
      else:
        hi = mid
    chunk = self.chunks[lo]
    if lo != self._cached_index:
      path = self.directory / chunk["file"]
      raw = path.read_bytes()
      if hashlib.sha256(raw).hexdigest() != chunk["sha256"]:
        raise ValueError(f"trajectory checksum mismatch: {path.name}")
      decoded = load_trajectory_chunk(raw)
      if decoded["opinions"].shape != (int(chunk["end"]) - int(chunk["start"]) + 1, self.agents):
        raise ValueError(f"trajectory shape mismatch: {path.name}")
      legacy = int(chunk["end"]) <= self.legacy_end_step
      for field, key in (("opinions", "opinions"), ("agent_opinion_sums", "opinion_sums")):
        precision = "float32" if legacy else self.precision[key]
        if decoded[field].dtype.itemsize != np.dtype(precision).itemsize:
          raise ValueError(f"trajectory {key} precision mismatch: {path.name}")
      self._cached = decoded
      self._cached_index = lo
    assert self._cached is not None
    return self._cached, step - int(chunk["start"])

  def row(self, field: str, step: int) -> np.ndarray:
    chunk, offset = self._chunk(step)
    return chunk[field][offset].astype(self.dtypes[field], copy=False)

  def opinion_precision_at(self, step: int) -> str:
    if step < 0 or step >= self.steps:
      raise IndexError(step)
    return "float32" if step <= self.legacy_end_step else self.precision["opinions"]

  def channel(self, field: str) -> LazyChannel:
    return LazyChannel(self, field)


class LazyChannel:
  def __init__(self, reader: TrajectoryReader, field: str):
    self.reader = reader
    self.field = field
    suffix = () if field == "opinions" else (4,)
    self.shape = (reader.steps, reader.agents, *suffix)
    self.dtype = reader.dtypes[field]

  def __len__(self) -> int:
    return self.shape[0]

  def __getitem__(self, key: Any) -> np.ndarray:
    if isinstance(key, tuple):
      step, *rest = key
    else:
      step, rest = key, []
    if isinstance(step, (int, np.integer)):
      index = int(step)
      if index < 0:
        index += len(self)
      row = self.reader.row(self.field, index)
      return row[tuple(rest)] if rest else row
    else:
      indices = range(*step.indices(len(self))) if isinstance(step, slice) else np.arange(len(self))[step]
      rows = [self.reader.row(self.field, int(i)) for i in indices]
      row = np.stack(rows) if rows else np.empty((0, *self.shape[1:]), dtype=self.dtype)
      return row[(slice(None), *rest)] if rest else row

  def __array__(self, dtype: Any = None, copy: bool | None = None) -> np.ndarray:
    rows = [self.reader.row(self.field, step) for step in range(len(self))]
    value = np.stack(rows) if rows else np.empty(self.shape, dtype=self.dtype)
    if dtype is not None:
      value = value.astype(dtype, copy=False)
    if copy:
      value = value.copy()
    return value
