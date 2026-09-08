// Trimmed for gordian-db's own small graph-ui (kata cycle 36) - only the shapes ExplorerPage.tsx/
// GraphViewer.tsx actually use. See ../../server.go for the real Go-side struct definitions these
// mirror.

export interface GNode {
  id: number
  props: Record<string, any>
}

export interface GraphVizNode {
  id: number
  props: Record<string, any>
  label: string
}

export interface GraphVizEdge {
  id: number
  from: number
  to: number
  label: string
}

export interface NeighborhoodResponse {
  center: GraphVizNode
  neighbors: GraphVizNode[]
  edges: GraphVizEdge[]
}

export interface NodeCursorPage {
  nodes: CursorNode[]
  next_cursor: number
  has_more: boolean
  limit: number
}

export interface CursorNode {
  id: number
  labels?: string[]
  props: Record<string, any>
}
