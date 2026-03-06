from typing import Optional, List, Any, TypeVar

import sqlite3
import msgpack
from dataclasses import dataclass

# ---------------- 数据模型 ----------------


@dataclass
class PostRecord:
  agent_id: int
  step: int
  opinion: float  # bool 类型 dynamics 以 0.0/1.0 存储


@dataclass
class RewiringEventBody:
  unfollow: int
  follow: int


@dataclass
class PostEventBody:
  record: PostRecord
  is_repost: bool


@dataclass
class ViewPostsEventBody:
  neighbor_concordant: List[PostRecord]
  neighbor_discordant: List[PostRecord]
  recommended_concordant: List[PostRecord]
  recommended_discordant: List[PostRecord]


@dataclass
class EventRecord:
  id: int
  type: str
  agent_id: int
  step: int
  body: Optional[Any] = None  # 可选事件体

# ---------------- 工具函数 ----------------


def unpack_view_posts_data(blob) -> ViewPostsEventBody:
  d = msgpack.unpackb(blob, raw=False)

  def to_records(lst):
    return [PostRecord(*t) for t in lst]
  return ViewPostsEventBody(
      neighbor_concordant=to_records(d.get('NeighborConcordant', [])),
      neighbor_discordant=to_records(d.get('NeighborDiscordant', [])),
      recommended_concordant=to_records(d.get('RecommendedConcordant', [])),
      recommended_discordant=to_records(d.get('RecommendedDiscordant', [])),
  )

# ---------------- 查询函数 ----------------

# 2.1 查找 step 在 [a, b) 的所有事件（可选type）


def get_events_by_step_range(
    db: sqlite3.Connection,
    a: int,
    b: int,
    type_: Optional[str] = None
) -> List[EventRecord]:
  cur = db.cursor()
  if type_:
    cur.execute(
        "SELECT id, type, agent_id, step FROM events WHERE step >= ? AND step < ? AND type = ?", (a, b, type_))
  else:
    cur.execute(
        "SELECT id, type, agent_id, step FROM events WHERE step >= ? AND step < ?", (a, b))
  rows = cur.fetchall()
  return [EventRecord(id=row[0], type=row[1], agent_id=row[2], step=row[3]) for row in rows]

# 2.2 查找一条帖子（agent_id, step）所有被发送和转发的事件


def get_post_events_by_agent_step(
    db: sqlite3.Connection,
    agent_id: int,
    step: int
) -> List[EventRecord]:
  cur = db.cursor()
  cur.execute("""
    SELECT e.id, e.type, e.agent_id, e.step, t.is_repost, t.opinion
    FROM events e
    JOIN post_events t ON e.id = t.event_id
    WHERE t.agent_id = ? AND t.step = ?
  """, (agent_id, step))
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

# 2.3 查找指定 step，type，可选 agent 的所有事件


def get_events_by_step_type(
    db: sqlite3.Connection,
    step: int,
    type_: str,
    agent_id: Optional[int] = None
) -> List[EventRecord]:
  cur = db.cursor()
  if agent_id is not None:
    cur.execute("SELECT id, type, agent_id, step FROM events WHERE step=? AND type=? AND agent_id=?",
                (step, type_, agent_id))
  else:
    cur.execute(
        "SELECT id, type, agent_id, step FROM events WHERE step=? AND type=?", (step, type_))
  rows = cur.fetchall()
  return [EventRecord(id=row[0], type=row[1], agent_id=row[2], step=row[3]) for row in rows]


def get_view_posts_event_body(
    db: sqlite3.Connection,
    event_id: int
) -> Optional[ViewPostsEventBody]:
  cur = db.cursor()
  cur.execute(
      "SELECT data FROM view_posts_events WHERE event_id = ?", (event_id,))
  row = cur.fetchone()
  if row:
    return unpack_view_posts_data(row[0])
  return None


def get_rewiring_event_body(
    db: sqlite3.Connection,
    event_id: int
) -> Optional[RewiringEventBody]:
  cur = db.cursor()
  cur.execute(
      "SELECT unfollow, follow FROM rewiring_events WHERE event_id = ?", (event_id,))
  row = cur.fetchone()
  if row:
    return RewiringEventBody(unfollow=row[0], follow=row[1])
  return None


def get_post_event_body(
    db: sqlite3.Connection,
    event_id: int
) -> Optional[PostEventBody]:
  cur = db.cursor()
  cur.execute(
      "SELECT agent_id, step, opinion, is_repost FROM post_events WHERE event_id = ?", (event_id,))
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
    lst: List[T],
    size: int,
):
  for i in range(0, len(lst), size):
    yield lst[i:i + size]


def batch_load_event_bodies(
    db: sqlite3.Connection,
    events: List[EventRecord],
    event_type: Optional[str] = None,
    max_batch_size: int = 1000,
) -> List[EventRecord]:
  if not events:
    return []
  event_type = event_type or events[0].type  # 假设同类
  event_ids = [e.id for e in events]
  id2body = {}

  cur = db.cursor()

  event_ids_itr = get_batch_iterator(event_ids, max_batch_size)

  if event_type == "Rewiring":
    # 批量查 rewiring_events
    for batch in event_ids_itr:
      sql = f"SELECT event_id, unfollow, follow FROM rewiring_events WHERE event_id IN ({','.join(['?']*len(batch))})"
      cur.execute(sql, batch)
      for eid, unf, fol in cur.fetchall():
        id2body[eid] = RewiringEventBody(unfollow=unf, follow=fol)
  elif event_type == "Post":
    # 批量查 post_events
    for batch in event_ids_itr:
      sql = f"SELECT event_id, agent_id, step, opinion, is_repost FROM post_events WHERE event_id IN ({','.join(['?']*len(batch))})"
      cur.execute(sql, batch)
      for eid, aid, step, op, is_rp in cur.fetchall():
        id2body[eid] = PostEventBody(
            record=PostRecord(agent_id=aid, step=step, opinion=op),
            is_repost=bool(is_rp)
        )
  elif event_type == "ViewPosts":
    # 批量查 view_posts_events
    for batch in event_ids_itr:
      sql = f"SELECT event_id, data FROM view_posts_events WHERE event_id IN ({','.join(['?']*len(batch))})"
      cur.execute(sql, batch)
      for eid, blob in cur.fetchall():
        id2body[eid] = unpack_view_posts_data(blob)
  else:
    # 其他类型不处理
    pass

  # 结果合成
  out = []
  for e in events:
    e.body = id2body.get(e.id)
    out.append(e)
  return out


def load_events_db(filename: str):
  return sqlite3.connect(filename)

# ---------------- 示例用法 ----------------


if __name__ == "__main__":
  db = sqlite3.connect("your.db")
  # 查询 step 在 [10, 20) 的所有 Post 事件，并加载 body
  events = get_events_by_step_range(db, 10, 20, type_="Post")
  events_with_body = [load_event_body(db, e) for e in events]
  for e in events_with_body:
    print(e)
  db.close()
