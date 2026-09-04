"""Python orchestration for the versioned ``smp-batch`` JSONL protocol."""

from __future__ import annotations

import os
import queue
import subprocess
import threading
from collections import deque
from collections.abc import Callable, Iterable, Mapping
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Literal

from smp_bindings.codec import (
    ProtocolError,
    decode_batch_response,
    decode_progress_line,
    encode_batch_request,
)
from smp_bindings.process import (
    executable_path,
    process_group_kwargs,
    terminate_process,
)

ProgressCallback = Callable[[dict[str, Any]], None]


class BatchExecutionError(RuntimeError):
  """A valid batch response reported a request-level error."""

  def __init__(self, response: Mapping[str, Any]) -> None:
    self.response = dict(response)
    error = response.get("error")
    detail = error if isinstance(error, Mapping) else {}
    self.kind = str(detail.get("kind", "unknown"))
    self.request_id = str(response.get("request_id", "<unknown>"))
    message = str(detail.get("message", "unknown batch error"))
    super().__init__(f"{self.request_id}: {self.kind}: {message}")


def _validate_progress(
    progress: ProgressCallback | None,
    progress_step_interval: int,
) -> None:
  if progress_step_interval < 0:
    raise ValueError("progress_step_interval must be non-negative")
  if progress is None and progress_step_interval:
    raise ValueError("progress_step_interval requires a progress callback")


class BatchClient:
  """A context-managed, sequential client for one long-lived Go process."""

  def __init__(
      self,
      binary_path: os.PathLike[str] | str,
      *,
      check: bool = True,
      environment: Mapping[str, str] | None = None,
      progress: ProgressCallback | None = None,
      progress_step_interval: int = 0,
  ) -> None:
    _validate_progress(progress, progress_step_interval)
    self.binary = executable_path(binary_path)
    self.check = check
    self.environment = dict(environment or {})
    self.progress = progress
    self.progress_step_interval = progress_step_interval
    self._process: subprocess.Popen[str] | None = None
    self._stderr_thread: threading.Thread | None = None
    self._diagnostics: deque[str] = deque(maxlen=100)
    self._callback_errors: list[BaseException] = []
    self._request_lock = threading.Lock()

  def __enter__(self) -> BatchClient:  # noqa: PYI034 -- Python 3.10 support
    self.start()
    return self

  def __exit__(self, exc_type, exc_value, traceback) -> Literal[False]:
    if exc_type is not None:
      self.terminate()
      return False
    self.close()
    return False

  def start(self) -> None:
    if self._process is not None:
      return
    command = [self.binary]
    if self.progress is not None:
      command.extend(["--progress", "jsonl"])
      if self.progress_step_interval:
        command.extend([
            "--progress-step-interval",
            str(self.progress_step_interval),
        ])
    environment = dict(os.environ)
    environment.update(self.environment)
    self._process = subprocess.Popen(
        command,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        encoding="utf-8",
        bufsize=1,
        env=environment,
        **process_group_kwargs(),
    )
    process = self._process
    assert process.stderr is not None
    stderr = process.stderr

    def read_stderr() -> None:
      for raw_line in stderr:
        line = raw_line.rstrip("\n")
        try:
          event = decode_progress_line(line)
        except ProtocolError as error:
          if not self._callback_errors:
            self._callback_errors.append(error)
          continue
        if event is None:
          self._diagnostics.append(line)
        elif self.progress is not None and not self._callback_errors:
          try:
            self.progress(event)
          except Exception as error:  # noqa: BLE001 -- propagate callback failure
            self._callback_errors.append(error)

    self._stderr_thread = threading.Thread(
        target=read_stderr,
        name="smp-batch-progress",
        daemon=True,
    )
    self._stderr_thread.start()

  def _diagnostic_text(self) -> str:
    return "\n".join(item for item in self._diagnostics if item).strip()

  def _raise_callback_error(self) -> None:
    if self._callback_errors:
      raise self._callback_errors[0]

  def run(self, request: Mapping[str, Any]) -> dict[str, Any]:
    """Send one request and wait for its correlated response."""
    encoded = encode_batch_request(request)
    request_id = str(request["request_id"])
    with self._request_lock:
      self.start()
      self._raise_callback_error()
      process = self._process
      assert process is not None
      assert process.stdin is not None
      assert process.stdout is not None
      if process.poll() is not None:
        raise RuntimeError(
            f"smp-batch is not running: {self._diagnostic_text()}"
        )
      try:
        process.stdin.write(encoded + "\n")
        process.stdin.flush()
      except BrokenPipeError as error:
        raise RuntimeError(
            f"smp-batch closed stdin: {self._diagnostic_text()}"
        ) from error
      line = process.stdout.readline()
      if not line:
        returncode = process.poll()
        raise RuntimeError(
            f"smp-batch ended before responding to {request_id!r}"
            f" (exit {returncode}): {self._diagnostic_text()}"
        )
      response = decode_batch_response(
          line,
          expected_request_id=request_id,
      )
      self._raise_callback_error()
      if self.check and response["status"] == "error":
        raise BatchExecutionError(response)
      return response

  def close(self) -> None:
    """Close stdin and require a clean server exit."""
    process = self._process
    if process is None:
      return
    try:
      if process.stdin is not None and not process.stdin.closed:
        process.stdin.close()
      returncode = process.wait()
    except BaseException:
      terminate_process(process, process_group=True)
      raise
    finally:
      if self._stderr_thread is not None:
        self._stderr_thread.join()
      if process.stdout is not None:
        process.stdout.close()
      if process.stderr is not None:
        process.stderr.close()
      self._process = None
    if returncode != 0:
      raise RuntimeError(
          f"smp-batch exited with {returncode}: {self._diagnostic_text()}"
      )
    self._raise_callback_error()

  def terminate(self) -> None:
    """Terminate the server and release all pipes."""
    process = self._process
    if process is None:
      return
    terminate_process(process, process_group=True)
    if process.stdin is not None and not process.stdin.closed:
      process.stdin.close()
    if self._stderr_thread is not None:
      self._stderr_thread.join()
    if process.stdout is not None:
      process.stdout.close()
    if process.stderr is not None:
      process.stderr.close()
    self._process = None


def _request_list(
    requests: Iterable[Mapping[str, Any]],
) -> tuple[list[Mapping[str, Any]], dict[str, int]]:
  values = list(requests)
  indices: dict[str, int] = {}
  for index, request in enumerate(values):
    encode_batch_request(request)
    request_id = str(request["request_id"])
    if request_id in indices:
      raise ValueError(f"duplicate batch request_id {request_id!r}")
    indices[request_id] = index
  return values, indices


def run_batch(
    binary_path: os.PathLike[str] | str,
    requests: Iterable[Mapping[str, Any]],
    *,
    check: bool = True,
    environment: Mapping[str, str] | None = None,
    progress: ProgressCallback | None = None,
    progress_step_interval: int = 0,
) -> list[dict[str, Any]]:
  """Run requests through one Go process and preserve request order."""
  values, indices = _request_list(requests)
  if not values:
    return []

  def enriched(event: dict[str, Any]) -> None:
    if progress is None:
      return
    item = dict(event)
    item["request_index"] = indices[item["request_id"]] + 1
    item["request_total"] = len(values)
    item["process_index"] = 1
    item["process_total"] = 1
    progress(item)

  with BatchClient(
      binary_path,
      check=check,
      environment=environment,
      progress=enriched if progress is not None else None,
      progress_step_interval=progress_step_interval,
  ) as client:
    return [client.run(request) for request in values]


def run_batch_parallel(
    binary_path: os.PathLike[str] | str,
    requests: Iterable[Mapping[str, Any]],
    *,
    processes: int,
    check: bool = True,
    environment: Mapping[str, str] | None = None,
    progress: ProgressCallback | None = None,
    progress_step_interval: int = 0,
) -> list[dict[str, Any]]:
  """Load-balance requests over multiple long-lived Go processes."""
  values, indices = _request_list(requests)
  if not values:
    return []
  if processes < 1 or processes > len(values):
    raise ValueError("processes must be in [1, number of requests]")
  _validate_progress(progress, progress_step_interval)

  pending: queue.Queue[tuple[int, Mapping[str, Any]]] = queue.Queue()
  for index, request in enumerate(values):
    pending.put((index, request))
  ordered: list[dict[str, Any] | None] = [None] * len(values)
  callback_lock = threading.Lock()
  active_lock = threading.Lock()
  active: list[BatchClient] = []
  stop = threading.Event()

  def worker(process_index: int) -> None:
    def enriched(event: dict[str, Any]) -> None:
      if progress is None:
        return
      item = dict(event)
      item["request_index"] = indices[item["request_id"]] + 1
      item["request_total"] = len(values)
      item["process_index"] = process_index + 1
      item["process_total"] = processes
      with callback_lock:
        progress(item)

    client = BatchClient(
        binary_path,
        check=check,
        environment=environment,
        progress=enriched if progress is not None else None,
        progress_step_interval=progress_step_interval,
    )
    with active_lock:
      active.append(client)
    try:
      with client:
        while not stop.is_set():
          try:
            index, request = pending.get_nowait()
          except queue.Empty:
            break
          ordered[index] = client.run(request)
    except BaseException:
      stop.set()
      with active_lock:
        peers = list(active)
      for peer in peers:
        if peer is not client:
          peer.terminate()
      raise
    finally:
      with active_lock:
        if client in active:
          active.remove(client)

  with ThreadPoolExecutor(
      max_workers=processes,
      thread_name_prefix="smp-batch",
  ) as pool:
    futures = [pool.submit(worker, index) for index in range(processes)]
    for future in futures:
      future.result()

  responses = [item for item in ordered if item is not None]
  if len(responses) != len(values):
    raise RuntimeError("parallel batch execution lost responses")
  return responses
