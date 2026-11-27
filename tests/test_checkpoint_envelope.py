"""The canonical reader exposes recovery metadata without changing pairing views."""

import lz4.frame
import msgpack

from smp_bindings.domain import ModelState, Snapshot
from smp_bindings.model_state import load_snapshot


def test_complete_research_envelope_is_available_without_reencoding(tmp_path):
    # Dictionaries here describe the native msgpack boundary, not internal state.
    payload = {"CurStep": 17, "Opinions": [-.4, .4],
               "Graph": {"directed": True, "nodes": {0: {}, 1: {}}, "adjacency": {0: {1: {}}}},
               "Posts": {0: [{"AgentID": 1, "Step": 16, "Opinion": .4}]},
               "RNGStates": {"dynamics": b"exact-state"}}
    slots = {"Shown": 0, "CrossCamp": 0, "CrossConfident": 0, "CrossRewire": 0}
    exposure = {"Updates": 0, "Requested": 0, "NeighborSeen": 0, "NeighborConcordant": 0,
                "NeighborWithPosts": 0, "NeighborShareSum": 0., "Recommended": 0,
                "Exploration": slots, "Policy": slots, "Bridge": slots,
                "CrossCamp": 0, "CrossConfident": 0, "CrossRewire": 0, "Rewires": 0, "AbsoluteShift": 0.}
    research = {"Options": {"ObserveAt": [0, 17], "PolicyChange": None, "ExposureTrace": "", "TraceStart": 0},
                "Endpoints": [{"Step": 0, "RelativeStep": 0,
                               "Pathway": {"separation": 1., "pair_concordance": .5,
                                           "edge_concordance": 1., "excess_concordance": .5},
                               "Exposure": exposure}],
                "Window": exposure, "SwitchStep": 8, "SwitchFallback": False,
                "Policy": "StructureM9", "PolicyEnded": False,
                "Trace": {"Offset": 0, "Updates": 0, "Slots": 0}, "FixedHorizon": True}
    envelope = {"DynamicsType": "HK", "Data": msgpack.packb(payload, use_bin_type=True),
                "CompletedStep": 16, "Metadata": {"UniqueName": "one"}, "Research": research}
    path = tmp_path / "checkpoint.msgpack.lz4"
    raw = lz4.frame.compress(msgpack.packb(envelope, use_bin_type=True))
    path.write_bytes(raw)
    physical = load_snapshot(str(path))
    complete = load_snapshot(str(path), include_envelope=True)
    assert isinstance(physical, Snapshot) and isinstance(physical.data, ModelState)
    assert physical.envelope is None and physical.data == complete.data
    assert physical.data.CurStep == 17 and physical.data.RNGStates == {"dynamics": b"exact-state"}
    assert physical.data.Graph.adjacency[0][1] is not None
    assert physical.data.Posts[0][0].AgentID == 1
    assert complete.envelope.Research.SwitchStep == 8
    assert complete.envelope.Research.Endpoints[0].pathway.separation == 1.
    assert complete.envelope.CompletedStep == 16
    assert path.read_bytes() == raw
