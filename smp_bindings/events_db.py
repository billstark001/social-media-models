import sqlite3
from collections.abc import Iterator
from contextlib import closing
from dataclasses import dataclass
from pathlib import Path
from typing import Any, TypeVar

import msgpack


@dataclass
class PostRecord:
  agent_id: int
  step: int
  opinion: float  # Boolean dynamics use 0.0/1.0 in event storage.


@dataclass
class RewiringEventBody:
  agent_id: int
  unfollow: int
  follow: int


@dataclass
class PostEventBody:
  record: PostRecord
  is_repost: bool


@dataclass
class ViewPostsEventBody:
  neighbor_concordant: list[PostRecord]
  neighbor_discordant: list[PostRecord]
  recommended_concordant: list[PostRecord]
  recommended_discordant: list[PostRecord]
  details: dict | None = None


@dataclass
class EventRecord:
  id: int
  type: str
  agent_id: int
  step: int
  body: Any | None = None


def unpack_view_posts_data(blob) -> ViewPostsEventBody:
  d = msgpack.unpackb(blob, raw=False)

  def to_records(lst):
    return [
        PostRecord(post["AgentID"], post["Step"], post["Opinion"]) if isinstance(post, dict) else PostRecord(*post)
        for post in (lst or [])
    ]
  return ViewPostsEventBody(
      neighbor_concordant=to_records(d.get('NeighborConcordant', [])),
      neighbor_discordant=to_records(d.get('NeighborDiscordant', [])),
      recommended_concordant=to_records(d.get('RecommendedConcordant', [])),
      recommended_discordant=to_records(d.get('RecommendedDiscordant', [])),
      details=d.get('Details'),
  )


def get_events_by_step_range(
    db: sqlite3.Connection,
    a: int,
    b: int,
    type_: str | None = None
) -> list[EventRecord]:
  """Read event headers for [a, b), ordered by completed step and event ID."""
  query = "SELECT id, type, agent_id, step FROM events WHERE step >= ? AND step < ?"
  params: tuple = (a, b)
  if type_:
    query += " AND type = ?"
    params += (type_,)
  with closing(db.execute(query + " ORDER BY step, id", params)) as cur:
    rows = cur.fetchall()
  return [EventRecord(id=row[0], type=row[1], agent_id=row[2], step=row[3]) for row in rows]


def iter_rewiring_events(
    db: sqlite3.Connection, start: int, stop: int
) -> Iterator[tuple[int, int, int, int]]:
  """Stream (event_id, agent_id, unfollow, follow) in commit order for [start, stop)."""
  cursor = db.execute(
      """
      SELECT e.id, e.agent_id, r.unfollow, r.follow
      FROM events AS e
      LEFT JOIN rewiring_events AS r ON r.event_id = e.id
      WHERE e.type = 'Rewiring' AND e.step >= ? AND e.step < ?
      ORDER BY e.step, e.id
      """,
      (start, stop),
  )
  try:
    for event_id, agent_id, unfollow, follow in cursor:
      if unfollow is None or follow is None:
        raise ValueError(f"missing rewiring body for event {event_id}")
      yield event_id, agent_id, unfollow, follow
  finally:
    cursor.close()


def get_post_events_by_agent_step(
    db: sqlite3.Connection,
    agent_id: int,
    step: int
) -> list[EventRecord]:
  """Find the emissions of a post identified by its original author and step."""
  with closing(db.execute("""
    SELECT e.id, e.type, e.agent_id, e.step, t.is_repost, t.opinion
    FROM events e
    JOIN post_events t ON e.id = t.event_id
    WHERE t.agent_id = ? AND t.step = ?
    ORDER BY e.step, e.id
  """, (agent_id, step))) as cur:
    rows = cur.fetchall()
  return [
      EventRecord(
          id=row[0], type=row[1], agent_id=row[2], step=row[3],
          body=PostEventBody(
              record=PostRecord(agent_id=agent_id, step=step, opinion=row[5]),
              is_repost=bool(row[4])
          )
      )
      for row in rows
  ]


def get_events_by_step_type(
    db: sqlite3.Connection,
    step: int,
    type_: str,
    agent_id: int | None = None
) -> list[EventRecord]:
  query = "SELECT id, type, agent_id, step FROM events WHERE step=? AND type=?"
  params: tuple = (step, type_)
  if agent_id is not None:
    query += " AND agent_id=?"
    params += (agent_id,)
  with closing(db.execute(query + " ORDER BY id", params)) as cur:
    rows = cur.fetchall()
  return [EventRecord(id=row[0], type=row[1], agent_id=row[2], step=row[3]) for row in rows]


def get_view_posts_event_body(
    db: sqlite3.Connection,
    event_id: int
) -> ViewPostsEventBody | None:
  with closing(db.execute(
      "SELECT data FROM view_posts_events WHERE event_id = ?", (event_id,))) as cur:
    row = cur.fetchone()
  if row:
    return unpack_view_posts_data(row[0])
  return None


def iter_view_posts(db: sqlite3.Connection, start: int, stop: int) -> Iterator[EventRecord]:
  """Stream canonical view-post events, preserving their logged coverage."""
  with closing(db.execute("SELECT name FROM sqlite_master WHERE type = 'table'")) as cursor:
    tables = {row[0] for row in cursor}
  if "view_tweets_events" in tables:
    raise ValueError("obsolete ViewTweets schema; run smp-migrate events <events.db>")
  if "view_posts_events" not in tables:
    with closing(db.execute(
        "SELECT id FROM events WHERE type = 'ViewPosts' AND step >= ? AND step < ? LIMIT 1",
        (start, stop),
    )) as cursor:
      if cursor.fetchone() is not None:
        raise ValueError("view-post events are missing their body table")
    return
  cursor = db.execute(
      "SELECT e.id, e.agent_id, e.step, v.data, e.type FROM events e "
      "LEFT JOIN view_posts_events v ON v.event_id=e.id "
      "WHERE e.type IN ('ViewPosts','ViewTweets') AND e.step>=? AND e.step<? ORDER BY e.step,e.id",
      (start, stop),
  )
  try:
    for event_id, agent_id, step, payload, kind in cursor:
      if kind != "ViewPosts":
        raise ValueError("obsolete ViewTweets schema; run smp-migrate events <events.db>")
      if payload is None:
        raise ValueError(f"missing view-post body for event {event_id}")
      yield EventRecord(event_id, "ViewPosts", agent_id, step, unpack_view_posts_data(payload))
  finally:
    cursor.close()


def get_rewiring_event_body(
    db: sqlite3.Connection,
    event_id: int
) -> RewiringEventBody | None:
  with closing(db.execute(
      "SELECT agent_id, unfollow, follow FROM rewiring_events WHERE event_id = ?", (event_id,))) as cur:
    row = cur.fetchone()
  if row:
    return RewiringEventBody(agent_id=row[0], unfollow=row[1], follow=row[2])
  return None


def get_post_event_body(
    db: sqlite3.Connection,
    event_id: int
) -> PostEventBody | None:
  with closing(db.execute(
      "SELECT agent_id, step, opinion, is_repost FROM post_events WHERE event_id = ?", (event_id,))) as cur:
    row = cur.fetchone()
  if row:
    return PostEventBody(
        record=PostRecord(agent_id=row[0], step=row[1], opinion=row[2]),
        is_repost=bool(row[3])
    )
  return None


def load_event_body(
    db: sqlite3.Connection,
    event: EventRecord
) -> EventRecord:
  if event.type == "Rewiring":
    body = get_rewiring_event_body(db, event.id)
  elif event.type == "Post":
    body = get_post_event_body(db, event.id)
  elif event.type == "ViewPosts":
    body = get_view_posts_event_body(db, event.id)
  else:
    body = None
  event.body = body
  return event


T = TypeVar('T')


def get_batch_iterator(
    lst: list[T],
    size: int,
) -> Iterator[list[T]]:
  if size <= 0:
    raise ValueError("batch size must be positive")
  for i in range(0, len(lst), size):
    yield lst[i:i + size]


def batch_load_event_bodies(
    db: sqlite3.Connection,
    events: list[EventRecord],
    event_type: str | None = None,
    max_batch_size: int = 1000,
) -> list[EventRecord]:
  if not events:
    return []
  event_type = event_type or events[0].type
  if any(event.type != event_type for event in events):
    raise ValueError("batch event bodies require one event type")
  if max_batch_size <= 0:
    raise ValueError("max_batch_size must be positive")
  event_ids = [e.id for e in events]
  id2body = {}

  # Keep batches within SQLite's baseline parameter limit.
  event_ids_itr = get_batch_iterator(event_ids, min(max_batch_size, 999))
  with closing(db.cursor()) as cur:
    if event_type == "Rewiring":
      for batch in event_ids_itr:
        sql = f"SELECT event_id, agent_id, unfollow, follow FROM rewiring_events WHERE event_id IN ({','.join(['?']*len(batch))})"
        cur.execute(sql, batch)
        for eid, aid, unf, fol in cur.fetchall():
          id2body[eid] = RewiringEventBody(agent_id=aid, unfollow=unf, follow=fol)
    elif event_type == "Post":
      for batch in event_ids_itr:
        sql = f"SELECT event_id, agent_id, step, opinion, is_repost FROM post_events WHERE event_id IN ({','.join(['?']*len(batch))})"
        cur.execute(sql, batch)
        for eid, aid, step, op, is_rp in cur.fetchall():
          id2body[eid] = PostEventBody(
              record=PostRecord(agent_id=aid, step=step, opinion=op),
              is_repost=bool(is_rp)
          )
    elif event_type == "ViewPosts":
      for batch in event_ids_itr:
        sql = f"SELECT event_id, data FROM view_posts_events WHERE event_id IN ({','.join(['?']*len(batch))})"
        cur.execute(sql, batch)
        for eid, blob in cur.fetchall():
          id2body[eid] = unpack_view_posts_data(blob)

  out = []
  for e in events:
    e.body = id2body.get(e.id)
    out.append(e)
  return out


def load_events_db(filename: str):
  """Open an existing event database without allowing writes or file creation."""
  return sqlite3.connect(Path(filename).resolve().as_uri() + "?mode=ro", uri=True)
