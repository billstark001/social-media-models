"""Wire codecs shared by the persistent and batch SMP runners."""

from __future__ import annotations

import json
from collections.abc import Mapping
from typing import Any

PROGRESS_VERSION = 1
BATCH_SCHEMA_VERSION = 1
PROGRESS_TYPES = frozenset({"rng", "start", "progress", "done"})


class ProtocolError(RuntimeError):
  """A subprocess emitted or received an invalid SMP protocol message."""


def _integer(value: object, field: str) -> int:
  if isinstance(value, bool) or not isinstance(value, int):
    raise ProtocolError(f"progress field {field!r} must be an integer")
  return value


def decode_progress_line(line: str) -> dict[str, Any] | None:
  """Decode one compact progress line into descriptive Python field names.

  Non-JSON diagnostic lines return ``None``. JSON objects that identify
  themselves as progress messages are validated strictly.
  """
  try:
    raw = json.loads(line)
  except json.JSONDecodeError:
    return None
  if not isinstance(raw, dict) or not {"v", "id", "t"}.issubset(raw):
    return None

  version = _integer(raw["v"], "v")
  if version != PROGRESS_VERSION:
    raise ProtocolError(
        f"unsupported progress version {version}; expected {PROGRESS_VERSION}"
    )
  request_id = raw["id"]
  event_type = raw["t"]
  if not isinstance(request_id, str) or not request_id:
    raise ProtocolError("progress field 'id' must be a non-empty string")
  if not isinstance(event_type, str) or event_type not in PROGRESS_TYPES:
    raise ProtocolError(f"unsupported progress event type {event_type!r}")

  event: dict[str, Any] = {
      "version": version,
      "request_id": request_id,
      "type": event_type,
  }
  for wire_name, public_name in (("s", "step"), ("m", "max_step")):
    if wire_name in raw:
      event[public_name] = _integer(raw[wire_name], wire_name)
  if "r" in raw:
    if not isinstance(raw["r"], str):
      raise ProtocolError("progress field 'r' must be a string")
    event["stop_reason"] = raw["r"]
  if event_type == "rng":
    rng_fields = (raw.get("a"), raw.get("x"), raw.get("y"))
    if not all(isinstance(value, str) and value for value in rng_fields):
      raise ProtocolError("rng progress requires non-empty a/x/y strings")
    event["rng"] = {
        "Algorithm": rng_fields[0],
        "Seed1": rng_fields[1],
        "Seed2": rng_fields[2],
    }
  return event


def encode_batch_request(request: Mapping[str, Any]) -> str:
  """Validate and compactly encode one versioned batch request."""
  value = dict(request)
  version = value.setdefault("schema_version", BATCH_SCHEMA_VERSION)
  if isinstance(version, bool) or version != BATCH_SCHEMA_VERSION:
    raise ProtocolError(
        f"unsupported batch schema_version {version!r}; "
        f"expected {BATCH_SCHEMA_VERSION}"
    )
  request_id = value.get("request_id")
  if not isinstance(request_id, str) or not request_id.strip():
    raise ProtocolError("batch request_id must be a non-empty string")
  if not isinstance(value.get("metadata"), Mapping):
    raise ProtocolError("batch metadata must be an object")
  output = value.setdefault("output", {})
  if not isinstance(output, Mapping):
    raise ProtocolError("batch output must be an object")
  try:
    return json.dumps(value, separators=(",", ":"), allow_nan=False)
  except (TypeError, ValueError) as error:
    raise ProtocolError(f"batch request is not valid JSON: {error}") from error


def decode_batch_response(
    line: str,
    *,
    expected_request_id: str | None = None,
) -> dict[str, Any]:
  """Decode and validate one batch response line."""
  try:
    value = json.loads(line)
  except json.JSONDecodeError as error:
    raise ProtocolError("smp-batch returned invalid JSON") from error
  if not isinstance(value, dict):
    raise ProtocolError("smp-batch returned a non-object response")
  version = value.get("schema_version")
  if isinstance(version, bool) or version != BATCH_SCHEMA_VERSION:
    raise ProtocolError(
        f"unsupported batch response schema_version {version!r}; "
        f"expected {BATCH_SCHEMA_VERSION}"
    )
  request_id = value.get("request_id")
  if not isinstance(request_id, str) or not request_id:
    raise ProtocolError("batch response has no request_id")
  if expected_request_id is not None and request_id != expected_request_id:
    raise ProtocolError(
        f"expected batch response {expected_request_id!r}, got {request_id!r}"
    )
  status = value.get("status")
  if status not in {"ok", "cancelled", "error"}:
    raise ProtocolError(f"unsupported batch response status {status!r}")
  if status == "error":
    if not isinstance(value.get("error"), dict):
      raise ProtocolError("error batch response has no error object")
  elif not isinstance(value.get("result"), dict):
    raise ProtocolError("successful batch response has no result object")
  return value
