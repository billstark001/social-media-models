"""
smp_bindings/simulation.py

Launch Go SMP simulations from Python, parse their per-step stdout progress
stream, and run multiple simulations concurrently with a configurable
concurrency limit.

Progress uses the compact JSONL codec shared with ``smp-batch``.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import threading
from collections.abc import Iterable, Iterator
from concurrent.futures import ThreadPoolExecutor, as_completed

from smp_bindings.codec import decode_progress_line
from smp_bindings.process import process_group_kwargs, terminate_process

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
# Public API
# ---------------------------------------------------------------------------


def run_simulation(
    binary_path: str,
    base_path: str,
    metadata: dict,
    show_progress: bool,
    print_lock: threading.Lock,
    sim_index: int | None = None,
    sim_total: int | None = None,
    stop_event: threading.Event | None = None,
    active_procs: set | None = None,
    active_procs_lock: threading.Lock | None = None,
    progress_line_state: dict | None = None,
) -> str:
  """Run one simulation subprocess and return its unique name on completion.

  The Go binary is always started with compact JSON progress output. When
  *show_progress* is True the start / progress / done messages are printed
  to stdout; otherwise they are silently consumed so that the subprocess
  stdout pipe never blocks.
  """
  unique_name = metadata.get("UniqueName", "<unknown>")
  metadata_json = json.dumps(metadata)
  max_step = metadata.get("MaxSimulationStep", 0)
  if sim_index is not None and sim_total is not None:
    progress_prefix = f"[{sim_index}/{sim_total}] "
  else:
    progress_prefix = ""
  if progress_line_state is None:
    progress_line_state = {"active": False, "last_len": 0}

  def _clear_inline_progress_line() -> None:
    if not progress_line_state.get("active", False):
      return
    last_len = int(progress_line_state.get("last_len", 0))
    print("\r" + (" " * last_len) + "\r", end="", flush=True)
    progress_line_state["active"] = False
    progress_line_state["last_len"] = 0

  popen_kwargs = {
      "stdout": subprocess.PIPE,
      "stderr": subprocess.PIPE,
      "text": True,
      "encoding": "utf-8",
      "bufsize": 1,
  }
  popen_kwargs.update(process_group_kwargs())

  proc = subprocess.Popen(
      [binary_path, base_path, metadata_json, "1"],
      **popen_kwargs,
  )
  if active_procs is not None and active_procs_lock is not None:
    with active_procs_lock:
      active_procs.add(proc)

  # Collect stderr in a background thread to prevent the pipe from blocking.
  stderr_lines: list[str] = []

  def _drain_stderr() -> None:
    assert proc.stderr is not None
    stderr_lines.extend(proc.stderr)

  stderr_thread = threading.Thread(target=_drain_stderr, daemon=True)
  stderr_thread.start()
  unexpected_stdout: str | None = None

  try:
    assert proc.stdout is not None
    for raw_line in proc.stdout:
      if stop_event is not None and stop_event.is_set():
        terminate_process(proc, process_group=True)
        raise KeyboardInterrupt

      line = raw_line.rstrip("\n")
      event = decode_progress_line(line)
      if event is None:
        if line.strip() and unexpected_stdout is None:
          unexpected_stdout = line
        continue

      msg_type = event["type"]
      if msg_type == "rng":
        metadata["RNG"] = event["rng"]
      if not show_progress:
        continue
      step = int(event.get("step", 0))

      with print_lock:
        if msg_type == "start":
          _clear_inline_progress_line()
          print(
              f"{progress_prefix}[{unique_name}] started (step {step})",
              flush=True,
          )
        elif msg_type == "progress":
          suffix = f"/{max_step}" if max_step else ""
          progress_text = (
              f"{progress_prefix}[{unique_name}] step {step}{suffix}"
          )
          last_len = int(progress_line_state.get("last_len", 0))
          padding = " " * max(0, last_len - len(progress_text))
          print("\r" + progress_text + padding, end="", flush=True)
          progress_line_state["active"] = True
          progress_line_state["last_len"] = len(progress_text)
        elif msg_type == "done":
          _clear_inline_progress_line()
          done_type = event.get("stop_reason", "?")
          print(
              f"{progress_prefix}[{unique_name}] done ({done_type}) at step {step}",
              flush=True,
          )

    proc.wait()

    if proc.returncode != 0:
      stderr_output = "".join(stderr_lines).strip()
      raise RuntimeError(
          f"Simulation '{unique_name}' failed with exit code {proc.returncode}"
          + (f":\n{stderr_output}" if stderr_output else "")
      )
    if unexpected_stdout:
      raise RuntimeError(
          f"Simulation '{unique_name}' emitted non-protocol stdout: "
          + unexpected_stdout
      )

    return unique_name
  finally:
    stderr_thread.join(timeout=2)
    if proc.stdout is not None:
      proc.stdout.close()
    if proc.stderr is not None:
      proc.stderr.close()
    if active_procs is not None and active_procs_lock is not None:
      with active_procs_lock:
        active_procs.discard(proc)


def run_simulations(
    binary_path: str,
    base_path: str,
    scenarios: list[dict] | Iterable[dict] | Iterator[dict],
    *,
    max_concurrent: int = 4,
    show_progress: bool | None = None,
    skip_finished: bool = True,
    show_position: bool = False,
) -> list[str]:
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
  show_position:
      When ``True``, prefix progress lines with ``[i/n]`` where ``i`` is the
      simulation's launch order among pending scenarios and ``n`` is the
      total number of pending scenarios.

  Returns
  -------
  list of str
      ``UniqueName`` of every simulation that was actually launched
      (finished simulations that were skipped are excluded).
  """
  if show_progress is None:
    show_progress = sys.stdout.isatty()

  # Materialize the iterable so we can compute totals upfront.
  scenario_list: list[dict] = list(scenarios)

  pending: list[dict] = []
  skipped: list[str] = []
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
  active_procs_lock = threading.Lock()
  active_procs = set()
  stop_event = threading.Event()
  progress_line_state = {"active": False, "last_len": 0}
  completed_names: list[str] = []
  pending_total = len(pending)
  interrupted = False

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
      for index, meta in enumerate(pending, start=1):
        name = meta.get("UniqueName", "?")
        try:
          result_name = run_simulation(
              binary_path,
              base_path,
              meta,
              show_progress or False,
              print_lock,
              index if show_position else None,
              pending_total if show_position else None,
              stop_event,
              active_procs,
              active_procs_lock,
              progress_line_state,
          )
          completed_names.append(result_name)
          if outer_bar is not None:
            outer_bar.update(1)
          elif not show_progress:
            print(
                f"Completed '{name}' "
                f"({len(completed_names)}/{len(pending)})"
            )
        # Each simulation is isolated; report its failure and continue the batch.
        except Exception as exc:  # noqa: BLE001
          print(
              f"ERROR in simulation '{name}': {exc}",
              file=sys.stderr,
          )
    else:
      executor = ThreadPoolExecutor(max_workers=max_concurrent)
      try:
        futures = {
            executor.submit(
                run_simulation,
                binary_path,
                base_path,
                meta,
                show_progress or False,
                print_lock,
                index if show_position else None,
                pending_total if show_position else None,
                stop_event,
                active_procs,
                active_procs_lock,
                progress_line_state,
            ): meta.get("UniqueName", "?")
            for index, meta in enumerate(pending, start=1)
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
          # Future failures are per-simulation and must not cancel peer runs.
          except Exception as exc:  # noqa: BLE001
            with print_lock:
              print(
                  f"ERROR in simulation '{name}': {exc}",
                  file=sys.stderr,
              )
      except KeyboardInterrupt:
        stop_event.set()
        raise
      finally:
        executor.shutdown(
            wait=not stop_event.is_set(), cancel_futures=stop_event.is_set()
        )
  except KeyboardInterrupt:
    interrupted = True
    stop_event.set()
    with print_lock:
      print(
          "\nKeyboardInterrupt received: stopping running simulations...",
          file=sys.stderr,
      )
    with active_procs_lock:
      running_procs = list(active_procs)
    for proc in running_procs:
      terminate_process(proc, process_group=True)
  finally:
    if outer_bar is not None:
      outer_bar.close()

  if interrupted:
    print(
        "Interrupted by user. "
        f"Completed {len(completed_names)}/{len(pending)} simulation(s)."
    )

  return completed_names
