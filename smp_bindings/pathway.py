"""Measure frozen graph and opinion rows with the online Go pathway kernel."""

from collections.abc import Iterable, Iterator, Sequence
from os import PathLike

from smp_bindings.batch import BatchClient


def measure_pathways(
    binary: str | PathLike[str],
    states: Iterable[tuple[int, Sequence[float], Iterable[tuple[int, int]]]],
    epsilon: float,
) -> Iterator[tuple[int, dict]]:
  """Stream successive observations through one worker, consuming each edge iterable immediately."""
  with BatchClient(binary) as client:
    try:
      for step, opinions, edges in states:
        request = {
            "request_id": f"pathway-{step}",
            "measure_pathway": {
                "epsilon": float(epsilon),
                "opinions": [float(value) for value in opinions],
                "edges": [[int(i), int(j)] for i, j in edges],
            },
        }
        response = client.run(request)
        yield step, response["result"]["pathway"]
    except GeneratorExit:
      # Early first passage leaves the worker idle. Close stdin normally.
      return
