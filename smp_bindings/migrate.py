"""迁移工具：将 v1/v2 的仿真产物升级到 v2/v3 格式。

用法示例
--------
从命令行批量迁移 msgpack 快照::

    python -m smp_bindings.migrate snapshot ./run/my-sim/snapshot-*.msgpack

迁移 SQLite 事件数据库::

    python -m smp_bindings.migrate events ./run/my-sim/events.db
"""

from __future__ import annotations

import pathlib
import sqlite3
import sys

import msgpack


# ---------------------------------------------------------------------------
# msgpack 快照迁移
# ---------------------------------------------------------------------------


def migrate_snapshot(path: str, dynamics_type: str = "HK") -> None:
  """将 v1/v2 快照文件原地升级为 v3 格式（RawSnapshotData 信封）。

  - v1→v2：将顶层 ``Tweets`` 键重命名为 ``Posts``。
  - v2→v3：将内容包装进 ``{DynamicsType, Data}`` 信封。

  Parameters
  ----------
  path:
      ``*.msgpack`` 文件路径。
  dynamics_type:
      动力学类型字符串，默认为 ``"HK"``。
      数据来自旧 Deffuant 仿真时应传 ``"Deffuant"``，以此类推。
  """
  data = pathlib.Path(path).read_bytes()
  inner = msgpack.unpackb(data, raw=False)

  # 检测是否已经是 v3 信封格式
  if isinstance(inner, dict) and "DynamicsType" in inner and "Data" in inner:
    print(f"[skip] already v3 format: {path}")
    return

  # v1→v2：Tweets → Posts
  if b"Tweets" in inner:
    inner[b"Posts"] = inner.pop(b"Tweets")

  # v2→v3：包装进信封
  inner_bytes = msgpack.packb(inner)
  envelope = {"DynamicsType": dynamics_type, "Data": inner_bytes}
  pathlib.Path(path).write_bytes(msgpack.packb(envelope))  # type: ignore
  print(f"[migrated] {path}")


# ---------------------------------------------------------------------------
# SQLite 事件数据库迁移
# ---------------------------------------------------------------------------


def migrate_events_db(path: str) -> None:
  """将 v1 ``events.db`` 数据库原地升级为 v2 格式。

  执行以下操作：

  - 将 ``tweet_events`` 表重命名为 ``post_events``。
  - 将 ``is_retweet`` 列重命名为 ``is_repost``。
  - 为 ``rewiring_events`` 添加 ``agent_id INTEGER NOT NULL DEFAULT 0`` 列。
    ``agent_id = 0`` 是哨兵值，表示该行在迁移前写入，实际施动智能体已无法还原；
    分析时可将其视为"未知"。
  - 将 ``view_tweets_events`` 表重命名为 ``view_posts_events``。
  - 将 ``events.type`` 中的 ``'Tweet'`` 更新为 ``'Post'``。
  - 将 ``events.type`` 中的 ``'ViewTweets'`` 更新为 ``'ViewPosts'``。

  操作在事务中执行，失败会自动回滚。
  """
  db = sqlite3.connect(path)
  try:
    cur = db.cursor()

    # 获取现有表列表
    cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}

    with db:
      # 重命名 tweet_events → post_events
      if "tweet_events" in tables and "post_events" not in tables:
        db.execute("ALTER TABLE tweet_events RENAME TO post_events")
        print(f"[migrated] renamed tweet_events → post_events in {path}")

      # is_retweet → is_repost（SQLite 3.25+ 支持 RENAME COLUMN）
      cur.execute("PRAGMA table_info(post_events)")
      columns = {row[1] for row in cur.fetchall()}
      if "is_retweet" in columns:
        db.execute(
            "ALTER TABLE post_events RENAME COLUMN is_retweet TO is_repost"
        )
        print(f"[migrated] renamed is_retweet → is_repost in {path}")

      # rewiring_events.agent_id（DEFAULT 0 为哨兵，表示迁移前写入的行）
      cur.execute("PRAGMA table_info(rewiring_events)")
      rewiring_columns = {row[1] for row in cur.fetchall()}
      if "agent_id" not in rewiring_columns:
        db.execute(
            "ALTER TABLE rewiring_events ADD COLUMN agent_id INTEGER NOT NULL DEFAULT 0"
        )
        print(f"[migrated] added agent_id to rewiring_events in {path}")

      # 重命名 view_tweets_events → view_posts_events
      if "view_tweets_events" in tables and "view_posts_events" not in tables:
        db.execute(
            "ALTER TABLE view_tweets_events RENAME TO view_posts_events"
        )
        print(
            f"[migrated] renamed view_tweets_events → view_posts_events in {path}"
        )

      # 更新事件类型字符串
      db.execute("UPDATE events SET type = 'Post' WHERE type = 'Tweet'")
      db.execute(
          "UPDATE events SET type = 'ViewPosts' WHERE type = 'ViewTweets'"
      )

    print(f"[done] {path}")
  finally:
    db.close()


# ---------------------------------------------------------------------------
# CLI 入口
# ---------------------------------------------------------------------------


def _usage() -> None:
  print(
      "用法:\n"
      "  python -m smp_bindings.migrate snapshot <file.msgpack> [<file2.msgpack> ...] [--dynamics HK|Deffuant|Galam|Voter]\n"
      "  python -m smp_bindings.migrate events <events.db> [<events2.db> ...]\n"
  )


def main(argv: list[str] | None = None) -> None:
  args = argv if argv is not None else sys.argv[1:]
  if len(args) < 2:
    _usage()
    sys.exit(1)

  command, *rest = args

  if command == "snapshot":
    dynamics_type = "HK"
    files = []
    i = 0
    while i < len(rest):
      if rest[i] == "--dynamics" and i + 1 < len(rest):
        dynamics_type = rest[i + 1]
        i += 2
      else:
        files.append(rest[i])
        i += 1
    for p in files:
      migrate_snapshot(p, dynamics_type=dynamics_type)

  elif command == "events":
    for p in rest:
      migrate_events_db(p)

  else:
    _usage()
    sys.exit(1)


if __name__ == "__main__":
  main()
