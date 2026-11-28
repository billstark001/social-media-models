"""Shared synthetic saved-run fixtures for native analysis API tests."""
import json
import os
from pathlib import Path
import struct
import subprocess

import lz4.frame
import msgpack
import pytest


@pytest.fixture(scope="module")
def probe_binary(tmp_path_factory):
  configured = os.environ.get("SMP_PROBE_TEST_BINARY")
  if configured:
    return configured
  binary = tmp_path_factory.mktemp("analysis-probe") / "smp-probe.exe"
  subprocess.run(["go", "build", "-o", str(binary), "./cmd/smp-probe"],
                 cwd=Path(__file__).resolve().parents[1], check=True)
  return str(binary)


@pytest.fixture
def analysis_run(tmp_path):
  n, rounds = 53, 8
  metadata = {"DynamicsType": "HK", "UniqueName": tmp_path.name, "NodeCount": n,
              "PostRetainCount": 0,
              "HKParams": {"Tolerance": .65, "RewiringRate": 0, "RepostRate": 0}}
  (tmp_path / "metadata.json").write_text(json.dumps(metadata))
  opinions = []
  for step in range(rounds):
    opinions.extend([-.6] * 26 + [.6] * 26 + ([.95] if step == 4 else [0]))
  payload = struct.pack(f"<ii{n * rounds}f", rounds, n, *opinions)
  payload += bytes(rounds * n * (8 + 16))
  (tmp_path / "acc-state-2026-11-28T000000Z.lz4").write_bytes(lz4.frame.compress(payload))
  graph = {"directed": True, "nodes": {i: {} for i in range(n)},
           "adjacency": {i: {52 if i < 52 else 0: {}} for i in range(n)}, "graph": {}}
  (tmp_path / "graph-0.msgpack").write_bytes(msgpack.packb(graph, use_bin_type=True))
  return tmp_path
