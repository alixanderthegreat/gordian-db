import { useRef, useEffect, useCallback, useState } from 'react'
import cytoscape from 'cytoscape'
import type { GraphVizNode, GraphVizEdge } from '../types'
import { nodeDisplayLabel } from '../lib/label'

interface Props {
  nodes: GraphVizNode[]
  edges: GraphVizEdge[]
  centerId?: number
  onNodeClick?: (nodeId: number) => void
}

// GRAPH_LABEL_LEN is deliberately much shorter than nodeDisplayLabel's own 100-char cap (kata
// cycle 41) - that cap was tuned for the list/detail views, which have real room and no
// competing labels nearby. On the canvas, every node's label competes for the SAME space as its
// neighbors', and a real high-degree hub (found live: node 4589, 310 real edges) turns long
// labels into an unreadable, overlapping mess. The FULL label is still one hover away (see the
// tooltip below), so nothing is actually lost - just not shown by default.
const GRAPH_LABEL_LEN = 22

function graphLabel(n: GraphVizNode): string {
  const full = nodeDisplayLabel(n)
  return full.length > GRAPH_LABEL_LEN ? full.slice(0, GRAPH_LABEL_LEN) + '…' : full
}

// Real, distinct color per real node type seen in production (kata cycle 45's own first ask:
// "color differentiation would be nice to show that this is a book and this is a book chunk").
// UNKNOWN_COLOR is the real fallback for any label not in this list, not a silent crash.
const TYPE_COLORS: Record<string, string> = {
  Book: '#f59e0b',
  BookChunk: '#fbbf24',
  Entity: '#a78bfa',
  Fact: '#34d399',
  Entry: '#2dd4bf',
  Note: '#f472b6',
  Reaction: '#fb923c',
  Resource: '#22d3ee',
  ResourceChunk: '#67e8f9',
}
const UNKNOWN_COLOR = '#3b82f6'

function colorFor(nodeType: string): string {
  return TYPE_COLORS[nodeType] ?? UNKNOWN_COLOR
}

// sizeFor scales a node by its own real degree (edge count) - kata cycle 45's own real addition,
// beyond what was explicitly asked: a 310-edge hub should visually read as a hub at a glance, not
// render as just another same-sized circle. Only the center node's real total degree is known
// here (edges.length from the neighborhood API) - a neighbor's own degree beyond this one-hop
// view isn't returned by the API, so neighbors get a smaller, fixed size instead of a fabricated
// number.
function sizeFor(degree: number): number {
  const size = 24 + Math.sqrt(degree) * 4
  return Math.min(80, Math.max(24, size))
}

export default function GraphViewer({ nodes, edges, centerId, onNodeClick }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const cyRef = useRef<cytoscape.Core | null>(null)
  const [tooltip, setTooltip] = useState<{ x: number; y: number; text: string } | null>(null)

  const buildElements = useCallback((): cytoscape.ElementDefinition[] => {
    const els: cytoscape.ElementDefinition[] = []
    const degree = centerId !== undefined ? edges.length : 0
    for (const n of nodes) {
      const isCenter = n.id === centerId
      // Real ordering (kata cycle 46), matching the user's own real request ("id order is
      // helpful too", grounded in a real 310-chunk book's own natural chunk_index sequence):
      // chunk_index when present, else the node's own id - the same rule sortEdges.ts uses for
      // the enumerated list, so the graph ring and the textual list always agree.
      const chunkIndex = n.props?.chunk_index
      const sortKey = typeof chunkIndex === 'number' ? chunkIndex : n.id
      els.push({
        data: {
          id: String(n.id),
          label: graphLabel(n),
          fullLabel: nodeDisplayLabel(n),
          nodeType: n.label,
          isCenter,
          size: isCenter ? sizeFor(degree) : sizeFor(1),
          sortKey,
        },
      })
    }
    for (const e of edges) {
      els.push({
        data: {
          id: `e${e.id}`,
          source: String(e.from),
          target: String(e.to),
          label: e.label,
        },
      })
    }
    return els
  }, [nodes, edges, centerId])

  useEffect(() => {
    if (!containerRef.current) return

    const elements = buildElements()

    if (cyRef.current) {
      cyRef.current.destroy()
    }

    if (elements.length === 0) {
      cyRef.current = null
      return
    }

    // Real fix (kata cycle 45's own second ask, "circle group around the node"): concentric
    // layout, keyed so the center node gets the highest concentric value (cytoscape places the
    // largest value in the middle) - every neighbor rings around it. Replaces the old cose/grid
    // split entirely: every real call into this component always has exactly one real center
    // (ExplorerPage's own neighborhood view), so a layout built around that shape is the right
    // default regardless of size, not just a fallback for huge neighborhoods.
    const cy = cytoscape({
      container: containerRef.current,
      elements,
      layout: {
        name: 'concentric',
        concentric: (node: cytoscape.NodeSingular) => (node.data('isCenter') ? 2 : 1),
        levelWidth: () => 1,
        // Real ordering within the ring (kata cycle 46) - without an explicit sort, cytoscape's
        // own within-level node placement isn't a guarantee worth relying on. sortKey (built
        // above) is chunk_index when present, else the node's own id.
        sort: (a: cytoscape.NodeSingular, b: cytoscape.NodeSingular) => a.data('sortKey') - b.data('sortKey'),
        minNodeSpacing: 45,
        animate: true,
        animationDuration: 400,
      } as any,
      style: [
        {
          selector: 'node',
          style: {
            'background-color': (ele: any) => colorFor(ele.data('nodeType')),
            label: 'data(label)',
            color: '#e2e8f0',
            'font-size': '11px',
            'text-valign': 'bottom',
            'text-margin-y': 8,
            width: 'data(size)',
            height: 'data(size)',
            'border-width': 2,
            'border-color': '#1e293b',
          },
        },
        {
          selector: 'node[?isCenter]',
          style: {
            'border-width': 3,
            'border-color': '#f8fafc',
          },
        },
        {
          selector: 'edge',
          style: {
            width: 2,
            'line-color': '#475569',
            'target-arrow-color': '#64748b',
            'target-arrow-shape': 'triangle',
            'curve-style': 'bezier',
            label: 'data(label)',
            'font-size': '9px',
            color: '#94a3b8',
            'text-rotation': 'autorotate',
            'text-margin-y': -10,
          } as any,
        },
        {
          selector: 'node:selected',
          style: {
            'border-color': '#f8fafc',
            'border-width': 3,
          },
        },
        {
          selector: 'node:active',
          style: {
            'overlay-opacity': 0.1,
          },
        },
      ],
    })

    cy.on('tap', 'node', (evt) => {
      const id = parseInt(evt.target.id(), 10)
      if (onNodeClick && !isNaN(id)) onNodeClick(id)
    })

    // Real, direct answer to "the text overlaps" beyond just shortening it: hovering any node
    // shows its own full, real label (the same one kata cycle 41 already made readable in the
    // detail panel) in a small floating tooltip - nothing is lost by shortening the on-canvas
    // label, it's one hover away.
    cy.on('mouseover', 'node', (evt) => {
      const pos = evt.target.renderedPosition()
      setTooltip({ x: pos.x, y: pos.y, text: evt.target.data('fullLabel') })
    })
    cy.on('mouseout', 'node', () => setTooltip(null))
    cy.on('pan zoom', () => setTooltip(null))

    cyRef.current = cy

    return () => {
      cy.destroy()
      cyRef.current = null
    }
  }, [buildElements, onNodeClick])

  if (nodes.length === 0) {
    return (
      <div className="w-full h-full min-h-[400px] bg-slate-950 rounded-lg border border-slate-800 flex items-center justify-center text-slate-600 text-sm">
        No graph data to display
      </div>
    )
  }

  return (
    <div className="relative w-full h-full min-h-[400px]">
      <div
        ref={containerRef}
        className="w-full h-full min-h-[400px] bg-slate-950 rounded-lg border border-slate-800"
      />
      {tooltip && (
        <div
          className="absolute z-10 pointer-events-none bg-slate-800 border border-slate-700 rounded-md px-2 py-1 text-xs text-white max-w-xs break-words shadow-lg"
          style={{ left: tooltip.x + 12, top: tooltip.y + 12 }}
        >
          {tooltip.text}
        </div>
      )}
    </div>
  )
}
