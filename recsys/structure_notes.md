# Analysis and Optimization of the Recommendation Algorithm

The original algorithm’s sluggish performance under frequent rewiring (like in the voter model) is not due to a minor inefficiency, but rather fundamental structural flaws.

## 1. Core Issues with the Old Algorithm

* **Structural Bug:** The `PostStep` logic is flawed. During a rewiring event (`{Unfollow, Follow}`), the algorithm updates the shared neighbors of the target nodes (B and C) but completely misses the acting agent (A). Consequently, the $N \times N$ matrix becomes permanently stale after initialization.
* **Severe Inefficiency:** `PreStep` forces a full $O(N^2)$ matrix rebuild every single step, **even if no edges change**. When combined with the frequent rewiring of the voter model, `PostStep` adds a massive $O(N^2D)$ overhead.

## 2. The Solution: Matrix-Free, On-Demand Computation

To optimize this, we abandon the pre-computed $N \times N$ matrices (`ConnMat`/`RateMat`). Instead, we dynamically calculate the 2-hop neighborhood only when `Recommend` is called.

**Complexity Comparison (on-demand, no cache):**

| Phase | Old Approach | On-Demand (useCache=false) |
| --- | --- | --- |
| **`PreStep`** | $O(N^2)$ (forced every step) | **$O(1)$ (No-op)** |
| **`PostStep`** | $O(N^2D)$ (plus logical bugs) | **$O(1)$ (No-op)** |
| **`Recommend`** | $O(N)$ row scan | **$O(D^2)$ graph traversal** |
| **Total per step** | $O(N^2)$ minimum | **$O(N \cdot D^2)$** |
| **Memory** | $O(N^2)$ permanent | **$O(D^2)$ temporary** |

*Note: In the new approach, noise sampling is done per recommendation (which is more mathematically sound for agent-based models). When `useCache=true`, `Dump()` / `PostInit()` persist and restore the cache across snapshots.*

## 3. Is the New Method Always Better?

The new approach is vastly superior for **sparse graphs**, which applies to almost all social network simulations.

* **The Break-Even Point:** The new algorithm's cost is $O(N \cdot D^2)$ compared to the old $O(N^2)$. The old method only becomes faster if the average degree $D > \sqrt{N}$.
* **Real-World Application:** In a network of $N = 5000$, a node would need an average degree of $D > 70$ for the old method to win. Since social networks usually have a much lower degree ($D \ll \sqrt{N}$), the new method guarantees better performance regardless of rewiring frequency.
* **Future-Proofing for Dense Graphs:** If you eventually need to run simulations on highly dense networks, you could introduce **row-level lazy caching** (invalidating a cache only when a 1-hop or 2-hop edge actually changes). This has now been implemented — see §4 below.

## 4. Row-Level Lazy Caching (`useCache=true`)

For scenarios with infrequent rewiring (many steps with no topology change), the on-demand approach still recomputes an $O(D^2)$ graph traversal every `Recommend` call. The **row-level lazy cache** eliminates this by reusing results until the neighborhood actually changes.

### Cache Structures (on `Structure`)

| Field | Type | Purpose |
| --- | --- | --- |
| `candidateCache` | `map[int64][]int64` | Sorted candidate agent IDs for `Structure.Recommend` (noise applied at fill time) |
| `rawScoreCache` | `map[int64]map[int64]float64` | Raw 2-hop common-neighbor counts, reused by `StructureRandom.Recommend` |
| `cacheValid` | `map[int64]bool` | Per-agent validity flag |

### Invalidation (`PostStep`)

Called after each step's rewiring commits. For each rewiring event $(A \text{ unfollows } B, A \text{ follows } C)$, every agent whose 2-hop neighborhood could have changed is invalidated:

1. $A$ itself (1-hop changed)
2. All undirected 1-hop neighbors of $A$
3. $B$ (1-hop changed: $A$ removed)
4. All undirected 1-hop neighbors of $B$
5. $C$ (1-hop changed: $A$ added)
6. All undirected 1-hop neighbors of $C$

This invalidation is conservative (never misses a stale entry) and requires `AgentID` (the acting agent $A$) in each rewiring event — added to `model.RewiringEventBody`.

### Performance Summary

| Scenario | `Structure.Recommend` | `StructureRandom.Recommend` |
| --- | --- | --- |
| **No rewiring (cache hit)** | **$O(k)$** | **$O(D^2 + N)$** |
| **High rewiring (cache miss)** | $O(D^2 \log D)$ | $O(D^2 + N)$ |

### Enabling the Cache

Pass `useCache=true` to `NewStructure` / `NewStructureRandom`. This parameter **replaces** the old no-op `matrixInit` parameter. With `useCache=false` behaviour is identical to the plain on-demand approach.

### Serialization (`Dump` / `PostInit`)

When `useCache=true`, `Dump()` marshals `candidateCache`, `rawScoreCache`, and `cacheValid` via msgpack into `SMPModelDumpData.RecsysDumpData`. On snapshot restore, `PostInit(dumpData)` deserializes and restores the cache, so agents that were already cached do not need to recompute on the first step after a resume.
