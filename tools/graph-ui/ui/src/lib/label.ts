// nodeDisplayLabel is the one, shared, real-content-aware display label for a node - used by both
// GraphViewer.tsx (the cytoscape graph, where this label is ALL that's shown until a node is
// clicked) and ExplorerPage.tsx's own list rows. Found necessary live (kata cycle 39): the graph
// view previously showed only the node's gordian-db TYPE ("Fact", "Entity", "Book"), making every
// node of the same type indistinguishable at a glance - real content (name/title/text) is what
// actually identifies a node to a human looking at the graph.
//
// MAX_LABEL_LEN is 100, not 40 (kata cycle 41's own real fix) - 40 was found live to clip even
// short, real Fact sentences (39-59 chars) mid-word. Still a real cap, not unbounded: cytoscape's
// own graph-node labels have no CSS-driven overflow handling the way a DOM element would, so
// unbounded text would visually overlap neighboring nodes/edges in the graph view specifically.
// The node DETAIL PANEL (ExplorerPage's own prop-value cards), where a user actually wants to
// read full content, does NOT use this truncation at all - see ExplorerPage.tsx's own real fix.
const MAX_LABEL_LEN = 100

export function nodeDisplayLabel(node: { id: number; label: string; props?: Record<string, any> }): string {
  const p = node.props ?? {}
  const candidates = [p.name, p.title, p.label, p.text]
  for (const c of candidates) {
    if (typeof c === 'string' && c.trim() !== '') {
      return c.length > MAX_LABEL_LEN ? c.slice(0, MAX_LABEL_LEN) + '…' : c
    }
  }
  return `${node.label} #${node.id}`
}
