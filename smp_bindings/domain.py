"""Simulator domain values; dictionaries exist only in serialization codecs."""

from __future__ import annotations

import types
import sys
from dataclasses import MISSING, dataclass, field, fields, is_dataclass, replace
from pathlib import Path
from functools import lru_cache
from typing import Any, Union, get_args, get_origin, get_type_hints

import numpy as np


@dataclass(kw_only=True, slots=True)
class WireRecord:
    _wire_fields: frozenset[str] | None = field(default=None, repr=False, compare=False)

    def __json__(self):
        return to_wire(self)

    def __wire__(self, *, preserve_arrays=False):
        return to_wire(self, preserve_arrays=preserve_arrays)


def to_wire(value, *, preserve_arrays=False):
    """Serialize at an I/O boundary, preserving explicitly recorded field presence."""
    if not isinstance(value, WireRecord) and hasattr(value, "__wire__"):
        return value.__wire__(preserve_arrays=preserve_arrays)
    if is_dataclass(value):
        result = {}
        domain_record = isinstance(value, WireRecord)
        present = value._wire_fields if domain_record else None
        for definition in fields(value):
            if definition.name.startswith("_"):
                continue
            name = definition.metadata.get("wire", definition.name)
            item = getattr(value, definition.name)
            default = definition.default
            if definition.default_factory is not MISSING:
                default = definition.default_factory()
            if domain_record and present is not None and name not in present and default is not MISSING:
                try:
                    equal = item == default
                    if isinstance(equal, (bool, np.bool_)) and equal:
                        continue
                except (TypeError, ValueError):
                    pass
            if domain_record and present is None and item is None and default is None and not definition.metadata.get("emit_none", False):
                continue
            result[name] = to_wire(item, preserve_arrays=preserve_arrays)
        return result
    if isinstance(value, np.ndarray):
        return value if preserve_arrays else value.tolist()
    if isinstance(value, np.generic):
        return value.item()
    if isinstance(value, Path):
        return str(value)
    if isinstance(value, (tuple, list)):
        return [to_wire(item, preserve_arrays=preserve_arrays) for item in value]
    if isinstance(value, dict):
        return {key: to_wire(item, preserve_arrays=preserve_arrays) for key, item in value.items()}
    return value


def _parse(annotation, value):
    if annotation is Any:
        return value
    origin, args = get_origin(annotation), get_args(annotation)
    if origin in (Union, types.UnionType):
        failures = []
        for candidate in args:
            try:
                return _parse(candidate, value)
            except (TypeError, ValueError, KeyError) as error:
                failures.append(str(error))
        raise ValueError("value does not match declared alternatives: " + "; ".join(failures))
    if annotation is type(None):
        if value is not None:
            raise TypeError("expected null")
        return None
    if origin in (tuple, list):
        if not isinstance(value, (list, tuple)):
            raise TypeError("expected an array")
        if len(args) == 2 and args[1] is Ellipsis:
            values = [_parse(args[0], item) for item in value]
        else:
            if len(args) != len(value):
                raise ValueError("array length does not match the declared tuple")
            values = [_parse(kind, item) for kind, item in zip(args, value)]
        return tuple(values) if origin is tuple else values
    if origin is dict:
        if not isinstance(value, dict):
            raise TypeError("expected a homogeneous map")
        def map_key(key):
            # JSON object keys encode integer identifiers as decimal strings.
            if args[0] is int and isinstance(key, str):
                try:
                    integer = int(key)
                except ValueError:
                    raise ValueError("integer map key must be decimal") from None
                if str(integer) != key:
                    raise ValueError("integer map key must use canonical decimal encoding")
                return integer
            return _parse(args[0], key)
        result = {}
        for key, item in value.items():
            decoded = map_key(key)
            if decoded in result:
                raise ValueError("duplicate decoded map key")
            result[decoded] = _parse(args[1], item)
        return result
    if is_dataclass(annotation):
        return parse_dataclass(annotation, value)
    if annotation is float:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise TypeError("expected a number")
        return value  # Retain recorded integer encoding for frozen fingerprints.
    if annotation in (int, bool, str, bytes):
        if type(value) is not annotation:
            raise TypeError(f"expected {annotation.__name__}")
        return value
    if annotation is Path:
        return Path(value)
    raise TypeError(f"unsupported wire annotation: {annotation}")


@lru_cache(maxsize=None)
def _wire_schema(kind):
    definitions = {item.metadata.get("wire", item.name): item for item in fields(kind) if not item.name.startswith("_")}
    namespace = vars(sys.modules[kind.__module__])
    return definitions, get_type_hints(kind, globalns=namespace, localns=namespace)


def parse_dataclass(kind, wire):
    if not isinstance(wire, dict):
        raise TypeError(f"{kind.__name__} requires an object at its decoding boundary")
    definitions, hints = _wire_schema(kind)
    unknown = wire.keys() - definitions.keys()
    if unknown:
        raise ValueError(f"unknown {kind.__name__} fields: {sorted(unknown)}")
    values = {definitions[name].name: _parse(hints[definitions[name].name], item) for name, item in wire.items()}
    if issubclass(kind, WireRecord):
        values["_wire_fields"] = frozenset(wire)
    return kind(**values)


@dataclass(kw_only=True, slots=True)
class Rng(WireRecord):
    Algorithm: str
    Seed1: str | int
    Seed2: str | int


@dataclass(kw_only=True, slots=True)
class HKParams(WireRecord):
    Tolerance: float | None = None
    Influence: float | None = None
    RewiringRate: float | None = None
    RepostRate: float | None = None


@dataclass(kw_only=True, slots=True)
class BinaryDynamicsParams(WireRecord):
    Influence: float
    RewiringRate: float
    RepostRate: float


@dataclass(kw_only=True, slots=True)
class RecommenderParams(WireRecord):
    NoiseStd: float | None = None
    OpRandomNoiseStd: float | None = None
    UseCache: bool | None = None
    Tolerance: float | None = None
    Steepness: float | None = None
    RandomRatio: float | None = None
    MixRate: float | None = None
    LogRecommendations: bool | None = None

    def with_overrides(self, other: RecommenderParams):
        return replace(self, **{item.name: getattr(other, item.name) for item in fields(other)
                                if not item.name.startswith("_") and getattr(other, item.name) is not None})


@dataclass(kw_only=True, slots=True)
class TrajectoryPrecision(WireRecord):
    opinions: str
    opinion_sums: str


@dataclass(kw_only=True, slots=True)
class ScenarioMetadata(WireRecord):
    UniqueName: str
    HKParams: HKParams | None = None
    DynamicsType: str = "HK"
    DataVersion: int | None = None
    RNG: Rng | None = None
    RNGSeed: int | str | None = None
    DeffuantParams: HKParams | None = None
    GalamParams: BinaryDynamicsParams | None = None
    VoterParams: BinaryDynamicsParams | None = None
    RecsysFactoryType: str | None = None
    RecsysCount: int | None = None
    PostRetainCount: int | None = None
    MaxSimulationStep: int | None = None
    CheckpointSteps: tuple[int, ...] | None = None
    TrajectoryPrecision: TrajectoryPrecision | None = None
    NetworkType: str | None = None
    NodeCount: int | None = None
    NodeFollowCount: int | None = None
    RecSysParams: RecommenderParams | None = None
    AgentNumber: bool | None = None
    OpinionSum: bool | None = None
    RewiringEvent: bool | None = None
    PostEvent: bool | None = None
    ViewPostsEvent: bool | None = None
    ViewPostsDetails: bool | None = None

    def dynamics_parameters(self):
        return getattr(self, self.DynamicsType + "Params")


def parse_metadata(wire):
    return parse_dataclass(ScenarioMetadata, wire)


@dataclass(kw_only=True, slots=True)
class SlotCounts(WireRecord):
    shown: int
    cross_camp: int
    cross_confident: int
    cross_rewire: int


@dataclass(kw_only=True, slots=True)
class Exposure(WireRecord):
    updates: int
    requested: int
    neighbor_seen: int
    neighbor_concordant: int
    neighbor_with_posts: int
    neighbor_share_sum: float
    recommended: int
    exploration: SlotCounts
    policy: SlotCounts
    bridge: SlotCounts
    cross_camp: int
    cross_confident: int
    cross_rewire: int
    rewires: int
    absolute_shift: float


@dataclass(kw_only=True, slots=True)
class Pathway(WireRecord):
    separation: float
    pair_concordance: float
    edge_concordance: float | None
    excess_concordance: float | None


@dataclass(kw_only=True, slots=True)
class Endpoint(WireRecord):
    step: int
    relative_step: int
    pathway: Pathway
    exposure: Exposure


@dataclass(kw_only=True, slots=True)
class TerminalComponent(WireRecord):
    minimum: float
    maximum: float
    mass: float
    major: bool


@dataclass(kw_only=True, slots=True)
class TerminalMargins(WireRecord):
    gap_to_epsilon: float
    diameter_to_epsilon: float
    mass_to_major_cutoff: float


@dataclass(kw_only=True, slots=True)
class TerminalResult(WireRecord):
    status: str
    category: str
    k_all: int
    k_major: int
    components: tuple[TerminalComponent, ...]
    margins: TerminalMargins


@dataclass(kw_only=True, slots=True)
class TraceReceipt(WireRecord):
    version: int
    path: str
    updates: int
    slots: int


@dataclass(kw_only=True, slots=True)
class ArmObservation(WireRecord):
    endpoints: tuple[Endpoint, ...]
    terminal: TerminalResult
    stop_reason: str
    stopped_at: int
    final_opinions: tuple[float, ...]
    index: int | None = None
    rng: Rng | None = None
    switch_step: int | None = None
    switch_fallback: bool = False
    exposure_trace: TraceReceipt | None = None
    raw_record: str | None = None
    run_directory: str | None = None
    binary_sha256: str | None = None
    source_sha256: str | None = None


def parse_observation(wire):
    return parse_dataclass(ArmObservation, wire)


@dataclass(kw_only=True, slots=True)
class PolicyChange(WireRecord):
    recommender: str
    at: int | None = None
    excess_at_least: float | None = None
    latest: int | None = None
    duration: int | None = None
    bridge: bool | None = None
    hide_reposts: bool | None = None


@dataclass(kw_only=True, slots=True)
class PersistentJob(WireRecord):
    base_path: str
    metadata: ScenarioMetadata
    observe_at: tuple[int, ...]
    fixed_horizon: bool
    result_protocol: str = "smp-research-result-v1"
    policy_change: PolicyChange | None = None
    exposure_trace: str | None = None
    trace_start: int | None = None


@dataclass(kw_only=True, slots=True)
class ResearchFrame(WireRecord):
    type: str
    protocol: str
    result: ArmObservation


@dataclass(kw_only=True, slots=True)
class ProbeSummary(WireRecord):
    mean: float
    variance: float
    samples: int
    active: int


@dataclass(kw_only=True, slots=True)
class ProbePoint(WireRecord):
    x: float
    f_probe: ProbeSummary
    f_neighbor: ProbeSummary
    f_recommendation: ProbeSummary
    mean_concordant_neighbor: float
    mean_concordant_recommendation: float
    update_second_moment: ProbeSummary
    update_variance: float
    no_update_probability: float


@dataclass(kw_only=True, slots=True)
class Energy(WireRecord):
    population: float
    edge: float
    topology_excess: float
    nodes: int
    edges: int


@dataclass(kw_only=True, slots=True)
class BinnedState(WireRecord):
    population: tuple[float, ...]
    edge: tuple[float, ...]
    nodes: int
    edges: int


@dataclass(kw_only=True, slots=True)
class Landscape(WireRecord):
    x: tuple[float, ...]
    population: tuple[float, ...]
    population_negative_gradient: tuple[float, ...]
    population_concordant_mass: tuple[float, ...]
    edge_by_source: tuple[float, ...]
    source_counts: tuple[int, ...]
    edge_counts: tuple[int, ...]
    edge_by_source_valid: tuple[bool, ...]


@dataclass(kw_only=True, slots=True)
class ProbeState(WireRecord):
    step: int
    precision: str
    points: tuple[ProbePoint, ...] | None
    pathway: Pathway | None = None
    energy: Energy | None = None
    binned: BinnedState | None = None
    terminal: TerminalResult | None = None
    landscape: Landscape | None = None
    counterfactual_neighbor_energy: dict[int, tuple[float, ...]] | None = None
    counterfactual_neighbor_valid: dict[int, bool] | None = None
    anchor_points: dict[int, tuple[ProbePoint, ...]] | None = None


@dataclass(kw_only=True, slots=True)
class ProbeResponse(WireRecord):
    version: int
    rng: Rng
    dynamics_type: str
    results: tuple[ProbeState, ...]
    source_max_step: int | None = None


@dataclass(kw_only=True, slots=True)
class AccumulatedState:
    steps: int
    agents: int
    opinions: Any
    agent_numbers: Any | None = None
    agent_opinion_sums: Any | None = None


@dataclass(kw_only=True, slots=True)
class ProgressEvent(WireRecord):
    version: int
    request_id: str
    type: str
    step: int | None = None
    max_step: int | None = None
    stop_reason: str | None = None
    rng: Rng | None = None


@dataclass(slots=True)
class ProgressLineState:
    active: bool = False
    last_len: int = 0


@dataclass(kw_only=True, slots=True)
class GraphAttributes(WireRecord):
    pass


@dataclass(kw_only=True, slots=True)
class GraphMetadata(WireRecord):
    name: str | None = None


@dataclass(kw_only=True, slots=True)
class GraphState(WireRecord):
    adjacency: dict[int, dict[int, GraphAttributes | None]]
    nodes: dict[int, GraphAttributes]
    directed: bool = True
    graph: GraphMetadata | None = None


@dataclass(kw_only=True, slots=True)
class SavedPost(WireRecord):
    AgentID: int
    Step: int
    Opinion: float | bool


@dataclass(kw_only=True, slots=True)
class ModelState(WireRecord):
    CurStep: int
    Graph: GraphState
    Opinions: tuple[float | bool, ...]
    Posts: dict[int, tuple[SavedPost, ...]]
    AgentNumbers: tuple[tuple[int, int, int, int], ...] | None = None
    AgentOpinionSums: tuple[tuple[float, float, float, float], ...] | None = None
    RecsysDumpData: bytes | None = None
    RNGStates: dict[str, bytes] | None = None


@dataclass(kw_only=True, slots=True)
class RecoveryOptions:
    ObserveAt: tuple[int, ...]
    PolicyChange: PolicyChange | None
    ExposureTrace: str
    TraceStart: int


@dataclass(kw_only=True, slots=True)
class TracePosition(WireRecord):
    Offset: int
    Updates: int
    Slots: int


@dataclass(kw_only=True, slots=True)
class ResearchRecovery:
    Options: RecoveryOptions
    Endpoints: tuple[Endpoint, ...]
    Window: Exposure
    SwitchStep: int | None
    SwitchFallback: bool
    Policy: str
    PolicyEnded: bool
    Trace: TracePosition
    FixedHorizon: bool


@dataclass(kw_only=True, slots=True)
class SnapshotEnvelope:
    DynamicsType: str
    Metadata: ScenarioMetadata | None
    CompletedStep: int | None
    StableSteps: int | None
    Research: ResearchRecovery | None


@dataclass(kw_only=True, slots=True)
class Snapshot:
    dynamics_type: str
    data: ModelState
    envelope: SnapshotEnvelope | None = None
