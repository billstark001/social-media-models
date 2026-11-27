import struct
from typing import Any

import lz4.frame
import msgpack
import networkx as nx
import numpy as np
from smp_bindings.domain import (AccumulatedState, Endpoint, Exposure, ModelState, Pathway, PolicyChange,
    RecoveryOptions, ResearchRecovery, SlotCounts, Snapshot, SnapshotEnvelope, TracePosition,
    parse_dataclass, parse_metadata)


def load_accumulative_model_state(
    path: str,
    with_agent_numbers: bool = False,
    with_agent_opinion_sums: bool = False
) -> AccumulatedState:
  """Read the channel-major legacy state, selecting optional output channels."""
  with open(path, 'rb') as f:
    raw = f.read()
  decompressed = lz4.frame.decompress(raw)
  if len(decompressed) < 8:
    raise ValueError("truncated accumulated-state header")
  steps_p1, agents = struct.unpack_from('<ii', decompressed)
  if steps_p1 <= 0 or agents <= 0:
    raise ValueError("invalid accumulated-state dimensions")
  cells = steps_p1 * agents
  expected = 8 + cells * (4 + 4 * 2 + 4 * 4)
  if len(decompressed) != expected:
    raise ValueError(f"accumulated-state payload has {len(decompressed)} bytes, expected {expected}")
  ret = AccumulatedState(steps=steps_p1 - 1, agents=agents,
      opinions=np.frombuffer(decompressed, dtype='<f4', count=cells, offset=8).reshape(steps_p1, agents).copy())
  if with_agent_numbers:
    ret.agent_numbers = np.frombuffer(
        decompressed, dtype='<i2', count=4 * cells, offset=8 + 4 * cells,
    ).reshape(steps_p1, agents, 4).astype(int)
  if with_agent_opinion_sums:
    ret.agent_opinion_sums = np.frombuffer(
        decompressed, dtype='<f4', count=4 * cells, offset=8 + (4 + 4 * 2) * cells,
    ).reshape(steps_p1, agents, 4).astype(float)
  return ret


def load_gonum_graph_dump(filename: str, check_sanity=True):
  """Read a Gonum graph, optionally requiring contiguous zero-based node IDs."""
  with open(filename, "rb") as f:
    raw = f.read()
  if filename.endswith('.lz4'):
    raw = lz4.frame.decompress(raw)
  nx_data = msgpack.unpackb(raw, raw=False, strict_map_key=False)
  if not isinstance(nx_data, dict):
    raise ValueError("graph dump must be a map")

  adjacency = nx_data["adjacency"]
  directed = nx_data.get("directed", True)
  nodes = nx_data.get("nodes", {})
  graph_attrs = nx_data.get("graph", {})

  if directed:
    G = nx.DiGraph()
  else:
    G = nx.Graph()

  node_indices = sorted(nodes)
  for n in node_indices:
    attrs = nodes[n]
    G.add_node(n, **attrs)

  for from_node, neighbors in adjacency.items():
    for to_node, edge_attrs in neighbors.items():
      if edge_attrs is None:
        edge_attrs = {}
      G.add_edge(from_node, to_node, **edge_attrs)

  if check_sanity and sorted(G.nodes) != list(range(len(G))):
    raise ValueError("graph node IDs must be contiguous and zero-based")
  G.graph.update(graph_attrs)
  return G


def _checkpoint_exposure(wire):
  """Decode Go's msgpack field names into the same semantic exposure class."""
  from dataclasses import fields
  values = {}
  for item in fields(Exposure):
    if item.name.startswith('_'):
      continue
    name = ''.join(part.capitalize() for part in item.name.split('_'))
    value = wire[name]
    if item.name in ('exploration', 'policy', 'bridge'):
      value = parse_dataclass(SlotCounts, {
          field.name: value[''.join(part.capitalize() for part in field.name.split('_'))]
          for field in fields(SlotCounts) if not field.name.startswith('_')
      })
    values[item.name] = value
  return Exposure(**values)


def _checkpoint_research(wire):
  if wire is None:
    return None
  from dataclasses import fields
  options = wire['Options']
  change = options['PolicyChange']
  if change is not None:
    change = parse_dataclass(PolicyChange, {
        item.name: change[''.join(part.capitalize() for part in item.name.split('_'))]
        for item in fields(PolicyChange) if not item.name.startswith('_')
    })
  endpoints = tuple(Endpoint(step=item['Step'], relative_step=item['RelativeStep'],
      pathway=parse_dataclass(Pathway, item['Pathway']),
      exposure=_checkpoint_exposure(item['Exposure'])) for item in wire['Endpoints'])
  return ResearchRecovery(
      Options=RecoveryOptions(ObserveAt=tuple(options['ObserveAt']), PolicyChange=change,
                              ExposureTrace=options['ExposureTrace'], TraceStart=options['TraceStart']),
      Endpoints=endpoints, Window=_checkpoint_exposure(wire['Window']), SwitchStep=wire['SwitchStep'],
      SwitchFallback=wire['SwitchFallback'], Policy=wire['Policy'], PolicyEnded=wire['PolicyEnded'],
      Trace=parse_dataclass(TracePosition, wire['Trace']), FixedHorizon=wire['FixedHorizon'])


def load_snapshot(path: str, *, include_envelope: bool = False) -> Snapshot:
  """Read a RawSnapshotData envelope and decode its microscopic model payload."""
  with open(path, 'rb') as f:
    raw = f.read()
  if path.endswith('.lz4'):
    raw = lz4.frame.decompress(raw)
  envelope = msgpack.unpackb(raw, raw=False)
  dynamics_type: str = envelope['DynamicsType']
  inner_bytes: bytes = envelope['Data']
  data = parse_dataclass(ModelState, msgpack.unpackb(inner_bytes, raw=False, strict_map_key=False))
  recovery = None
  if include_envelope:
    metadata_wire = envelope.get('Metadata')
    if metadata_wire is not None:
      metadata_wire = dict(metadata_wire)
      precision = metadata_wire.get('TrajectoryPrecision')
      if precision is not None:
        metadata_wire['TrajectoryPrecision'] = {'opinions': precision['Opinions'], 'opinion_sums': precision['OpinionSums']}
    recovery = SnapshotEnvelope(DynamicsType=dynamics_type,
        Metadata=parse_metadata(metadata_wire) if metadata_wire is not None else None,
        CompletedStep=envelope.get('CompletedStep'), StableSteps=envelope.get('StableSteps'),
        Research=_checkpoint_research(envelope.get('Research')))
  return Snapshot(dynamics_type=dynamics_type, data=data, envelope=recovery)
