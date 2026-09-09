// Trimmed for gordian-db's own small graph-ui (kata cycle 36) - only the 6 real endpoints
// tools/graph-ui/server.go actually serves. The full goraphdb-ui client (Cypher, indexes,
// metrics, slow queries, cluster) is deliberately not ported - see server.go's own package doc.
import type { NodeCursorPage, GNode, NeighborhoodResponse, CursorNode, GraphVizEdge, GraphVizNode } from '../types'

const BASE = '/api'

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + url, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || `HTTP ${res.status}`)
  }
  return res.json()
}

export const api = {
  getStats: () => fetchJSON<{ node_count: number; edge_count: number }>('/stats'),

  getNode: (id: number) => fetchJSON<GNode>(`/nodes/${id}`),
  getNeighborhood: (id: number) =>
    fetchJSON<NeighborhoodResponse>(`/nodes/${id}/neighborhood`),
  deleteNode: (id: number) =>
    fetchJSON<{ status: string }>(`/nodes/${id}`, { method: 'DELETE' }),

  createEdge: (from: number, to: number, label: string) =>
    fetchJSON<{ id: number }>('/edges', {
      method: 'POST',
      body: JSON.stringify({ from, to, label }),
    }),

  listNodesCursor: (cursor = -1, limit = 30) =>
    fetchJSON<NodeCursorPage>(`/nodes/cursor?cursor=${cursor}&limit=${limit}`),

  searchNodes: (q: string, cursor = -1, limit = 30) =>
    fetchJSON<NodeCursorPage>(`/nodes/search?q=${encodeURIComponent(q)}&cursor=${cursor}&limit=${limit}`),

  getNodeDegrees: (ids: number[]) =>
    fetchJSON<{ degrees: Record<string, number> }>(`/nodes/degrees?ids=${ids.join(',')}`),

  // kata cycle 54's own two real, generic primitives - see graphui/server.go's own doc comments
  // for why these are label-generic, not "book map"-specific.
  getNodesByLabel: (label: string) =>
    fetchJSON<{ nodes: CursorNode[] }>(`/nodes/by-label?label=${encodeURIComponent(label)}`),

  getNodeEdgesByLabel: (id: number, label: string) =>
    fetchJSON<{ edges: GraphVizEdge[] }>(`/nodes/${id}/edges?label=${encodeURIComponent(label)}`),

  // kata cycle 55's own two real, generic primitives - see graphui/server.go's own doc comments.
  // getNodeEdgeCounts is deliberately cheaper than getNeighborhood: real per-label counts, no
  // neighbor resolution, so a caller can decide whether a node is safe to fully load BEFORE
  // paying for it. getNodesBatch resolves a bounded, chosen id set in one call, for rendering
  // just one label's worth of a high-degree node's own edges without an N+1 fetch pattern.
  getNodeEdgeCounts: (id: number) =>
    fetchJSON<{ counts: Record<string, number> }>(`/nodes/${id}/edge-counts`),

  getNodesBatch: (ids: number[]) =>
    fetchJSON<{ nodes: GraphVizNode[] }>(`/nodes/batch?ids=${ids.join(',')}`),
}
