import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import GraphViewer from '../components/GraphViewer'
import type { GraphVizNode, GraphVizEdge } from '../types'

// BookMapPage is kata cycle 54's own real answer to "what would the graph look like if the
// explorer just opened with all" - at real production scale (tens of thousands of nodes, growing
// live during background book ingestion) that question's own honest answer is "an unreadable,
// unrenderable hairball," not a feature to build. What IS real and renderable: the corpus's own
// macro-structure - which books relate to which, via real RELATED_TO edges kata cycle 54 computes
// incrementally as chunks are ingested (see journal.go's own appendBookChunkWithVector). Book
// count stays small (tens to hundreds) regardless of how large the chunk corpus grows, so this
// stays a real, fast, legible view no matter how long ingestion has been running.
//
// Both API calls this page makes (getNodesByLabel/getNodeEdgesByLabel) are fully generic - see
// client.ts's own comment - this page is simply the first real caller of them.
export default function BookMapPage() {
  const navigate = useNavigate()
  const [nodes, setNodes] = useState<GraphVizNode[]>([])
  const [edges, setEdges] = useState<GraphVizEdge[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const { nodes: books } = await api.getNodesByLabel('Book')
      const vizNodes: GraphVizNode[] = books.map((b) => ({ id: b.id, props: b.props, label: 'Book' }))

      // Real edges are fetched per-book (book count is small - this is the whole real point of
      // kata cycle 54's own scalable design) and de-duplicated, since a real bridge is
      // discoverable from EITHER book's own edge list (RELATED_TO is stored both directions).
      const seen = new Map<string, GraphVizEdge>()
      let edgeID = 0
      await Promise.all(
        books.map(async (b) => {
          const { edges: bookEdges } = await api.getNodeEdgesByLabel(b.id, 'RELATED_TO')
          for (const e of bookEdges) {
            const key = e.from < e.to ? `${e.from}-${e.to}` : `${e.to}-${e.from}`
            if (!seen.has(key)) {
              seen.set(key, { id: edgeID++, from: e.from, to: e.to, label: e.label })
            }
          }
        })
      )

      setNodes(vizNodes)
      setEdges([...seen.values()])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  return (
    <div className="h-full flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold text-white">Book Map</h1>
          <p className="text-sm text-slate-400">
            {loading
              ? 'Loading real books and bridges...'
              : `${nodes.length} real book${nodes.length === 1 ? '' : 's'}, ${edges.length} real cross-book connection${edges.length === 1 ? '' : 's'}`}
          </p>
        </div>
        <button
          onClick={load}
          className="px-3 py-1.5 text-sm rounded-md bg-slate-800 text-slate-200 hover:bg-slate-700 border border-slate-700"
        >
          Refresh
        </button>
      </div>

      {error && (
        <div className="px-4 py-2 rounded-md bg-red-950 border border-red-800 text-red-300 text-sm">
          {error}
        </div>
      )}

      {!loading && nodes.length === 0 && !error && (
        <div className="flex-1 flex items-center justify-center text-slate-600 text-sm">
          No real Book nodes yet.
        </div>
      )}

      {nodes.length > 0 && (
        <div className="flex-1 min-h-0">
          <GraphViewer
            nodes={nodes}
            edges={edges}
            onNodeClick={(id) => navigate(`/explorer?node=${id}`)}
          />
        </div>
      )}

      {!loading && nodes.length > 0 && edges.length === 0 && (
        <p className="text-xs text-slate-500">
          Every real book here is still its own island - no cross-book semantic overlap has crossed the real
          0.75 similarity threshold yet. That's a genuine, honest reading of the corpus, not a bug: real
          bridges appear here as ingestion continues, computed incrementally, not retroactively.
        </p>
      )}
    </div>
  )
}
