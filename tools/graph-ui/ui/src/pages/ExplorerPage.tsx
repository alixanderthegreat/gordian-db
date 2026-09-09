import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { ChevronLeft, ChevronRight, Trash2, Search, X, ArrowRight, ArrowLeft } from 'lucide-react'
import GraphViewer from '../components/GraphViewer'
import { api } from '../api/client'
import type { GNode, GraphVizNode, GraphVizEdge, CursorNode } from '../types'
import { nodeDisplayLabel } from '../lib/label'
import { sortEdges, type SortMode } from '../lib/sortEdges'

const PAGE_SIZE = 30

export default function ExplorerPage() {
  const [nodes, setNodes] = useState<CursorNode[]>([])
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  // The real next_cursor from the server's own last response - kata cycle 42's own real fix:
  // goNext used to derive the next page's cursor from `nodes[last].id`, which happens to equal
  // the real next_cursor for normal listing (ListNodes' own contract) but is WRONG for search -
  // a search's real scan position can be past the last MATCHED node (it also advances through
  // every non-matching node it skipped), so the server's own returned value must be used
  // directly, never re-derived from the visible results.
  const [nextCursor, setNextCursor] = useState(-1)

  // Cursor stack for back/forward navigation - starts at -1 (gordian-db's own "start from the
  // beginning" sentinel, since node id 0 is itself real and valid - see Graph.ListNodes'
  // own doc comment). Trimmed from goraphdb-ui's own [0] start, which assumed 0 meant
  // "nothing seen yet" - not true for gordian-db's own 0-indexed node ids.
  const cursorStack = useRef<number[]>([-1])
  const [stackIdx, setStackIdx] = useState(0)

  // Selection state
  const [selectedNode, setSelectedNode] = useState<GNode | null>(null)
  const [graphNodes, setGraphNodes] = useState<GraphVizNode[]>([])
  const [graphEdges, setGraphEdges] = useState<GraphVizEdge[]>([])
  const [edgeCount, setEdgeCount] = useState(0)

  // Real sort control for the Edges panel (kata cycle 47) - deliberately scoped to just this
  // list, not the main paginated node list (that's a real, separate, bigger question - see this
  // cycle's own kata record). 'connections' needs each neighbor's own real total degree, fetched
  // on demand via the new /api/nodes/degrees endpoint only when that mode is actually selected -
  // id/label sort need no extra round trip, already free from data already in hand.
  const [sortMode, setSortMode] = useState<SortMode>('id')
  const [degrees, setDegrees] = useState<Record<string, number>>({})
  useEffect(() => {
    if (sortMode !== 'connections' || graphNodes.length === 0) return
    let cancelled = false
    api.getNodeDegrees(graphNodes.map((n) => n.id)).then((res) => {
      if (!cancelled) setDegrees(res.degrees ?? {})
    })
    return () => {
      cancelled = true
    }
  }, [sortMode, graphNodes])

  // Real, shared ordering (kata cycle 46, extended in cycle 47 with a real sort mode) - the same
  // sortEdges.ts logic GraphViewer's own concentric layout uses for its own default ('id') mode,
  // so the enumerated list below and the graph ring agree whenever 'id' is selected; label/
  // connections modes are this list's own real addition, not mirrored in the graph ring (a
  // deliberate scope line - see this cycle's own kata record).
  const sortedEdges = useMemo(
    () => (selectedNode ? sortEdges(graphEdges, graphNodes, selectedNode.id, sortMode, degrees) : []),
    [graphEdges, graphNodes, selectedNode, sortMode, degrees],
  )

  // Load node list using cursor pagination
  const loadList = useCallback(async (cursor: number) => {
    setLoading(true)
    try {
      const res = await api.listNodesCursor(cursor, PAGE_SIZE)
      setNodes(res.nodes ?? [])
      setHasMore(res.has_more)
      setNextCursor(res.next_cursor)
    } catch (e: any) {
      console.error('Failed to load nodes:', e)
    } finally {
      setLoading(false)
    }
  }, [])

  // Real search - kata cycle 40, found necessary live ("I have no way to search id or name") at
  // real production scale (11,101+ nodes - paging 30 at a time to find one specific node isn't
  // practical). A query replaces the cursor-paginated list with real search results; clearing it
  // falls back to normal pagination.
  //
  // Real pagination for search itself - kata cycle 42's own fix: the original "no load more,
  // refine your query instead" design broke down completely for a genuinely common real term
  // (486 real matches for "wife" in the actual corpus; the exact node being searched for was past
  // the original 30-result cap with no way to reach it). Search now reuses the SAME
  // cursorStack/stackIdx state as normal browsing - Next/Prev works identically either way.
  const [query, setQuery] = useState('')
  const runSearch = useCallback(async (q: string, cursor: number) => {
    setLoading(true)
    try {
      const res = await api.searchNodes(q, cursor, PAGE_SIZE)
      setNodes(res.nodes ?? [])
      setHasMore(res.has_more)
      setNextCursor(res.next_cursor)
    } catch (e: any) {
      console.error('Search failed:', e)
    } finally {
      setLoading(false)
    }
  }, [])

  // A NEW search term always restarts pagination from the beginning - an old cursor position
  // from a previous query has no meaning against a different query.
  const setSearchQuery = (q: string) => {
    setQuery(q)
    cursorStack.current = [-1]
    setStackIdx(0)
  }

  useEffect(() => {
    const cursor = cursorStack.current[stackIdx]
    if (query.trim() === '') {
      loadList(cursor)
      return
    }
    const t = setTimeout(() => runSearch(query.trim(), cursor), 250)
    return () => clearTimeout(t)
  }, [loadList, runSearch, stackIdx, query])

  // HIGH_DEGREE_THRESHOLD is kata cycle 55's own real line: 310 real edges (a real book, cited in
  // kata cycle 45's own notes) already renders fine today, so the threshold sits comfortably
  // above that proven-working case rather than restricting the common path - 1,571 real edges
  // (node 33175, found live) is what actually broke the Edges panel and the graph view, not
  // anything close to 310.
  const HIGH_DEGREE_THRESHOLD = 300

  const [edgeCounts, setEdgeCounts] = useState<Record<string, number>>({})
  const [needsLabelChoice, setNeedsLabelChoice] = useState(false)

  // loadFullNeighborhood is selectNode's own former real body, unchanged - the common case (a
  // node under the real threshold) behaves exactly as it always has, no regression.
  const loadFullNeighborhood = useCallback(async (nodeId: number) => {
    const hood = await api.getNeighborhood(nodeId)
    const vizNodes: GraphVizNode[] = [hood.center, ...hood.neighbors]
    setGraphNodes(vizNodes)
    setGraphEdges(hood.edges)
    setEdgeCount(hood.edges.length)
  }, [])

  // loadFilteredByLabel is kata cycle 55's own real answer for a node OVER the threshold: fetch
  // just ONE label's worth of real edges (already proven cheap - kata cycle 54), then resolve
  // just those real neighbor ids (plus the center's own id, for its real label/props) via the new
  // batch endpoint - never the full, unbounded neighborhood.
  const loadFilteredByLabel = useCallback(async (node: GNode, label: string) => {
    const { edges } = await api.getNodeEdgesByLabel(node.id, label)
    const otherIds = Array.from(new Set(edges.map((e) => (e.from === node.id ? e.to : e.from))))
    const { nodes: resolved } = await api.getNodesBatch([node.id, ...otherIds])
    setGraphNodes(resolved)
    setGraphEdges(edges)
    setEdgeCount(edges.length)
    setNeedsLabelChoice(false)
  }, [])

  // Select and explore a node - checks the real, cheap per-label edge COUNT first (kata cycle 55)
  // before ever attempting the full, unbounded neighborhood fetch that broke on node 33175's own
  // 1,571 real edges.
  const selectNode = useCallback(async (node: GNode) => {
    setSelectedNode(node)
    setNeedsLabelChoice(false)
    setGraphNodes([])
    setGraphEdges([])
    try {
      const { counts } = await api.getNodeEdgeCounts(node.id)
      setEdgeCounts(counts)
      const total = Object.values(counts).reduce((a, b) => a + b, 0)
      if (total > HIGH_DEGREE_THRESHOLD) {
        setNeedsLabelChoice(true)
        setEdgeCount(total)
        return
      }
      await loadFullNeighborhood(node.id)
    } catch (e: any) {
      console.error('Failed to load node edge counts:', e)
    }
  }, [loadFullNeighborhood])

  // Explore a node from the graph (click-to-expand)
  const exploreById = useCallback(async (id: number) => {
    try {
      const n = await api.getNode(id)
      selectNode(n)
    } catch {
      // ignore — node might have been deleted
    }
  }, [selectNode])

  // Real deep-link support (kata cycle 54) - MapPage's own onNodeClick navigates here with
  // ?node=<id> so clicking a book in the map opens that exact book's own real neighborhood,
  // rather than landing on a blank Explorer. Fires once per real id in the URL, not on every
  // render (the [searchParams] dependency only changes when the URL itself does).
  const [searchParams] = useSearchParams()
  useEffect(() => {
    const raw = searchParams.get('node')
    if (!raw) return
    const id = parseInt(raw, 10)
    if (!isNaN(id)) exploreById(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchParams])

  // Delete a node
  const deleteNode = async (id: number) => {
    if (!confirm(`Delete node ${id} and all its edges?`)) return
    try {
      await api.deleteNode(id)
      if (selectedNode?.id === id) {
        setSelectedNode(null)
        setGraphNodes([])
        setGraphEdges([])
      }
      const cursor = cursorStack.current[stackIdx]
      if (query.trim() !== '') {
        await runSearch(query.trim(), cursor)
      } else {
        await loadList(cursor)
      }
    } catch (e: any) {
      console.error(e)
    }
  }

  const goNext = () => {
    if (!hasMore) return
    // Push the real server-returned next_cursor onto the stack if we're at the end - NOT
    // derived from the visible nodes (see nextCursor's own doc comment above: a search's real
    // scan position can be past the last matched node).
    if (stackIdx === cursorStack.current.length - 1) {
      cursorStack.current.push(nextCursor)
    }
    setStackIdx(stackIdx + 1)
  }

  const goPrev = () => {
    if (stackIdx <= 0) return
    setStackIdx(stackIdx - 1)
  }

  const displayLabel = (n: CursorNode) =>
    nodeDisplayLabel({ id: n.id, label: n.labels?.[0] ?? 'Node', props: n.props })

  return (
    <div className="flex gap-5 h-[calc(100vh-48px)]">
      {/* Left: node list */}
      <div className="w-72 flex-shrink-0 flex flex-col">
        <h1 className="text-2xl font-bold text-white mb-4">Explorer</h1>

        <div className="relative mb-3">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-slate-600" />
          <input
            type="text"
            value={query}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search by id or name…"
            className="w-full bg-slate-900 border border-slate-800 rounded-lg pl-9 pr-8 py-2 text-sm text-white placeholder-slate-600 focus:outline-none focus:border-blue-500/50"
          />
          {query !== '' && (
            <button
              onClick={() => setSearchQuery('')}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-600 hover:text-slate-300"
              title="Clear search"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          )}
        </div>

        <div className="flex-1 overflow-auto bg-slate-900 border border-slate-800 rounded-xl">
          {loading ? (
            <div className="flex items-center justify-center h-32 text-slate-500 text-sm">
              Loading…
            </div>
          ) : nodes.length === 0 ? (
            <div className="flex items-center justify-center h-32 text-slate-600 text-sm">
              {query.trim() !== '' ? `No matches for "${query.trim()}"` : 'No nodes found'}
            </div>
          ) : (
            <div className="divide-y divide-slate-800/50">
              {nodes.map((node) => (
                <div
                  key={node.id}
                  onClick={() => selectNode({ id: node.id, props: node.props })}
                  className={`flex items-center justify-between px-4 py-3 cursor-pointer transition-colors ${
                    selectedNode?.id === node.id
                      ? 'bg-blue-500/10 border-l-2 border-blue-400'
                      : 'hover:bg-slate-800/50 border-l-2 border-transparent'
                  }`}
                >
                  <div className="min-w-0">
                    <p className="text-sm font-medium text-white truncate">
                      {displayLabel(node)}
                    </p>
                    <p className="text-[11px] text-slate-500 font-mono">
                      ID: {node.id}
                    </p>
                  </div>
                  <button
                    onClick={(e) => {
                      e.stopPropagation()
                      deleteNode(node.id)
                    }}
                    className="p-1 text-slate-700 hover:text-red-400 flex-shrink-0 transition-colors"
                    title="Delete node"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Pagination - real, working during an active search too (kata cycle 42): search
            results page through the full real result set exactly like normal browsing, using
            the same cursorStack/stackIdx state and the same Next/Prev controls. */}
        <div className="flex items-center justify-between mt-3 text-xs text-slate-500">
          <span>
            {query.trim() !== ''
              ? nodes.length === 0
                ? 'no matches'
                : `${nodes.length} match${nodes.length === 1 ? '' : 'es'}${hasMore ? ' (more →)' : ''}`
              : nodes.length === 0
                ? 'empty'
                : `${nodes.length} nodes${hasMore ? ' (more →)' : ''}`}
          </span>
          <div className="flex gap-1">
            <button
              onClick={goPrev}
              disabled={stackIdx === 0}
              className="p-1 rounded hover:bg-slate-800 disabled:opacity-30 transition-colors"
            >
              <ChevronLeft className="w-4 h-4" />
            </button>
            <button
              onClick={goNext}
              disabled={!hasMore}
              className="p-1 rounded hover:bg-slate-800 disabled:opacity-30 transition-colors"
            >
              <ChevronRight className="w-4 h-4" />
            </button>
          </div>
        </div>
      </div>

      {/* Right: detail + graph */}
      <div className="flex-1 flex flex-col min-w-0">
        {selectedNode ? (
          <>
            {/* Node properties card */}
            <div className="bg-slate-900 border border-slate-800 rounded-xl p-5 mb-4 flex-shrink-0">
              <div className="flex items-center justify-between mb-3">
                <h2 className="text-sm font-semibold text-slate-400 uppercase tracking-wider">
                  Node {selectedNode.id}
                </h2>
                <span className="text-xs text-slate-600">
                  {edgeCount} edge(s)
                </span>
              </div>
              {/* Real fix, kata cycle 41: props used to be single-line CSS-truncated with the
                  full value reachable ONLY via a mouse-hover title tooltip - the one place in
                  this tool meant for actually reading a node's real content shouldn't require
                  hovering to do that. Long values (e.g. a Fact/Entry's own real text) now wrap
                  and span the full row instead of being squeezed into a narrow grid cell. */}
              <div className="grid grid-cols-2 lg:grid-cols-3 gap-2">
                {Object.entries(selectedNode.props ?? {}).map(([k, v]) => {
                  const text = String(v)
                  const isLong = text.length > 60
                  return (
                    <div
                      key={k}
                      className={`bg-slate-800/50 rounded-lg px-3 py-2 ${isLong ? 'col-span-2 lg:col-span-3' : ''}`}
                    >
                      <span className="text-[10px] text-slate-500 uppercase block">
                        {k}
                      </span>
                      <p className="text-sm text-white font-mono whitespace-normal break-words">
                        {text}
                      </p>
                    </div>
                  )
                })}
              </div>
            </div>

            {/* kata cycle 55's own real fix: a node over HIGH_DEGREE_THRESHOLD (node 33175, found
                live - 1,571 real edges, a 1.4MB response, both the graph and the Edges panel
                broke trying to render it) shows its real per-label breakdown instead of
                attempting the full, unbounded fetch - the user picks ONE label to actually
                explore, using the same cheap edges?label=X endpoint the Book Map already proved
                out (kata cycle 54). */}
            {needsLabelChoice ? (
              <div className="flex-1 min-h-0 bg-slate-900 border border-slate-800 rounded-xl p-5 overflow-auto">
                <p className="text-sm text-slate-300 mb-1">
                  {edgeCount} real edges - too many to render at once.
                </p>
                <p className="text-xs text-slate-500 mb-4">
                  Pick one label to explore its own real edges:
                </p>
                <div className="flex flex-wrap gap-2">
                  {Object.entries(edgeCounts)
                    .sort((a, b) => b[1] - a[1])
                    .map(([label, count]) => (
                      <button
                        key={label}
                        onClick={() => selectedNode && loadFilteredByLabel(selectedNode, label)}
                        className="px-3 py-1.5 text-sm rounded-md bg-slate-800 text-slate-200 hover:bg-slate-700 border border-slate-700"
                      >
                        {label} <span className="text-slate-500">({count})</span>
                      </button>
                    ))}
                </div>
              </div>
            ) : (
            /* Graph + enumerated edge list - kata cycle 46's own real fix: the graph alone is a
                poor way to actually READ every edge on a high-degree node (a real 310-edge book
                is the exact case that motivated this) - a real, scrollable, textual list lets a
                user see every one, not just what fits visually in the ring. Both share the same
                real ordering (sortEdges.ts: chunk_index when present, else node id), so the
                graph ring and this list always agree. */
            <div className="flex-1 min-h-0 flex gap-4">
              <div className="flex-1 min-w-0">
                <GraphViewer
                  nodes={graphNodes}
                  edges={graphEdges}
                  centerId={selectedNode?.id}
                  onNodeClick={exploreById}
                />
              </div>
              <div className="w-72 flex-shrink-0 flex flex-col min-h-0">
                <div className="flex items-center justify-between mb-2 flex-shrink-0">
                  <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wider">
                    Edges ({sortedEdges.length})
                  </h3>
                  <select
                    value={sortMode}
                    onChange={(e) => setSortMode(e.target.value as SortMode)}
                    className="bg-slate-800 border border-slate-700 rounded text-[10px] text-slate-300 px-1.5 py-0.5 focus:outline-none focus:border-blue-500/50"
                    title="Sort edges by"
                  >
                    <option value="id">ID order</option>
                    <option value="label">Lexicographical</option>
                    <option value="connections">Connections</option>
                  </select>
                </div>
                <div className="flex-1 overflow-auto bg-slate-900 border border-slate-800 rounded-xl divide-y divide-slate-800/50">
                  {sortedEdges.length === 0 ? (
                    <div className="flex items-center justify-center h-24 text-slate-600 text-xs">
                      No edges
                    </div>
                  ) : (
                    sortedEdges.map(({ edge, otherNode, direction }) => (
                      <div
                        key={edge.id}
                        onClick={() => exploreById(otherNode.id)}
                        className="px-3 py-2 cursor-pointer hover:bg-slate-800/50 transition-colors"
                      >
                        <div className="flex items-center gap-1.5 text-[10px] text-slate-500 uppercase tracking-wide">
                          {direction === 'out' ? (
                            <ArrowRight className="w-3 h-3 flex-shrink-0" />
                          ) : (
                            <ArrowLeft className="w-3 h-3 flex-shrink-0" />
                          )}
                          <span className="truncate">{edge.label}</span>
                        </div>
                        <p className="text-sm text-white truncate">{nodeDisplayLabel(otherNode)}</p>
                        <p className="text-[10px] text-slate-500 font-mono">
                          ID: {otherNode.id}
                          {sortMode === 'connections' && (
                            <span className="ml-2">
                              · {degrees[String(otherNode.id)] ?? 0} connection
                              {(degrees[String(otherNode.id)] ?? 0) === 1 ? '' : 's'}
                            </span>
                          )}
                        </p>
                      </div>
                    ))
                  )}
                </div>
              </div>
            </div>
            )}
          </>
        ) : (
          <div className="flex-1 flex items-center justify-center text-slate-600">
            <div className="text-center">
              <p className="text-sm">Select a node to explore</p>
              <p className="text-xs mt-1 text-slate-700">
                Click a node to see its properties and connections
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
