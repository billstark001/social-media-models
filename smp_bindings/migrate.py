"""Explicit migration of historical metadata, snapshots, and event schemas."""

from __future__ import annotations

import json
import os
import pathlib
import sqlite3
import sys
import tempfile

import lz4.frame
import msgpack

_RECSYS_NAMES = {
    name.lower(): name for name in (
        "NoiseStd", "OpRandomNoiseStd", "UseCache", "Tolerance",
        "Steepness", "RandomRatio", "MixRate", "LogRecommendations",
    )
}


def _canonical_metadata(metadata: dict) -> bool:
  """Normalize obsolete RecSysParams spellings without selecting conflicting values."""
  params = metadata.get("RecSysParams")
  if not isinstance(params, dict):
    return False
  normalized = {}
  for key, value in params.items():
    canonical = _RECSYS_NAMES.get(key.lower(), key) if isinstance(key, str) else key
    if canonical in normalized and (
        normalized[canonical] != value
        or isinstance(normalized[canonical], bool) != isinstance(value, bool)
    ):
      raise ValueError(f"conflicting RecSysParams spellings for {canonical}")
    normalized[canonical] = value
  changed = normalized != params
  metadata["RecSysParams"] = normalized
  return changed


def _atomic_replace(path: pathlib.Path, data: bytes) -> None:
  descriptor, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
  try:
    os.chmod(temporary, path.stat().st_mode & 0o777)
    with os.fdopen(descriptor, "wb") as stream:
      stream.write(data)
      stream.flush()
      os.fsync(stream.fileno())
    os.replace(temporary, path)
  finally:
    if os.path.exists(temporary):
      os.unlink(temporary)


def migrate_metadata(path: str) -> None:
  """Normalize a metadata.json file while keeping its data version and RNG."""
  target = pathlib.Path(path)
  metadata = json.loads(target.read_text(encoding="utf8"))
  if not isinstance(metadata, dict):
    raise ValueError(f"metadata must be an object: {target}")
  if _canonical_metadata(metadata):
    _atomic_replace(target, (json.dumps(metadata, indent=2, ensure_ascii=False) + "\n").encode("utf8"))
    print(f"[migrated] metadata parameter names in {target}")
  else:
    print(f"[skip] metadata parameter names already canonical: {target}")


def migrate_snapshot(path: str, dynamics_type: str = "HK") -> None:
  """Wrap legacy model dumps and normalize embedded metadata and Posts names.

  Supports uncompressed msgpack and .lz4 checkpoints. Physical state, RNG
  positions, and existing envelope metadata are preserved.
  """
  target = pathlib.Path(path)
  encoded = target.read_bytes()
  compressed = target.suffix == ".lz4"
  payload = lz4.frame.decompress(encoded) if compressed else encoded
  unpacked = msgpack.unpackb(payload, raw=False, strict_map_key=False)
  if not isinstance(unpacked, dict):
    raise ValueError(f"snapshot must be a map: {target}")
  enveloped = "DynamicsType" in unpacked and "Data" in unpacked
  envelope = unpacked if enveloped else {"DynamicsType": dynamics_type}
  model = (
      msgpack.unpackb(unpacked["Data"], raw=False, strict_map_key=False)
      if enveloped else unpacked
  )
  if not isinstance(model, dict):
    raise ValueError(f"snapshot model must be a map: {target}")
  changed = not enveloped
  for old, new in (("Tweets", "Posts"), (b"Tweets", b"Posts")):
    if old in model:
      if new in model and model[new] != model[old]:
        raise ValueError("snapshot has conflicting Tweets and Posts")
      model[new] = model.pop(old)
      changed = True
  metadata = envelope.get("Metadata")
  if isinstance(metadata, dict):
    changed = _canonical_metadata(metadata) or changed
  if not changed:
    print(f"[skip] snapshot already canonical: {target}")
    return
  envelope["Data"] = msgpack.packb(model, use_bin_type=True)
  output = msgpack.packb(envelope, use_bin_type=True)
  _atomic_replace(target, lz4.frame.compress(output) if compressed else output)
  print(f"[migrated] snapshot in {target}")


def _columns(db: sqlite3.Connection, table: str) -> set[str]:
  return {row[1] for row in db.execute(f"PRAGMA table_info({table})")}


def _merge_renamed_table(
    db: sqlite3.Connection, tables: set[str], old: str, new: str, columns: tuple[str, ...]
) -> None:
  """Rename or merge a legacy table, rejecting conflicting rows before changes commit."""
  if old not in tables:
    return
  if new not in tables:
    db.execute(f"ALTER TABLE {old} RENAME TO {new}")
    tables.remove(old)
    tables.add(new)
    return
  conflicts = " OR ".join(f"old.{name} IS NOT new.{name}" for name in columns[1:])
  if db.execute(
      f"SELECT 1 FROM {old} AS old JOIN {new} AS new ON old.event_id=new.event_id "
      f"WHERE {conflicts} LIMIT 1"
  ).fetchone() is not None:
    raise ValueError(f"conflicting rows in {old} and {new}")
  fields = ",".join(columns)
  db.execute(f"INSERT OR IGNORE INTO {new} ({fields}) SELECT {fields} FROM {old}")
  db.execute(f"DROP TABLE {old}")
  tables.remove(old)


def migrate_events_db(path: str) -> None:
  """Migrate legacy tables and event names transactionally, recovering actor IDs."""
  target = pathlib.Path(path).resolve()
  # Opening a nonexistent path must not fabricate an empty input database.
  db = sqlite3.connect(target.as_uri() + "?mode=rw", uri=True)
  try:
    db.execute("BEGIN IMMEDIATE")
    tables = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
    if "events" not in tables:
      raise ValueError(f"missing events table: {target}")
    for table in ("tweet_events", "post_events"):
      if table not in tables:
        continue
      columns = _columns(db, table)
      if "is_retweet" in columns:
        if "is_repost" in columns:
          if db.execute(
              f"SELECT 1 FROM {table} WHERE is_retweet IS NOT is_repost LIMIT 1"
          ).fetchone() is not None:
            raise ValueError(f"conflicting is_retweet and is_repost values in {table}")
          db.execute(f"ALTER TABLE {table} DROP COLUMN is_retweet")
        else:
          db.execute(f"ALTER TABLE {table} RENAME COLUMN is_retweet TO is_repost")
    _merge_renamed_table(
        db, tables, "tweet_events", "post_events",
        ("event_id", "agent_id", "step", "opinion", "is_repost"),
    )
    _merge_renamed_table(
        db, tables, "view_tweets_events", "view_posts_events", ("event_id", "data")
    )
    if "rewiring_events" in tables:
      missing = db.execute(
          "SELECT 1 FROM rewiring_events r LEFT JOIN events e ON e.id=r.event_id "
          "WHERE e.id IS NULL LIMIT 1"
      ).fetchone()
      if missing is not None:
        raise ValueError("rewiring body has no parent event")
      if "agent_id" not in _columns(db, "rewiring_events"):
        db.execute("ALTER TABLE rewiring_events ADD COLUMN agent_id INTEGER NOT NULL DEFAULT 0")
      # The event header has always identified the acting agent. Also repair the
      # zero sentinel written by older versions of this migration.
      db.execute(
          "UPDATE rewiring_events SET agent_id=(SELECT e.agent_id FROM events e "
          "WHERE e.id=rewiring_events.event_id)"
      )
    db.execute("UPDATE events SET type='Post' WHERE type='Tweet'")
    db.execute("UPDATE events SET type='ViewPosts' WHERE type='ViewTweets'")
    db.commit()
    print(f"[migrated] event schema in {target}")
  except BaseException:
    db.rollback()
    raise
  finally:
    db.close()


def _usage() -> None:
  print(
      "Usage:\n"
      "  smp-migrate metadata <metadata.json> [<metadata2.json> ...]\n"
      "  smp-migrate snapshot <snapshot.msgpack[.lz4]> [...] [--dynamics HK|Deffuant|Galam|Voter]\n"
      "  smp-migrate events <events.db> [<events2.db> ...]\n"
  )


def main(argv: list[str] | None = None) -> None:
  args = argv if argv is not None else sys.argv[1:]
  if len(args) < 2:
    _usage()
    sys.exit(1)
  command, *files = args
  dynamics_type = "HK"
  if command == "snapshot" and "--dynamics" in files:
    index = files.index("--dynamics")
    if index + 1 >= len(files):
      raise ValueError("--dynamics requires a value")
    dynamics_type = files[index + 1]
    del files[index:index + 2]
  if not files:
    raise ValueError("at least one input file is required")
  for path in files:
    if command == "metadata":
      migrate_metadata(path)
    elif command == "snapshot":
      migrate_snapshot(path, dynamics_type=dynamics_type)
    elif command == "events":
      migrate_events_db(path)
    else:
      _usage()
      sys.exit(1)


if __name__ == "__main__":
  main()
