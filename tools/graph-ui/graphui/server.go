// Package graphui is kata cycle 36's own real answer to "fitting the graph-ui under tools/" - a
// small, purpose-built HTTP backend for gordian-db's own data, deliberately NOT a port of
// goraphdb's own graphdb-ui (assets/goraphdb/cmd/graphdb-ui, 1,478-line server.go): that tool
// exposes Cypher query execution, Raft/cluster management, and generic named indexes/constraints -
// none of which gordian-db has or wants (no query language, deliberately single-node, typed
// structural indexes only). This backend covers exactly what the real ExplorerPage.tsx needs:
// browse nodes, inspect a node's neighborhood (every edge, every label, both directions - the real
// gap ListNodes/OutEdges/InEdges were built to fill), create an edge, delete a node.
//
// Promoted from tools/graph-ui's own package main to a real importable package in kata cycle 52 -
// the real point being that a caller who ALREADY has a *gordian.Graph open (simple-bot's own live
// process, most concretely) can serve the exact same UI/API directly against it, as a goroutine in
// its own process, rather than needing a second OS process and gordian.Open's own exclusive lock
// contention with the first. tools/graph-ui's own main.go remains a thin CLI wrapper for the
// standalone case (inspecting a store while nothing else has it open).
package graphui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	gordian "github.com/alixanderthegreat/gordian-db"
)

type apiServer struct {
	g *gordian.Graph
}

// vizNode mirrors the real frontend's GraphVizNode type (types.ts) exactly.
type vizNode struct {
	ID    int64          `json:"id"`
	Props map[string]any `json:"props"`
	Label string         `json:"label"`
}

// vizEdge mirrors GraphVizEdge. ID is a synthesized, within-response-only index - gordian-db's
// own AddEdge has no real edge id (unlike goraphdb's GEdge), a deliberate simplification recorded
// in this cycle's own obstacle, not a hidden gap.
type vizEdge struct {
	ID    int64  `json:"id"`
	From  int64  `json:"from"`
	To    int64  `json:"to"`
	Label string `json:"label"`
}

type neighborhoodResponse struct {
	Center    vizNode   `json:"center"`
	Neighbors []vizNode `json:"neighbors"`
	Edges     []vizEdge `json:"edges"`
}

// cursorNode mirrors CursorNode - Labels is a slice (goraphdb supports multi-label nodes) even
// though gordian-db's own Node.Label is always exactly one; wrapping it in a one-element slice
// costs nothing and keeps the real frontend's existing type happy unmodified.
type cursorNode struct {
	ID     int64          `json:"id"`
	Labels []string       `json:"labels,omitempty"`
	Props  map[string]any `json:"props"`
}

type nodeCursorPage struct {
	Nodes      []cursorNode `json:"nodes"`
	NextCursor int64        `json:"next_cursor"`
	HasMore    bool         `json:"has_more"`
	Limit      int          `json:"limit"`
}

// gnode mirrors the real frontend's GNode type - deliberately no label field (GNode itself
// doesn't carry one; only GraphVizNode does), matching client.ts's own getNode contract exactly.
type gnode struct {
	ID    int64          `json:"id"`
	Props map[string]any `json:"props"`
}

func toVizNode(n gordian.Node) vizNode {
	return vizNode{ID: n.ID, Props: n.Props, Label: n.Label}
}

// truncateProps caps every string prop value at max runes, leaving every other type and every prop
// KEY untouched - kata cycle 58's own real payload fix. Rune-based, not byte-based, so a cap never
// splits a multi-byte character into invalid UTF-8 (this corpus contains real non-ASCII text).
// Returns a new map rather than mutating the node's own props, since the caller's map came
// straight from a real decoded Node and must not be corrupted for anything else holding it.
func truncateProps(props map[string]any, max int) map[string]any {
	if props == nil {
		return nil
	}
	out := make(map[string]any, len(props))
	for k, v := range props {
		s, ok := v.(string)
		if !ok {
			out[k] = v
			continue
		}
		r := []rune(s)
		if len(r) > max {
			out[k] = string(r[:max]) + "…"
			continue
		}
		out[k] = s
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// pathID extracts the {id} path segment already matched by Go 1.22+'s own mux wildcard syntax
// (net/http's ServeMux, no external router needed for a surface this small).
func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func (s *apiServer) handleListNodesCursor(w http.ResponseWriter, r *http.Request) {
	cursor := int64(-1)
	if c := r.URL.Query().Get("cursor"); c != "" {
		v, err := strconv.ParseInt(c, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid cursor: %w", err))
			return
		}
		cursor = v
	}
	limit := 30
	if l := r.URL.Query().Get("limit"); l != "" {
		v, err := strconv.Atoi(l)
		if err != nil || v <= 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid limit"))
			return
		}
		limit = v
	}

	nodes, next, hasMore, err := s.g.ListNodes(cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	page := nodeCursorPage{NextCursor: next, HasMore: hasMore, Limit: limit}
	for _, n := range nodes {
		page.Nodes = append(page.Nodes, cursorNode{ID: n.ID, Labels: []string{n.Label}, Props: n.Props})
	}
	if page.Nodes == nil {
		page.Nodes = []cursorNode{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *apiServer) handleGetNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	n, ok, err := s.g.GetNode(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("node %d not found", id))
		return
	}
	writeJSON(w, http.StatusOK, gnode{ID: n.ID, Props: n.Props})
}

// handleNeighborhood is the real point of this whole tool: every edge, every label, both
// directions, resolved via OutEdges/InEdges (kata cycle 36's own new label-agnostic primitives) -
// the exact real gap Neighbors/InNeighbors (label-required) could never answer for a genuine
// graph explorer, which by definition doesn't know in advance which labels matter.
func (s *apiServer) handleNeighborhood(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	center, ok, err := s.g.GetNode(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("node %d not found", id))
		return
	}

	out, err := s.g.OutEdges(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	in, err := s.g.InEdges(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Real, defensive fix (kata cycle 43): an edge is only ever included if its OTHER endpoint
	// actually resolves - found live, this endpoint used to append every OutEdges/InEdges result
	// unconditionally, even when the neighbor's own GetNode failed (silently dropped from
	// Neighbors, but the dangling Edges entry was still sent) - the frontend then tried to build
	// a cytoscape edge referencing a node id that was never in its own node set and crashed. This
	// protects against ANY dangling edge, not just ones DeleteNode's own new cleanup (graph.go)
	// now prevents going forward - a store predating that fix, or any other future edge case,
	// can never crash this endpoint again.
	resp := neighborhoodResponse{Center: toVizNode(center), Neighbors: []vizNode{}, Edges: []vizEdge{}}
	resolved := map[int64]gordian.Node{id: center}
	resolve := func(otherID int64) (gordian.Node, bool) {
		if n, ok := resolved[otherID]; ok {
			return n, true
		}
		n, ok, err := s.g.GetNode(otherID)
		if err != nil || !ok {
			return gordian.Node{}, false
		}
		resolved[otherID] = n
		return n, true
	}
	seen := map[int64]bool{}
	var edgeID int64
	for _, e := range out {
		if n, ok := resolve(e.To); ok {
			resp.Edges = append(resp.Edges, vizEdge{ID: edgeID, From: e.From, To: e.To, Label: e.Label})
			edgeID++
			if e.To != id && !seen[e.To] {
				resp.Neighbors = append(resp.Neighbors, toVizNode(n))
				seen[e.To] = true
			}
		}
	}
	for _, e := range in {
		if n, ok := resolve(e.From); ok {
			resp.Edges = append(resp.Edges, vizEdge{ID: edgeID, From: e.From, To: e.To, Label: e.Label})
			edgeID++
			if e.From != id && !seen[e.From] {
				resp.Neighbors = append(resp.Neighbors, toVizNode(n))
				seen[e.From] = true
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *apiServer) handleCreateEdge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From  int64  `json:"from"`
		To    int64  `json:"to"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Label == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("label is required"))
		return
	}
	if err := s.g.AddEdge(body.From, body.To, body.Label); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// No real edge id exists in gordian-db - 0 is a documented placeholder, not a real reference.
	writeJSON(w, http.StatusOK, map[string]int64{"id": 0})
}

// handleDeleteNode deletes the raw node and its automatic label-index entry (gordian-db's own
// DeleteNode, kata cycle 32) - it does NOT know about or clean up any property/range/vector
// secondary index entries this node might carry, the same "caller must know what was indexed"
// limitation DeleteNode's own doc comment already establishes. A real, honest limitation of an
// admin/debug tool operating on arbitrary nodes without knowing their own indexing history - not
// silently pretended away.
func (s *apiServer) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.g.DeleteNode(id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleStats reports a real, correct node_count via a full ListNodes pagination - honest, not
// approximated, since this is a manually-invoked diagnostic endpoint, not a hot path (unlike
// AllNodes, which kata cycle 32 fixed specifically because it WAS hot). edge_count is not
// computed (no cheap way to do so without a real per-node OutEdges call for every node) - reported
// as -1, a documented "not computed" sentinel, not a fabricated number. shard_count/
// disk_size_bytes from the real frontend's own GraphStats type are omitted entirely - gordian-db
// has no sharding, and a real disk-size figure would need filesystem access this tool doesn't
// have reason to take on for a debug/introspection surface.
// handleSearch is kata cycle 40's own real answer to "I have no way to search id or name" - found
// live, the instant the user actually tried to find a specific node among 11,101 real ones with
// only 30-per-page cursor pagination available. A query that parses as an int64 is treated as an
// exact id lookup (fast, single GetNode call); otherwise it's a real, case-insensitive substring
// scan across props.name/title/text (the same fields nodeDisplayLabel, kata cycle 39, already
// prioritizes) using ListNodes to enumerate every node regardless of label. Early-exits once limit
// matches are found - deliberately DIFFERENT from FindQuote's own no-early-exit design elsewhere
// in this project (that existed to avoid biasing a RANDOM SAMPLE toward early-ingested content;
// this is a direct search, where "first N matches, refine your query for more" is the expected,
// correct UX, not a bias risk).
// handleSearch's own cursor support is kata cycle 42's real fix: cycle 40's original design
// capped results at `limit` with no way to see more, on the assumption a caller could always
// "refine the query" for a narrower result - false for a genuinely common real term (486 real
// matches for "wife" in the actual corpus; the specific node a real search was FOR, the Entity
// "wife" itself, was past the cap with no way to reach it). Now returns the exact same
// NodeCursorPage contract /api/nodes/cursor uses (next_cursor/has_more), tracking the real
// underlying ListNodes scan position - not a match count - so a second call with that cursor
// resumes the scan exactly where the first one stopped, including mid-batch.
func (s *apiServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 30
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	cursor := int64(-1)
	if c := r.URL.Query().Get("cursor"); c != "" {
		if v, err := strconv.ParseInt(c, 10, 64); err == nil {
			cursor = v
		}
	}
	if q == "" {
		writeJSON(w, http.StatusOK, nodeCursorPage{Nodes: []cursorNode{}, Limit: limit})
		return
	}

	if id, err := strconv.ParseInt(q, 10, 64); err == nil {
		// An exact id match has exactly one real result - paginating it makes no sense, so any
		// cursor beyond the first page (-1) is treated as "nothing more."
		if cursor != -1 {
			writeJSON(w, http.StatusOK, nodeCursorPage{Nodes: []cursorNode{}, Limit: limit})
			return
		}
		n, ok, gerr := s.g.GetNode(id)
		if gerr != nil {
			writeError(w, http.StatusInternalServerError, gerr)
			return
		}
		if !ok {
			writeJSON(w, http.StatusOK, nodeCursorPage{Nodes: []cursorNode{}, Limit: limit})
			return
		}
		writeJSON(w, http.StatusOK, nodeCursorPage{
			Nodes:      []cursorNode{{ID: n.ID, Labels: []string{n.Label}, Props: n.Props}},
			NextCursor: n.ID,
			Limit:      limit,
		})
		return
	}

	needle := strings.ToLower(q)
	matches := []cursorNode{}
	scanCursor := cursor
	hasMore := false
scan:
	for {
		nodes, next, more, err := s.g.ListNodes(scanCursor, 500)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		for _, n := range nodes {
			scanCursor = n.ID
			if nodeMatchesSearch(n, needle) {
				matches = append(matches, cursorNode{ID: n.ID, Labels: []string{n.Label}, Props: n.Props})
				if len(matches) >= limit {
					// Stopped before exhausting the scan - there is real, unscanned store left
					// (this batch's own remainder, at minimum), so more is a safe, correct default
					// even in the rare case those specific remaining nodes hold no further match.
					hasMore = true
					break scan
				}
			}
		}
		if !more {
			hasMore = false
			break
		}
		scanCursor = next
	}
	writeJSON(w, http.StatusOK, nodeCursorPage{Nodes: matches, NextCursor: scanCursor, HasMore: hasMore, Limit: limit})
}

func nodeMatchesSearch(n gordian.Node, needle string) bool {
	for _, key := range []string{"name", "title", "text"} {
		if s, ok := n.Props[key].(string); ok && strings.Contains(strings.ToLower(s), needle) {
			return true
		}
	}
	return false
}

func (s *apiServer) handleStats(w http.ResponseWriter, r *http.Request) {
	var count int64
	cursor := int64(-1)
	for {
		nodes, next, hasMore, err := s.g.ListNodes(cursor, 1000)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		count += int64(len(nodes))
		if !hasMore {
			break
		}
		cursor = next
	}
	writeJSON(w, http.StatusOK, map[string]int64{"node_count": count, "edge_count": -1})
}

// handleNodeDegrees is kata cycle 47's own real, small, reusable answer to "sort by connections":
// rather than a one-off field bolted onto the neighborhood response, this computes real total
// degree (OutEdges+InEdges) for a GIVEN, caller-chosen set of ids - generically useful for any
// future view that wants degree info, not narrowly scoped to the Edges panel's own sort control.
// A malformed or missing id in the list is silently skipped (matching the existing endpoints' own
// permissive-on-bad-input convention, e.g. handleSearch's own limit parsing) rather than failing
// the whole batch over one bad entry.
func (s *apiServer) handleNodeDegrees(w http.ResponseWriter, r *http.Request) {
	idsParam := strings.TrimSpace(r.URL.Query().Get("ids"))
	degrees := map[int64]int64{}
	if idsParam == "" {
		writeJSON(w, http.StatusOK, map[string]any{"degrees": degrees})
		return
	}
	for _, raw := range strings.Split(idsParam, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			continue
		}
		degrees[id] = 0
	}
	for id := range degrees {
		out, err := s.g.OutEdges(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		in, err := s.g.InEdges(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		degrees[id] = int64(len(out) + len(in))
	}
	writeJSON(w, http.StatusOK, map[string]any{"degrees": degrees})
}

// handleEdgeCounts is kata cycle 57's own real GLOBAL counterpart to handleNodeEdgeCounts: how
// many real edges exist per label across the WHOLE graph, aggregate-only. This is what a
// whole-graph map view fetches FIRST, before any payload - the same "price the work before doing
// it" discipline cycle 55 established for one high-degree node, applied to the entire store.
func (s *apiServer) handleEdgeCounts(w http.ResponseWriter, r *http.Request) {
	counts, err := s.g.EdgeCountsByLabel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": counts})
}

// handleEdgesByLabel is kata cycle 57's own real GLOBAL edge query: every real edge carrying one
// label, from anywhere in the graph, capped by a real limit that reports truncation honestly
// rather than handing back a partial answer that looks whole. The label determines its own
// endpoints, which is exactly why an EDGE label (not a node label) is the right control for a map
// view over a heterogeneous graph.
func (s *apiServer) handleEdgesByLabel(w http.ResponseWriter, r *http.Request) {
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	if label == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("label is required"))
		return
	}
	limit := 5000
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	found, truncated, err := s.g.EdgesByLabel(label, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	edges := make([]vizEdge, len(found))
	for i, e := range found {
		edges[i] = vizEdge{ID: int64(i), From: e.From, To: e.To, Label: e.Label}
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": edges, "truncated": truncated})
}

// handleNodeEdgeCounts is kata cycle 55's own real, GENERIC primitive: a node's own real edge
// count, GROUPED BY LABEL, without resolving a single neighbor node - found necessary live (node
// 33175, a real still-ingesting book, hit 1,571 real edges and a 1.4MB neighborhood response that
// broke both the Edges panel and the graph view). This is deliberately cheaper than
// handleNodeEdgesByLabel (kata cycle 54): that one still returns full edge data for ONE chosen
// label; this one returns only real integer counts for EVERY label a node touches, so a caller
// can decide whether fetching anything more is even a good idea BEFORE paying for it.
func (s *apiServer) handleNodeEdgeCounts(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.g.OutEdges(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	in, err := s.g.InEdges(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	counts := map[string]int{}
	for _, e := range out {
		counts[e.Label]++
	}
	for _, e := range in {
		counts[e.Label]++
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": counts})
}

// handleNodesBatch is kata cycle 55's own real, GENERIC primitive: resolve a caller-chosen,
// bounded set of real node ids in one call - needed so drilling into just ONE label's worth of a
// high-degree node's own edges (via handleNodeEdgesByLabel) doesn't turn into a real N+1 pattern,
// one GetNode call per neighbor, from the frontend. Returns vizNode (carries Label, unlike gnode)
// since the real caller here is rendering a graph, which needs each node's own type to color it.
// An id that doesn't resolve (deleted, made up) is silently skipped, not a hard failure of the
// whole batch - matches this file's own existing permissive-on-bad-input convention (e.g.
// handleNodeDegrees).
//
// ?maxprop=N (kata cycle 58) caps every string prop value at N characters. Measured need, not a
// hypothetical: a real map view resolving 2,601 real nodes pulled 3.88 MB, almost entirely
// `description` free text on JobListing nodes (up to 14.9 KB EACH) that the map never draws - it
// draws one short label per node. The same nodes at display size are 198 KB, twenty times
// smaller. Deliberately generic - this caps by LENGTH and keeps every prop KEY, so the server
// never learns what a JobListing is and the frontend's own display-label priority keeps working
// untouched. Omitted means full props, unchanged, which the Explorer's detail view genuinely needs.
func (s *apiServer) handleNodesBatch(w http.ResponseWriter, r *http.Request) {
	idsParam := strings.TrimSpace(r.URL.Query().Get("ids"))
	maxProp := 0
	if m := r.URL.Query().Get("maxprop"); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v > 0 {
			maxProp = v
		}
	}
	nodes := []vizNode{}
	if idsParam == "" {
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
		return
	}
	for _, raw := range strings.Split(idsParam, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			continue
		}
		n, ok, err := s.g.GetNode(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			continue
		}
		v := toVizNode(n)
		if maxProp > 0 {
			v.Props = truncateProps(v.Props, maxProp)
		}
		nodes = append(nodes, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// handleNodesByLabel is kata cycle 54's own real, GENERIC primitive (not book-specific despite
// the real need that motivated it) - "give me every node of one label" via the real, already-fast
// AllNodes(label) (label-indexed since kata cycle 32), for any label small enough to return in one
// response (a real caller's own judgment call - e.g. Book, not BookChunk). No cursor pagination:
// unlike /api/nodes/cursor's own label-agnostic scan, a caller who already knows they want exactly
// one, typically-small label doesn't need one.
func (s *apiServer) handleNodesByLabel(w http.ResponseWriter, r *http.Request) {
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	if label == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("label is required"))
		return
	}
	nodes, err := s.g.AllNodes(label)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]cursorNode, len(nodes))
	for i, n := range nodes {
		out[i] = cursorNode{ID: n.ID, Labels: []string{n.Label}, Props: n.Props}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": out})
}

// handleNodeEdgesByLabel is kata cycle 54's own real, GENERIC primitive: a node's own real edges
// (both directions), filtered server-side to ONE requested label - found necessary because the
// existing, fully generic handleNeighborhood resolves EVERY edge a node has, which is exactly
// wrong for a node like a Book with thousands of real PART_OF-linked chunks when a caller only
// wants its (typically far sparser) RELATED_TO edges. Filtering happens after the real
// OutEdges/InEdges scan (a cheap, unresolved key scan - no per-edge Node lookup) but before any
// JSON is built, so a node with thousands of edges in OTHER labels never gets serialized over the
// wire just to be discarded client-side.
func (s *apiServer) handleNodeEdgesByLabel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	if label == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("label is required"))
		return
	}
	out, err := s.g.OutEdges(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	in, err := s.g.InEdges(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	edges := []vizEdge{}
	var edgeID int64
	for _, e := range out {
		if e.Label == label {
			edges = append(edges, vizEdge{ID: edgeID, From: e.From, To: e.To, Label: e.Label})
			edgeID++
		}
	}
	for _, e := range in {
		if e.Label == label {
			edges = append(edges, vizEdge{ID: edgeID, From: e.From, To: e.To, Label: e.Label})
			edgeID++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": edges})
}

// NewMux builds the real graph-ui HTTP handler - every /api/ route, plus (if uiFS is non-nil) the
// built frontend with SPA-router fallback. uiFS works over any real fs.FS - the package's own
// EmbeddedUI() (kata cycle 52's own real point: no loose directory to keep in sync), or
// os.DirFS(dir) for local frontend development against fresh, unbuilt files. nil means API-only,
// matching the tool's own original empty-uiDir behavior.
func NewMux(g *gordian.Graph, uiFS fs.FS) *http.ServeMux {
	s := &apiServer{g: g}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/nodes/cursor", s.handleListNodesCursor)
	mux.HandleFunc("GET /api/nodes/search", s.handleSearch)
	mux.HandleFunc("GET /api/nodes/degrees", s.handleNodeDegrees)
	mux.HandleFunc("GET /api/nodes/by-label", s.handleNodesByLabel)
	mux.HandleFunc("GET /api/nodes/batch", s.handleNodesBatch)
	mux.HandleFunc("GET /api/nodes/{id}", s.handleGetNode)
	mux.HandleFunc("GET /api/nodes/{id}/neighborhood", s.handleNeighborhood)
	mux.HandleFunc("GET /api/nodes/{id}/edges", s.handleNodeEdgesByLabel)
	mux.HandleFunc("GET /api/nodes/{id}/edge-counts", s.handleNodeEdgeCounts)
	mux.HandleFunc("DELETE /api/nodes/{id}", s.handleDeleteNode)
	mux.HandleFunc("GET /api/edges/counts", s.handleEdgeCounts)
	mux.HandleFunc("GET /api/edges", s.handleEdgesByLabel)
	mux.HandleFunc("POST /api/edges", s.handleCreateEdge)
	mux.HandleFunc("GET /api/stats", s.handleStats)

	if uiFS != nil {
		mux.Handle("/", spaHandler(uiFS))
	}
	return mux
}

// spaHandler serves the built frontend's static files over a real fs.FS, falling back to
// index.html for any path that isn't a real file - the standard SPA-router pattern (react-router's
// own client-side routes like /explorer aren't real files, only index.html + client-side JS
// routing is). Works identically whether uiFS is an embedded build or a real disk directory -
// kata cycle 52's own real point in moving off a raw uiDir string.
func spaHandler(uiFS fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(uiFS))
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if f, err := uiFS.Open(clean); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		data, err := fs.ReadFile(uiFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	}
}
