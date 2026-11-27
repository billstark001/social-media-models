import sqlite3
import struct

import lz4.frame
import msgpack
import numpy as np
import pytest

from smp_bindings.events_db import (
    EventRecord,
    batch_load_event_bodies,
    get_events_by_step_range,
    iter_view_posts,
    load_events_db,
)
from smp_bindings.model_state import load_accumulative_model_state
from smp_bindings.record import RawSimulationRecord
from smp_bindings.domain import parse_metadata


def write_legacy_state(path):
  opinions = np.array([[-0.75, 0, 0.75], [-0.5, 0.25, 0.5]], dtype="<f4")
  counts = np.arange(24, dtype="<i2").reshape(2, 3, 4)
  sums = (np.arange(24, dtype="<f4") / 4).reshape(2, 3, 4)
  raw = struct.pack("<ii", 2, 3) + opinions.tobytes() + counts.tobytes() + sums.tobytes()
  path.write_bytes(lz4.frame.compress(raw))
  return opinions, counts, sums


def test_legacy_reader_selects_sums_without_counts(tmp_path):
  path = tmp_path / "state.lz4"
  opinions, counts, sums = write_legacy_state(path)
  selected = load_accumulative_model_state(str(path), with_agent_opinion_sums=True)
  complete = load_accumulative_model_state(str(path), with_agent_numbers=True, with_agent_opinion_sums=True)
  assert selected.agent_numbers is None
  np.testing.assert_array_equal(selected.opinions, opinions)
  np.testing.assert_array_equal(selected.agent_opinion_sums, sums)
  np.testing.assert_array_equal(complete.agent_numbers, counts)
  assert selected.opinions.dtype == np.dtype("float32")
  assert selected.agent_opinion_sums.dtype == np.dtype(float)
  assert complete.agent_numbers.dtype == np.dtype(int)

  raw = lz4.frame.decompress(path.read_bytes())
  for invalid in (raw[:7], raw[:-1], raw + b"extra", struct.pack("<ii", -1, 3)):
    path.write_bytes(lz4.frame.compress(invalid))
    with pytest.raises(ValueError):
      load_accumulative_model_state(str(path))


def test_events_reader_is_read_only_and_detects_missing_bodies(tmp_path):
  path = tmp_path / "events.db"
  with pytest.raises(sqlite3.OperationalError):
    load_events_db(str(path))
  assert not path.exists()
  db = sqlite3.connect(path)
  try:
    db.executescript("""
      CREATE TABLE events(id INTEGER PRIMARY KEY, type TEXT, agent_id INTEGER, step INTEGER);
      CREATE TABLE view_posts_events(event_id INTEGER PRIMARY KEY, data BLOB);
      INSERT INTO events VALUES (1, 'ViewPosts', 0, 2), (2, 'ViewPosts', 1, 1);
    """)
    db.execute("INSERT INTO view_posts_events VALUES (?, ?)", (2, msgpack.packb({})))
    db.commit()
  finally:
    db.close()
  reader = load_events_db(str(path))
  try:
    assert [event.id for event in get_events_by_step_range(reader, 0, 3)] == [2, 1]
    assert [event.id for event in iter_view_posts(reader, 1, 2)] == [2]
    with pytest.raises(ValueError, match="missing view-post body for event 1"):
      list(iter_view_posts(reader, 0, 3))
    with pytest.raises(ValueError, match="one event type"):
      batch_load_event_bodies(reader, [EventRecord(1, "Post", 0, 1), EventRecord(2, "Rewiring", 0, 1)])
    with pytest.raises(ValueError, match="must be positive"):
      batch_load_event_bodies(reader, [EventRecord(1, "Post", 0, 1)], max_batch_size=-1)
    with pytest.raises(sqlite3.OperationalError, match="readonly"):
      reader.execute("DELETE FROM events")
  finally:
    reader.close()


def test_raw_record_restores_isolated_agents_and_closes_failed_load(tmp_path, monkeypatch):
  run = tmp_path / "run"
  run.mkdir()
  write_legacy_state(run / "acc-state-0.lz4")
  (run / "finished-0.msgpack").touch()
  sqlite3.connect(run / "events.db").close()
  graph = {"directed": True, "nodes": {0: {}, 2: {}}, "adjacency": {0: {2: {}}}}
  (run / "graph-0.msgpack").write_bytes(msgpack.packb(graph))
  metadata = {"UniqueName": "run", "HKParams": {"RewiringRate": 0}}
  record = RawSimulationRecord(str(tmp_path), parse_metadata(metadata))
  with record:
    assert set(record.get_graph(0)) == {0, 1, 2}
    assert record.followers.tolist() == [1, 0, 0]
    assert [(step, set(snapshot)) for step, snapshot in record.iter_graphs([0, 1])] == [(0, {0, 1, 2}), (1, {0, 1, 2})]
  record.dispose()
  assert record.events_db is None

  (run / "graph-0.msgpack").rename(run / "graph-1.msgpack")
  record = RawSimulationRecord(str(tmp_path), parse_metadata(metadata))
  connection = sqlite3.connect(":memory:")
  monkeypatch.setattr("smp_bindings.record.load_events_db", lambda _: connection)
  with pytest.raises(ValueError, match="initial graph snapshot"):
    record.load()
  assert record.events_db is None
  with pytest.raises(sqlite3.ProgrammingError, match="closed"):
    connection.execute("SELECT 1")


def test_raw_record_selects_latest_numeric_completed_step(tmp_path):
  run = tmp_path / "run"
  run.mkdir()
  for step in (2, 10):
    cells = (step + 1) * 3
    raw = struct.pack("<ii", step + 1, 3) + bytes(cells * (4 + 8 + 16))
    (run / f"acc-state-{step}.lz4").write_bytes(lz4.frame.compress(raw))
  (run / "finished-10.msgpack").touch()
  sqlite3.connect(run / "events.db").close()
  (run / "graph-0.msgpack").write_bytes(msgpack.packb({
      "directed": True, "nodes": {0: {}, 1: {}, 2: {}}, "adjacency": {},
  }))
  with RawSimulationRecord(str(tmp_path), parse_metadata({"UniqueName": "run"})) as record:
    assert record.max_step == 10
    assert record.acc_state_path.endswith("acc-state-10.lz4")


def test_raw_record_selects_latest_utc_generation_and_uses_header_population(tmp_path):
  run = tmp_path / "run"
  run.mkdir()
  for stamp, step in (("2026-11-02T000000Z", 10), ("2026-03-22T050705Z", 2)):
    cells = (step + 1) * 3
    raw = struct.pack("<ii", step + 1, 3) + bytes(cells * (4 + 8 + 16))
    (run / f"acc-state-{stamp}.lz4").write_bytes(lz4.frame.compress(raw))
  (run / "finished-2026-11-02T000001Z.msgpack").touch()
  sqlite3.connect(run / "events.db").close()
  (run / "graph-0.msgpack").write_bytes(msgpack.packb({
      "directed": True, "nodes": {0: {}, 2: {}}, "adjacency": {},
  }))
  with RawSimulationRecord(str(tmp_path), parse_metadata({"UniqueName": "run"})) as record:
    assert record.max_step == 10 and record.agents == 3
    assert record.acc_state_path.endswith("acc-state-2026-11-02T000000Z.lz4")
    assert set(record.get_graph(0)) == {0, 1, 2}
    assert record.metadata.NodeCount is None
  with pytest.raises(ValueError, match="NodeCount"):
    with RawSimulationRecord(str(tmp_path), parse_metadata({"UniqueName": "run", "NodeCount": 4})):
      pass


@pytest.mark.parametrize("names", [
    ["acc-state-2.lz4", "acc-state-2026-03-22T050705Z.lz4"],
    ["acc-state-2.lz4", "acc-state-02.lz4"],
    ["acc-state-2026-02-30T050705Z.lz4"],
])
def test_raw_record_rejects_ambiguous_or_invalid_generations(tmp_path, names):
  run = tmp_path / "run"
  run.mkdir()
  for name in names:
    (run / name).touch()
  with pytest.raises(ValueError):
    RawSimulationRecord(str(tmp_path), parse_metadata({"UniqueName": "run"}))
