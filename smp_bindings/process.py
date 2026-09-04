"""Public subprocess lifecycle helpers shared by SMP bindings."""

from __future__ import annotations

import os
import signal
import subprocess
from pathlib import Path
from typing import Any


def executable_path(path: os.PathLike[str] | str) -> str:
  """Return a resolved executable path or raise a specific path error."""
  resolved = Path(path).expanduser().resolve()
  if not resolved.is_file():
    raise FileNotFoundError(resolved)
  if not os.access(resolved, os.X_OK):
    raise PermissionError(f"binary is not executable: {resolved}")
  return str(resolved)


def process_group_kwargs() -> dict[str, Any]:
  """Return platform-appropriate ``Popen`` process-group arguments."""
  if os.name == "posix":
    return {"start_new_session": True}
  return {"creationflags": subprocess.CREATE_NEW_PROCESS_GROUP}


def _send_signal(
    proc: subprocess.Popen,
    sig: signal.Signals | int,
    *,
    process_group: bool,
) -> None:
  if proc.poll() is not None:
    return
  if os.name == "posix" and process_group:
    try:
      os.killpg(os.getpgid(proc.pid), sig)
      return
    except ProcessLookupError:
      return
    except PermissionError:
      try:
        proc.send_signal(sig)
      except ProcessLookupError:
        return
  elif os.name != "posix" and process_group and sig == signal.CTRL_C_EVENT:
    try:
      proc.send_signal(sig)
    except (ProcessLookupError, ValueError):
      return
  else:
    try:
      proc.send_signal(sig)
    except ProcessLookupError:
      return


def terminate_process(
    proc: subprocess.Popen,
    *,
    process_group: bool = False,
) -> None:
  """Stop a subprocess gracefully, then escalate if necessary.

  Set ``process_group=True`` only when the process was started with the values
  returned by :func:`process_group_kwargs`.
  """
  if proc.poll() is not None:
    return
  if os.name == "posix":
    _send_signal(proc, signal.SIGINT, process_group=process_group)
    try:
      proc.wait(timeout=8)
    except subprocess.TimeoutExpired:
      _send_signal(proc, signal.SIGTERM, process_group=process_group)
      try:
        proc.wait(timeout=3)
      except subprocess.TimeoutExpired:
        _send_signal(proc, signal.SIGKILL, process_group=process_group)
        proc.wait()
  else:
    try:
      if process_group:
        _send_signal(proc, signal.CTRL_C_EVENT, process_group=True)
      else:
        proc.terminate()
      proc.wait(timeout=8)
    except subprocess.TimeoutExpired:
      try:
        proc.terminate()
        proc.wait(timeout=3)
      except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
