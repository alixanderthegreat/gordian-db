import type { GraphVizEdge, GraphVizNode } from '../types'
import { nodeDisplayLabel } from './label'

export interface ResolvedEdge {
  edge: GraphVizEdge
  otherNode: GraphVizNode
  direction: 'out' | 'in'
}

// SortMode is kata cycle 47's own real, scoped answer to "can we get a sort tool" - deliberately
// limited to the Edges panel (all its data is already client-side), not the main paginated node
// list (a real, separate, bigger question - sorting 11,000+ nodes by something other than id
// needs a real new backend secondary index, not a dropdown).
export type SortMode = 'id' | 'label' | 'connections'

// resolveEdges pairs each real edge with its OTHER node (relative to centerId) and a real
// direction - the shared resolution step every sort mode needs, split out from the actual
// ordering so callers needing just the pairing (without a specific sort) can reuse it too.
function resolveEdges(edges: GraphVizEdge[], neighbors: GraphVizNode[], centerId: number): ResolvedEdge[] {
  const byId = new Map(neighbors.map((n) => [n.id, n]))
  const resolved: ResolvedEdge[] = []
  for (const e of edges) {
    const isOut = e.from === centerId
    const otherId = isOut ? e.to : e.from
    const otherNode = byId.get(otherId)
    // A real defensive skip, not expected in practice (server.go's own handleNeighborhood
    // already only ever returns edges whose other endpoint resolves - kata cycle 43) - kept here
    // as a real safety net against any future edge case, not a silent assumption.
    if (!otherNode) continue
    resolved.push({ edge: e, otherNode, direction: isOut ? 'out' : 'in' })
  }
  return resolved
}

// sortEdges is the one, shared real ordering for a node's own neighborhood. 'id' mode (the
// default, and GraphViewer's own always-on behavior) sorts by the other node's real chunk_index
// prop when present (BookChunk's own real, natural sequence - the exact real case that motivated
// this: a Book's 310 real chunks), falling back to the node's own id otherwise. 'label' sorts
// lexicographically by the same real display text already shown (nodeDisplayLabel) - free,
// needs no extra data. 'connections' sorts by each node's own real total degree, descending
// (highest-connected first) - needs a real degree map (kata cycle 47's own new
// /api/nodes/degrees endpoint); nodes missing from the map (not yet fetched) sort as degree 0,
// not an error.
export function sortEdges(
  edges: GraphVizEdge[],
  neighbors: GraphVizNode[],
  centerId: number,
  mode: SortMode = 'id',
  degrees?: Record<string, number>,
): ResolvedEdge[] {
  const resolved = resolveEdges(edges, neighbors, centerId)

  if (mode === 'label') {
    resolved.sort((a, b) => nodeDisplayLabel(a.otherNode).localeCompare(nodeDisplayLabel(b.otherNode)))
    return resolved
  }

  if (mode === 'connections') {
    resolved.sort((a, b) => {
      const da = degrees?.[String(a.otherNode.id)] ?? 0
      const db = degrees?.[String(b.otherNode.id)] ?? 0
      return db - da
    })
    return resolved
  }

  resolved.sort((a, b) => {
    const aIdx = a.otherNode.props?.chunk_index
    const bIdx = b.otherNode.props?.chunk_index
    if (typeof aIdx === 'number' && typeof bIdx === 'number') {
      return aIdx - bIdx
    }
    if (typeof aIdx === 'number') return -1
    if (typeof bIdx === 'number') return 1
    return a.otherNode.id - b.otherNode.id
  })
  return resolved
}
