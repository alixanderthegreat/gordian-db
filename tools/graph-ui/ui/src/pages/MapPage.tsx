import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import GraphViewer, { FORCE_MAX_NODES } from '../components/GraphViewer'
import type { GraphVizNode, GraphVizEdge } from '../types'

// MAP_NODE_BUDGET is a real, named ceiling on how many nodes the map will try to lay out at once.
// Grounded, not arbitrary: a real 3,892-node domain graph (the jobs graph this view was built
// against) renders fine, while the book corpus at 30,000+ nodes provably does not - the honest
// line sits between them, and going over it means saying so rather than hanging the browser.
const MAP_NODE_BUDGET = 6000

// MAP_PROP_CHARS is how much of any one string prop the map asks the server for. A map node renders
// a single short display label and nothing else, so anything past this is downloaded and thrown
// away. Measured on the real jobs graph: HAS_KEYWORD's 2,601 nodes cost 3.88 MB at full props,
// because one real JobListing carries up to 14.9 KB of `description` free text - and 198 KB capped.
// 120 is comfortably longer than any label the viewer can actually draw.
const MAP_PROP_CHARS = 120

// MapPage is kata cycle 57's own real generalization of the Book Map. The control is an EDGE
// label, not a node label, because an edge label already determines its own endpoints - which is
// the only thing that works for a genuinely heterogeneous graph (JobListing -POSTED_BY-> Employer
// and JobListing -HAS_KEYWORD-> Keyword have different labels on each end; a node-label picker
// cannot express either). RELATED_TO reproduces the old Book Map for free.
//
// Counts are always fetched FIRST, before any payload (kata cycle 55's own discipline): the user
// sees every real edge label with its real count and chooses what to render, rather than the view
// discovering a 1.4MB response by freezing on it.
export default function MapPage() {
  const navigate = useNavigate()
  const [counts, setCounts] = useState<Record<string, number>>({})
  const [enabled, setEnabled] = useState<Set<string>>(new Set())
  const [nodes, setNodes] = useState<GraphVizNode[]>([])
  const [edges, setEdges] = useState<GraphVizEdge[]>([])
  const [loadingCounts, setLoadingCounts] = useState(true)
  const [rendering, setRendering] = useState(false)
  const [truncated, setTruncated] = useState<string[]>([])
  const [overBudget, setOverBudget] = useState(0)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    api
      .getEdgeCounts()
      .then((res) => {
        if (cancelled) return
        setCounts(res.counts ?? {})
        // Default to the SMALLEST real edge label - the one guaranteed safe to render, and in
        // practice usually the most interesting (a sparse edge type is sparse because it means
        // something specific).
        const entries = Object.entries(res.counts ?? {}).sort((a, b) => a[1] - b[1])
        if (entries.length > 0) setEnabled(new Set([entries[0][0]]))
      })
      .catch((e) => !cancelled && setError(e instanceof Error ? e.message : String(e)))
      .finally(() => !cancelled && setLoadingCounts(false))
    return () => {
      cancelled = true
    }
  }, [])

  const render = useCallback(async (labels: Set<string>) => {
    if (labels.size === 0) {
      setNodes([])
      setEdges([])
      setTruncated([])
      setOverBudget(0)
      return
    }
    setRendering(true)
    setError(null)
    try {
      const allEdges: GraphVizEdge[] = []
      const wasTruncated: string[] = []
      let nextId = 0
      for (const label of labels) {
        const res = await api.getEdgesByLabel(label)
        if (res.truncated) wasTruncated.push(label)
        for (const e of res.edges) {
          allEdges.push({ id: nextId++, from: e.from, to: e.to, label: e.label })
        }
      }

      const ids = Array.from(new Set(allEdges.flatMap((e) => [e.from, e.to])))
      if (ids.length > MAP_NODE_BUDGET) {
        setOverBudget(ids.length)
        setNodes([])
        setEdges([])
        setTruncated(wasTruncated)
        return
      }
      setOverBudget(0)

      // Resolve in real chunks - a single URL carrying thousands of ids is its own problem.
      const resolved: GraphVizNode[] = []
      const CHUNK = 500
      for (let i = 0; i < ids.length; i += CHUNK) {
        const res = await api.getNodesBatch(ids.slice(i, i + CHUNK), MAP_PROP_CHARS)
        resolved.push(...res.nodes)
      }

      setNodes(resolved)
      setEdges(allEdges)
      setTruncated(wasTruncated)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setRendering(false)
    }
  }, [])

  useEffect(() => {
    render(enabled)
  }, [enabled, render])

  const toggle = (label: string) => {
    setEnabled((prev) => {
      const next = new Set(prev)
      if (next.has(label)) next.delete(label)
      else next.add(label)
      return next
    })
  }

  const selectedEdgeTotal = Array.from(enabled).reduce((sum, l) => sum + (counts[l] ?? 0), 0)

  return (
    <div className="h-full flex flex-col gap-4">
      <div>
        <h1 className="text-lg font-semibold text-white">Map</h1>
        <p className="text-sm text-slate-400">
          {loadingCounts
            ? 'Counting real edges...'
            : `${Object.keys(counts).length} real edge type${Object.keys(counts).length === 1 ? '' : 's'} in this graph`}
        </p>
      </div>

      {error && (
        <div className="px-4 py-2 rounded-md bg-red-950 border border-red-800 text-red-300 text-sm">
          {error}
        </div>
      )}

      <div className="flex flex-wrap gap-2">
        {Object.entries(counts)
          .sort((a, b) => b[1] - a[1])
          .map(([label, count]) => {
            const on = enabled.has(label)
            return (
              <button
                key={label}
                onClick={() => toggle(label)}
                className={`px-3 py-1.5 text-sm rounded-md border transition-colors ${
                  on
                    ? 'bg-blue-500/15 border-blue-500/50 text-blue-300'
                    : 'bg-slate-800 border-slate-700 text-slate-400 hover:bg-slate-700'
                }`}
              >
                {label} <span className="opacity-60">({count})</span>
              </button>
            )
          })}
      </div>

      {truncated.length > 0 && (
        <p className="text-xs text-amber-400">
          Showing only the first slice of: {truncated.join(', ')} — this is not the whole set.
        </p>
      )}

      {overBudget > 0 ? (
        <div className="flex-1 flex items-center justify-center">
          <div className="text-center max-w-md">
            <p className="text-sm text-slate-300">
              {overBudget.toLocaleString()} nodes across {selectedEdgeTotal.toLocaleString()} edges —
              over the {MAP_NODE_BUDGET.toLocaleString()} budget for one map.
            </p>
            <p className="text-xs text-slate-500 mt-2">
              Turn off an edge type to bring it under. Refusing to render is deliberate: laying this
              out would freeze the browser rather than show you anything.
            </p>
          </div>
        </div>
      ) : (
        <div className="flex-1 min-h-0">
          {rendering ? (
            <div className="w-full h-full flex items-center justify-center text-slate-600 text-sm">
              Loading {selectedEdgeTotal.toLocaleString()} real edges...
            </div>
          ) : (
            <GraphViewer
              nodes={nodes}
              edges={edges}
              layout="force"
              onNodeClick={(id) => navigate(`/explorer?node=${id}`)}
            />
          )}
        </div>
      )}

      {!rendering && !overBudget && nodes.length > 0 && (
        <p className="text-xs text-slate-600">
          {nodes.length.toLocaleString()} nodes · {edges.length.toLocaleString()} edges · click any
          node to open it in the Explorer
          {nodes.length > FORCE_MAX_NODES && (
            // Say it out loud rather than silently swapping algorithms: past the measured cose
            // ceiling the map ranks by degree instead of simulating forces, so hubs sit in the
            // middle. A view that quietly changes what it is showing is worse than a slow one.
            <> · over {FORCE_MAX_NODES.toLocaleString()} nodes, laid out by degree (hubs centered)
            rather than force-simulated</>
          )}
        </p>
      )}
    </div>
  )
}
