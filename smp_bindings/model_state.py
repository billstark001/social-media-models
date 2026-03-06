
import sqlite3
import struct
from typing import Any, Dict

import numpy as np
import lz4.frame
import msgpack
import networkx as nx


def load_accumulative_model_state(path: str):
  with open(path, 'rb') as f:
    raw = f.read()
  # LZ4 解压
  decompressed = lz4.frame.decompress(raw)
  offset = 0

  # 读取steps和agents
  steps_p1, agents = struct.unpack_from('<ii', decompressed, offset)
  offset += 8

  # 读取Opinions
  opinions = []
  for _ in range(steps_p1):
    arr = np.frombuffer(decompressed, dtype='<f4', count=agents, offset=offset)
    opinions.append(arr)
    offset += 4 * agents

  # 读取AgentNumbers
  agent_numbers = []
  for _ in range(steps_p1):
    step_arr = []
    for _ in range(agents):
      arr = struct.unpack_from('<4h', decompressed, offset)
      step_arr.append(arr)
      offset += 4 * 2
    agent_numbers.append(step_arr)

  # 读取AgentOpinionSums
  agent_opinion_sums = []
  for _ in range(steps_p1):
    step_arr = []
    for _ in range(agents):
      arr = struct.unpack_from('<4f', decompressed, offset)
      step_arr.append(arr)
      offset += 4 * 4
    agent_opinion_sums.append(step_arr)

  return {
      'steps': steps_p1 - 1,
      'agents': agents,
      'opinions': np.array(opinions),  # shape: (steps + 1, agents)
      # shape: (steps + 1, agents, 4)
      'agent_numbers': np.array(agent_numbers),
      # shape: (steps + 1, agents, 4)
      'agent_opinion_sums': np.array(agent_opinion_sums),
  }


def load_gonum_graph_dump(filename: str, check_sanity=True):
  # 读取 msgpack 文件
  with open(filename, "rb") as f:
    nx_data = msgpack.unpack(f, raw=False, strict_map_key=False)
  assert isinstance(nx_data, dict)

  # 获取 adjacency 信息
  adjacency = nx_data["adjacency"]
  directed = nx_data.get("directed", True)
  nodes = nx_data.get("nodes", {})
  graph_attrs = nx_data.get("graph", {})

  # 构建 NetworkX 图
  if directed:
    G = nx.DiGraph()
  else:
    G = nx.Graph()

  # 添加节点属性
  node_indices = sorted(list(nodes.keys()))
  if check_sanity:
    assert node_indices[0] == 0 and node_indices[-1] == len(
        node_indices) - 1, ' Wrong graph format'
  for n in node_indices:
    attrs = nodes[n]
    G.add_node(n, **attrs)

  # 添加边和边属性
  for from_node, neighbors in adjacency.items():
    for to_node, edge_attrs in neighbors.items():
      if edge_attrs is None:
        edge_attrs = {}
      G.add_edge(from_node, to_node, **edge_attrs)

  # 设置图的属性
  G.graph.update(graph_attrs)
  return G


def load_snapshot(path: str) -> Dict[str, Any]:
  """加载 v2 格式的模型快照文件（RawSnapshotData 信封）。

  返回一个 dict，包含：
    - ``dynamics_type`` (str): 动力学类型，例如 ``"HK"``、``"Deffuant"``。
    - ``data`` (dict): 解包后的 SMPModelDumpData 内容（Posts、Agents 等字段）。
  """
  with open(path, 'rb') as f:
    raw = f.read()
  envelope = msgpack.unpackb(raw, raw=False)
  dynamics_type: str = envelope['DynamicsType']
  inner_bytes: bytes = envelope['Data']
  data = msgpack.unpackb(inner_bytes, raw=False)
  return {'dynamics_type': dynamics_type, 'data': data}
