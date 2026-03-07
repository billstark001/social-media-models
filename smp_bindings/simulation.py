"""
smp_bindings/simulation.py

Launch Go SMP simulations from Python, parse their per-step stdout progress
stream, and run multiple simulations concurrently with a configurable
concurrency limit.

Stdout protocol (produced by the Go binary with parsable-progress enabled):
    TASK:<name>;TYPE:INIT;STEP:<n>;
    TASK:<name>;TYPE:PROGRESS;STEP:<n>;
    TASK:<name>;TYPE:DONE;DONE_TYPE:(SIG|ITER|HALT);STEP:<n>;
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import threading
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import Dict, Iterable, Iterator, List, Optional, Union

try:
  from tqdm import tqdm as _tqdm

  _TQDM_AVAILABLE = True
except ImportError:
  _TQDM_AVAILABLE = False


# ---------------------------------------------------------------------------
# Public helpers
# ---------------------------------------------------------------------------


def is_simulation_finished(base_path: str, metadata: dict) -> bool:
  """Return True if *base_path/<UniqueName>* contains a ``finished-*`` file.

  This mirrors the Go ``SimulationSerializer.IsFinished()`` check, which
  looks for any file whose name starts with ``finished`` inside the
  simulation sub-directory.
  """
  unique_name = metadata.get("UniqueName", "")
  sim_dir = os.path.join(base_path, unique_name)
  if not os.path.isdir(sim_dir):
    return False
  return any(f.startswith("finished") for f in os.listdir(sim_dir))


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------


def _parse_progress_line(line: str) -> Optional[Dict[str, str]]:
  """Parse a ``KEY:VALUE;...`` progress line; return None for other lines."""
  parts = [p for p in line.strip().split(";") if p]
  result: Dict[str, str] = {}
  for part in parts:
    if ":" in part:
      k, _, v = part.partition(":")
      result[k] = v
  return result if result else None


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def run_simulation(
    binary_path: str,
    base_path: str,
    metadata: dict,
    show_progress: bool,
    print_lock: threading.Lock,
) -> str:
  """Run one simulation subprocess and return its unique name on completion.

  The Go binary is always started with parsable-progress output.  When
  *show_progress* is True the INIT / PROGRESS / DONE messages are printed
  to stdout; otherwise they are silently consumed so that the subprocess
  stdout pipe never blocks.
  """
  unique_name = metadata.get("UniqueName", "<unknown>")
  metadata_json = json.dumps(metadata)
  max_step = metadata.get("MaxSimulationStep", 0)

  proc = subprocess.Popen(
      [binary_path, base_path, metadata_json, "1"],
      stdout=subprocess.PIPE,
      stderr=subprocess.PIPE,
      text=True,
      bufsize=1,
  )

  # Drain stderr in a background thread to prevent the pipe from blocking.
  def _drain_stderr() -> None:
    assert proc.stderr is not None
    for _ in proc.stderr:
      pass

  stderr_thread = threading.Thread(target=_drain_stderr, daemon=True)
  stderr_thread.start()

  assert proc.stdout is not None
  for raw_line in proc.stdout:
    if not show_progress:
      continue
    line = raw_line.rstrip("\n")
    parsed = _parse_progress_line(line)
    if parsed is None:
      continue

    msg_type = parsed.get("TYPE", "")
    step_raw = parsed.get("STEP", "0")
    step = int(step_raw) if step_raw.isdigit() else 0

    with print_lock:
      if msg_type == "INIT":
        print(f"[{unique_name}] started (step {step})", flush=True)
      elif msg_type == "PROGRESS":
        suffix = f"/{max_step}" if max_step else ""
        print(
            f"\r[{unique_name}] step {step}{suffix}   ",
            end="",
            flush=True,
        )
      elif msg_type == "DONE":
        done_type = parsed.get("DONE_TYPE", "?")
        print(
            f"\n[{unique_name}] done ({done_type}) at step {step}",
            flush=True,
        )

  proc.wait()
  stderr_thread.join(timeout=2)
  return unique_name


def run_simulations(
    binary_path: str,
    base_path: str,
    scenarios: Union[List[dict], Iterable[dict], Iterator[dict]],
    *,
    max_concurrent: int = 4,
    show_progress: Optional[bool] = None,
    skip_finished: bool = True,
) -> List[str]:
  """Run multiple SMP simulations concurrently.

  Parameters
  ----------
  binary_path:
      Path to the compiled SMP binary (e.g. ``"./smp"``).
  base_path:
      Root directory where each simulation's sub-directory will be created.
  scenarios:
      Sequence (or any iterable / iterator) of metadata dicts.  Each dict
      must contain at least ``UniqueName`` and ``MaxSimulationStep``.
  max_concurrent:
      Maximum number of simulations running simultaneously (default ``4``).
  show_progress:
      * ``True``  – always print per-simulation step progress to stdout.
      * ``False`` – suppress all per-simulation output.
      * ``None``  – auto-detect: show only when stdout is a terminal.
  skip_finished:
      Skip simulations whose output directory already contains a *finished*
      mark file, avoiding redundant re-runs (default ``True``).

  Returns
  -------
  list of str
      ``UniqueName`` of every simulation that was actually launched
      (finished simulations that were skipped are excluded).
  """
  if show_progress is None:
    show_progress = sys.stdout.isatty()

  # Materialize the iterable so we can compute totals upfront.
  scenario_list: List[dict] = list(scenarios)

  pending: List[dict] = []
  skipped: List[str] = []
  for meta in scenario_list:
    if skip_finished and is_simulation_finished(base_path, meta):
      skipped.append(meta.get("UniqueName", ""))
    else:
      pending.append(meta)

  if skipped:
    print(
        f"Skipping {len(skipped)} already-finished simulation(s): "
        + ", ".join(skipped)
    )
  if not pending:
    print("All simulations already finished.")
    return []

  print_lock = threading.Lock()
  completed_names: List[str] = []

  # Show an outer tqdm bar to track overall simulation count.  When
  # show_progress is True the per-sim step output is already verbose, so we
  # skip the bar to reduce noise.
  use_outer_bar = _TQDM_AVAILABLE and not show_progress
  outer_bar = (
      _tqdm(total=len(pending), desc="Simulations", unit="sim")
      if use_outer_bar
      else None
  )

  try:
    if max_concurrent <= 1:
      for meta in pending:
        name = meta.get("UniqueName", "?")
        try:
          result_name = run_simulation(
              binary_path, base_path, meta, show_progress or False, print_lock
          )
          completed_names.append(result_name)
          if outer_bar is not None:
            outer_bar.update(1)
          elif not show_progress:
            print(
                f"Completed '{name}' "
                f"({len(completed_names)}/{len(pending)})"
            )
        except Exception as exc:
          print(
              f"ERROR in simulation '{name}': {exc}",
              file=sys.stderr,
          )
    else:
      with ThreadPoolExecutor(max_workers=max_concurrent) as executor:
        futures = {
            executor.submit(
                run_simulation,
                binary_path,
                base_path,
                meta,
                show_progress or False,
                print_lock,
            ): meta.get("UniqueName", "?")
            for meta in pending
        }
        for future in as_completed(futures):
          name = futures[future]
          try:
            result_name = future.result()
            completed_names.append(result_name)
            if outer_bar is not None:
              outer_bar.update(1)
            elif not show_progress:
              print(
                  f"Completed '{name}' "
                  f"({len(completed_names)}/{len(pending)})"
              )
          except Exception as exc:
            with print_lock:
              print(
                  f"ERROR in simulation '{name}': {exc}",
                  file=sys.stderr,
              )
  finally:
    if outer_bar is not None:
      outer_bar.close()

  return completed_names
