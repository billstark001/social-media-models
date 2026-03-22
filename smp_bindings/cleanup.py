"""Cleanup utilities for simulation output directories.

This module provides two main capabilities:

1. Prune historical serializer artifacts and keep only the latest file for
   each file type (for example: snapshot, acc-state, finished mark).
2. Detect problematic simulation directories and optionally delete them.

All destructive operations are opt-in. By default, helpers run in dry-run mode.
"""

from __future__ import annotations

import argparse
import dataclasses
import json
import shutil
from pathlib import Path
from typing import Iterable, Sequence


@dataclasses.dataclass(slots=True)
class PruneResult:
  """Result of pruning historical files for one simulation directory."""

  simulation: str
  deleted_files: list[str]
  kept_files: list[str]


@dataclasses.dataclass(slots=True)
class SimulationIssue:
  """A detected issue inside one simulation directory."""

  simulation: str
  problems: list[str]


def _iter_simulation_dirs(base_dir: str | Path) -> list[Path]:
  base = Path(base_dir)
  if not base.exists():
    return []
  return sorted([p for p in base.iterdir() if p.is_dir()], key=lambda p: p.name)


def _get_typed_files(sim_dir: Path, file_type: str, suffix: str) -> list[Path]:
  files = [
      p
      for p in sim_dir.iterdir()
      if p.is_file()
      and p.name.startswith(f"{file_type}-")
      and p.name.endswith(suffix)
  ]
  return sorted(files, key=lambda p: p.name)


STATE_FILE_TYPES: dict[str, str] = {
    "snapshot": ".msgpack",
    "acc-state": ".lz4",
    "finished": ".msgpack",
}


def prune_non_latest_state_files(
    base_dir: str | Path,
    *,
    file_types: Sequence[str] = ("snapshot", "acc-state", "finished"),
    simulations: Iterable[str] | None = None,
    dry_run: bool = True,
) -> list[PruneResult]:
  """Keep only latest state files for each simulation.

  Parameters
  ----------
  base_dir:
      Root directory containing per-simulation subdirectories.
  file_types:
      File type prefixes to prune, each must exist in ``STATE_FILE_TYPES``.
  simulations:
      Optional simulation name allow-list.
  dry_run:
      If True, only report what would be deleted.
  """
  requested = set(simulations) if simulations is not None else None

  unknown_types = [t for t in file_types if t not in STATE_FILE_TYPES]
  if unknown_types:
    raise ValueError(f"unsupported file types: {unknown_types}")

  results: list[PruneResult] = []
  for sim_dir in _iter_simulation_dirs(base_dir):
    if requested is not None and sim_dir.name not in requested:
      continue

    deleted: list[str] = []
    kept: list[str] = []
    for file_type in file_types:
      suffix = STATE_FILE_TYPES[file_type]
      files = _get_typed_files(sim_dir, file_type, suffix)
      if not files:
        continue

      # Files are name-sorted; serializer names include RFC3339-like timestamp,
      # so lexical order matches chronological order.
      keep = files[-1]
      kept.append(str(keep))
      for stale in files[:-1]:
        deleted.append(str(stale))
        if not dry_run:
          stale.unlink(missing_ok=True)

    if deleted or kept:
      results.append(
          PruneResult(
              simulation=sim_dir.name, deleted_files=deleted, kept_files=kept
          )
      )

  return results


def _is_empty_file(path: Path) -> bool:
  try:
    return path.is_file() and path.stat().st_size == 0
  except OSError:
    return True


def inspect_problematic_simulations(
    base_dir: str | Path,
    *,
    deep_check: bool = False,
) -> list[SimulationIssue]:
  """Inspect simulations and return problematic ones.

  Rules:
  - Empty ``snapshot-*``, ``acc-state-*`` or ``finished-*`` files are invalid.
  - If any finished mark exists, both snapshot and acc-state files must exist.
  - If any finished mark exists, latest snapshot/acc-state must be non-empty.
  - Optional ``deep_check`` verifies latest snapshot and acc-state can be parsed.
  """
  issues: list[SimulationIssue] = []

  if deep_check:
    from smp_bindings.model_state import (
        load_accumulative_model_state,
        load_snapshot,
    )

  for sim_dir in _iter_simulation_dirs(base_dir):
    problems: list[str] = []
    snapshots = _get_typed_files(sim_dir, "snapshot", ".msgpack")
    acc_states = _get_typed_files(sim_dir, "acc-state", ".lz4")
    finished_marks = _get_typed_files(sim_dir, "finished", ".msgpack")

    for p in [*snapshots, *acc_states]:
      if _is_empty_file(p):
        problems.append(f"empty file: {p.name}")

    has_finished = len(finished_marks) > 0
    if has_finished:
      if not snapshots:
        problems.append("finished mark exists but snapshot file is missing")
      if not acc_states:
        problems.append("finished mark exists but acc-state file is missing")
      if snapshots and _is_empty_file(snapshots[-1]):
        problems.append(f"latest snapshot is empty: {snapshots[-1].name}")
      if acc_states and _is_empty_file(acc_states[-1]):
        problems.append(f"latest acc-state is empty: {acc_states[-1].name}")

    if deep_check:
      if snapshots and not _is_empty_file(snapshots[-1]):
        try:
          load_snapshot(str(snapshots[-1]))
        except Exception as exc:  # noqa: BLE001
          problems.append(
              f"latest snapshot parse failed ({snapshots[-1].name}): {exc}"
          )
      if acc_states and not _is_empty_file(acc_states[-1]):
        try:
          load_accumulative_model_state(str(acc_states[-1]))
        except Exception as exc:  # noqa: BLE001
          problems.append(
              f"latest acc-state parse failed ({acc_states[-1].name}): {exc}"
          )

    if problems:
      issues.append(SimulationIssue(
          simulation=sim_dir.name, problems=problems))

  return issues


def delete_problematic_simulations(
    base_dir: str | Path,
    issues: Sequence[SimulationIssue],
    *,
    dry_run: bool = True,
) -> list[str]:
  """Delete whole simulation directories for supplied issues."""
  deleted: list[str] = []
  root = Path(base_dir)
  for issue in issues:
    sim_dir = root / issue.simulation
    if not sim_dir.exists():
      continue
    deleted.append(str(sim_dir))
    if not dry_run:
      shutil.rmtree(sim_dir)
  return deleted


def _build_parser() -> argparse.ArgumentParser:
  parser = argparse.ArgumentParser(
      description="Cleanup and inspect simulation output directories."
  )
  sub = parser.add_subparsers(dest="command", required=True)

  prune = sub.add_parser("prune", help="remove non-latest state files")
  prune.add_argument("base_dir", help="simulation root directory")
  prune.add_argument(
      "--type",
      dest="types",
      action="append",
      choices=sorted(STATE_FILE_TYPES.keys()),
      help="state file type to prune; repeatable; default is all",
  )
  prune.add_argument(
      "--simulation",
      dest="simulations",
      action="append",
      help="only process selected simulation name; repeatable",
  )
  prune.add_argument(
      "--apply",
      action="store_true",
      help="actually delete files (default: dry-run)",
  )

  inspect = sub.add_parser("inspect", help="find problematic simulations")
  inspect.add_argument("base_dir", help="simulation root directory")
  inspect.add_argument(
      "--deep-check",
      action="store_true",
      help="parse latest snapshot and acc-state to detect corruption",
  )
  inspect.add_argument(
      "--delete",
      action="store_true",
      help="delete problematic simulation directories",
  )
  inspect.add_argument(
      "--apply",
      action="store_true",
      help="actually delete directories when used with --delete",
  )

  return parser


def main(argv: list[str] | None = None) -> None:
  parser = _build_parser()
  args = parser.parse_args(argv)

  if args.command == "prune":
    file_types = tuple(args.types) if args.types else tuple(
        STATE_FILE_TYPES.keys())
    results = prune_non_latest_state_files(
        args.base_dir,
        file_types=file_types,
        simulations=args.simulations,
        dry_run=not args.apply,
    )
    payload = {
        "mode": "apply" if args.apply else "dry-run",
        "results": [dataclasses.asdict(r) for r in results],
    }
    print(json.dumps(payload, ensure_ascii=True, indent=2))
    return

  if args.command == "inspect":
    issues = inspect_problematic_simulations(
        args.base_dir,
        deep_check=args.deep_check,
    )
    deleted: list[str] = []
    if args.delete:
      deleted = delete_problematic_simulations(
          args.base_dir,
          issues,
          dry_run=not args.apply,
      )
    payload = {
        "mode": "apply" if args.apply else "dry-run",
        "issues": [dataclasses.asdict(i) for i in issues],
        "deleted": deleted,
    }
    print(json.dumps(payload, ensure_ascii=True, indent=2))
    return

  parser.error(f"unknown command: {args.command}")


if __name__ == "__main__":
  main()
