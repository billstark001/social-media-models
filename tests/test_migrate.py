"""Explicit compatibility conversion preserves state and rejects ambiguous inputs."""

import json
import sqlite3

import lz4.frame
import msgpack
import pytest

from smp_bindings.events_db import iter_view_posts
from smp_bindings.migrate import migrate_events_db, migrate_metadata, migrate_snapshot


def test_metadata_migration_preserves_rng_and_rejects_conflicting_aliases(tmp_path):
  path = tmp_path / "metadata.json"
  data = {
      "DataVersion": 2, "RNG": {"Seed1": "0x1"},
      "RecSysParams": {"mixRate": 0.2, "NoiseStd": 0.1},
  }
  path.write_text(json.dumps(data))
  migrate_metadata(str(path))
  actual = json.loads(path.read_text())
  assert actual == {**data, "RecSysParams": {"MixRate": 0.2, "NoiseStd": 0.1}}
  canonical = path.read_bytes()
  migrate_metadata(str(path))
  assert path.read_bytes() == canonical

  path.write_text(json.dumps({"RecSysParams": {"mixRate": 0.2, "MixRate": 0.3}}))
  conflict = path.read_bytes()
  with pytest.raises(ValueError, match="conflicting"):
    migrate_metadata(str(path))
  assert path.read_bytes() == conflict


def test_snapshot_migration_normalizes_embedded_aliases_and_keeps_stream_positions(tmp_path):
  path = tmp_path / "checkpoint.msgpack.lz4"
  model = {
      "CurStep": 3, "Opinions": [-0.5, 0.5], "Tweets": {0: [{"AgentID": 0, "Step": 2, "Opinion": -0.5}]},
      "RNGStates": {"schedule": b"opaque_rng"}, "RecsysDumpData": b"opaque_cache",
  }
  envelope = {
      "DynamicsType": "HK", "Data": msgpack.packb(model, use_bin_type=True),
      "Metadata": {"RecSysParams": {"noiseStd": 0.1}}, "CompletedStep": 2, "StableSteps": 1,
  }
  path.write_bytes(lz4.frame.compress(msgpack.packb(envelope, use_bin_type=True)))
  migrate_snapshot(str(path))
  actual = msgpack.unpackb(lz4.frame.decompress(path.read_bytes()), raw=False, strict_map_key=False)
  inner = msgpack.unpackb(actual["Data"], raw=False, strict_map_key=False)
  assert inner == {**{key: value for key, value in model.items() if key != "Tweets"}, "Posts": model["Tweets"]}
  assert actual["Metadata"] == {"RecSysParams": {"NoiseStd": 0.1}}
  assert actual["CompletedStep"] == 2 and actual["StableSteps"] == 1
  canonical = path.read_bytes()
  migrate_snapshot(str(path))
  assert path.read_bytes() == canonical


def test_event_migration_merges_names_and_recovers_actor_from_header(tmp_path):
  path = tmp_path / "events.db"
  with sqlite3.connect(path) as db:
    db.executescript("""
      CREATE TABLE events(id INTEGER PRIMARY KEY, type TEXT, agent_id INTEGER, step INTEGER);
      CREATE TABLE rewiring_events(event_id INTEGER PRIMARY KEY, unfollow INTEGER, follow INTEGER);
      CREATE TABLE view_tweets_events(event_id INTEGER PRIMARY KEY, data BLOB);
      CREATE TABLE view_posts_events(event_id INTEGER PRIMARY KEY, data BLOB);
      INSERT INTO events VALUES (1,'ViewTweets',5,2), (2,'Rewiring',8,2);
      INSERT INTO rewiring_events VALUES (2,3,4);
    """)
    body = msgpack.packb({"NeighborConcordant": [[3, 1, 0.1]]})
    db.execute("INSERT INTO view_tweets_events VALUES (1,?)", (body,))
    with pytest.raises(ValueError, match="smp-migrate events"):
      list(iter_view_posts(db, 0, 3))
  migrate_events_db(str(path))
  with sqlite3.connect(path) as db:
    assert db.execute("SELECT agent_id FROM rewiring_events WHERE event_id=2").fetchone() == (8,)
    [view] = iter_view_posts(db, 0, 3)
    assert view.agent_id == 5 and view.body.neighbor_concordant[0].agent_id == 3
    db.execute("UPDATE rewiring_events SET agent_id=0")
  migrate_events_db(str(path))
  with sqlite3.connect(path) as db:
    assert db.execute("SELECT agent_id FROM rewiring_events WHERE event_id=2").fetchone() == (8,)


def test_event_migration_conflict_rolls_back_every_rename(tmp_path):
  path = tmp_path / "events.db"
  with sqlite3.connect(path) as db:
    db.executescript("""
      CREATE TABLE events(id INTEGER PRIMARY KEY, type TEXT, agent_id INTEGER, step INTEGER);
      CREATE TABLE tweet_events(event_id INTEGER PRIMARY KEY, agent_id INTEGER, step INTEGER, opinion REAL, is_retweet BOOLEAN);
      CREATE TABLE view_tweets_events(event_id INTEGER PRIMARY KEY, data BLOB);
      CREATE TABLE view_posts_events(event_id INTEGER PRIMARY KEY, data BLOB);
      INSERT INTO events VALUES (1,'ViewTweets',5,2);
      INSERT INTO view_tweets_events VALUES (1,x'01');
      INSERT INTO view_posts_events VALUES (1,x'02');
    """)
  with pytest.raises(ValueError, match="conflicting"):
    migrate_events_db(str(path))
  with sqlite3.connect(path) as db:
    assert db.execute("SELECT type FROM events").fetchone() == ("ViewTweets",)
    assert {row[1] for row in db.execute("PRAGMA table_info(tweet_events)")} >= {"is_retweet"}
    assert db.execute("SELECT data FROM view_tweets_events").fetchone() == (b"\x01",)
