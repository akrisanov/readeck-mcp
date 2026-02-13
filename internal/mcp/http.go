package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/akrisanov/readeck-mcp/internal/readeck"
	"github.com/akrisanov/readeck-mcp/internal/render"
)

const maxHTTPBodySize = 1 << 20

func (s *Server) RunHTTP(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(s.cfg.HTTPPath, s.handleHTTPMCP)

	httpServer := &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		err := httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		<-errCh
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleHTTPMCP(w http.ResponseWriter, r *http.Request) {
	if !s.isHTTPAuthorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !s.isOriginAllowed(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodPost:
		s.handleHTTPPost(w, r)
	case http.MethodGet, http.MethodDelete:
		w.WriteHeader(http.StatusMethodNotAllowed)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHTTPPost(w http.ResponseWriter, r *http.Request) {
	if !acceptsRPCResponse(r.Header.Get("Accept")) {
		http.Error(w, "not acceptable", http.StatusNotAcceptable)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBodySize))
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		writeHTTPRPCResponse(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	if strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
		writeHTTPRPCResponse(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32600, Message: "batch requests are not supported"}})
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeHTTPRPCResponse(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}

	if req.Method == "" {
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err == nil {
			if _, ok := raw["result"]; ok {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			if _, ok := raw["error"]; ok {
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}
		writeHTTPRPCResponse(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32600, Message: "invalid request"}})
		return
	}

	if !req.hasID() {
		s.handleNotification(req)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := s.executeRPCOverHTTP(r.Context(), req)
	writeHTTPRPCResponse(w, resp)
}

func (s *Server) executeRPCOverHTTP(ctx context.Context, req rpcRequest) rpcResponse {
	requestID := req.idString()
	ctx = readeck.WithRequestID(ctx, requestID)

	s.logger.Printf("request_id=%s method=%s transport=http", requestID, req.Method)
	start := time.Now()
	defer func() {
		s.logger.Printf("request_id=%s method=%s transport=http duration_ms=%d", requestID, req.Method, time.Since(start).Milliseconds())
	}()

	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": s.cfg.Protocol,
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{"subscribe": false},
				"prompts":   map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    s.cfg.ServerName,
				"version": s.cfg.ServerVersion,
			},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": []map[string]any{
			{"name": "readeck.search", "description": "Search and filter bookmarks.", "inputSchema": searchInputSchema()},
			{"name": "readeck.get", "description": "Fetch one bookmark with optional content and highlights.", "inputSchema": getInputSchema()},
			{"name": "readeck.archive", "description": "Archive or unarchive a bookmark.", "inputSchema": archiveInputSchema()},
			{"name": "readeck.labels.list", "description": "List all labels.", "inputSchema": labelsListInputSchema()},
			{"name": "readeck.labels.set", "description": "Replace labels on a bookmark.", "inputSchema": labelsSetInputSchema()},
			{"name": "readeck.highlights.list", "description": "List highlights for a bookmark.", "inputSchema": highlightsListInputSchema()},
			{"name": "readeck.cite", "description": "Generate citations for a bookmark in multiple styles.", "inputSchema": citeInputSchema()},
		}}
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		result, err := s.executeTool(ctx, params.Name, params.Arguments)
		if err != nil {
			mapped := mapToolError(err)
			resp.Result = map[string]any{
				"isError": true,
				"content": []map[string]any{{"type": "text", "text": mapped.Message}},
				"structuredContent": map[string]any{
					"error": mapped,
				},
			}
			break
		}
		resp.Result = map[string]any{
			"content":           []map[string]any{{"type": "text", "text": mustJSON(result)}},
			"structuredContent": result,
		}
	case "resources/list", "resources/templates/list":
		resp.Result = map[string]any{"resources": []map[string]any{
			{"uriTemplate": "readeck://bookmark/{id}", "name": "Bookmark metadata", "mimeType": "application/json"},
			{"uriTemplate": "readeck://bookmark/{id}/content.md", "name": "Bookmark content markdown", "mimeType": "text/markdown"},
			{"uriTemplate": "readeck://bookmark/{id}/content.txt", "name": "Bookmark content text", "mimeType": "text/plain"},
			{"uriTemplate": "readeck://bookmark/{id}/highlights.json", "name": "Bookmark highlights JSON", "mimeType": "application/json"},
			{"uriTemplate": "readeck://bookmark/{id}/highlights.md", "name": "Bookmark highlights markdown", "mimeType": "text/markdown"},
		}}
	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		if strings.TrimSpace(params.URI) == "" {
			resp.Error = &rpcError{Code: -32602, Message: "uri is required"}
			break
		}
		parsed, err := parseReadeckURI(params.URI)
		if err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid resource uri"}
			break
		}
		bookmark, err := s.client.GetBookmark(ctx, parsed.ID, readeck.IncludeOptions{
			Content:    parsed.Kind == "content.md" || parsed.Kind == "content.txt",
			Highlights: parsed.Kind == "highlights.json" || parsed.Kind == "highlights.md",
			Labels:     true,
		})
		if err != nil {
			mapped := mapToolError(err)
			resp.Error = &rpcError{Code: -32000, Message: mapped.Message, Data: map[string]any{"error": mapped}}
			break
		}

		mime := "application/json"
		text := ""
		switch parsed.Kind {
		case "metadata":
			data := bookmark
			data.ContentText = ""
			data.ContentHTML = ""
			data.Highlights = nil
			text = mustJSON(data)
		case "content.md":
			mime = "text/markdown"
			text = render.BookmarkContentMarkdown(bookmark, false)
		case "content.txt":
			mime = "text/plain"
			text = render.BookmarkContentText(bookmark)
		case "highlights.json":
			text = mustJSON(map[string]any{"highlights": bookmark.Highlights})
		case "highlights.md":
			mime = "text/markdown"
			text = render.HighlightsMarkdown(bookmark.Highlights)
		default:
			resp.Error = &rpcError{Code: -32602, Message: "unsupported resource uri"}
		}
		if resp.Error == nil {
			resp.Result = map[string]any{"contents": []map[string]any{{"uri": params.URI, "mimeType": mime, "text": text}}}
		}
	case "prompts/list":
		resp.Result = map[string]any{"prompts": []map[string]any{
			{
				"name":        "readeck.prompt.summarize",
				"description": "Summarize a bookmark with optional focus mode.",
				"arguments": []map[string]any{
					{"name": "bookmark_id", "required": true},
					{"name": "focus", "required": false},
				},
			},
			{
				"name":        "readeck.prompt.flashcards",
				"description": "Create flashcards from bookmark content/highlights.",
				"arguments": []map[string]any{
					{"name": "bookmark_id", "required": true},
					{"name": "num_cards", "required": false},
					{"name": "card_type", "required": false},
					{"name": "use_highlights", "required": false},
				},
			},
		}}
	case "prompts/get":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		bookmarkID, _ := params.Arguments["bookmark_id"].(string)
		if strings.TrimSpace(bookmarkID) == "" {
			resp.Error = &rpcError{Code: -32602, Message: "bookmark_id is required"}
			break
		}
		switch params.Name {
		case "readeck.prompt.summarize":
			focus, _ := params.Arguments["focus"].(string)
			if focus == "" {
				focus = "key_ideas"
			}
			text := fmt.Sprintf("Summarize this article with focus on %s. Read:\n- readeck://bookmark/%s/content.md\n- readeck://bookmark/%s/highlights.md", focus, bookmarkID, bookmarkID)
			resp.Result = promptResult("Summarize bookmark", text)
		case "readeck.prompt.flashcards":
			numCards := 10
			if raw, ok := params.Arguments["num_cards"]; ok {
				numCards = int(toFloat(raw, 10))
			}
			cardType, _ := params.Arguments["card_type"].(string)
			if cardType == "" {
				cardType = "qa"
			}
			useHighlights := true
			if raw, ok := params.Arguments["use_highlights"]; ok {
				if b, ok := raw.(bool); ok {
					useHighlights = b
				}
			}
			text := fmt.Sprintf("Generate %d %s flashcards from this article. Read:\n- readeck://bookmark/%s/content.md", numCards, cardType, bookmarkID)
			if useHighlights {
				text += fmt.Sprintf("\n- readeck://bookmark/%s/highlights.md", bookmarkID)
			}
			resp.Result = promptResult("Flashcards from bookmark", text)
		default:
			resp.Error = &rpcError{Code: -32602, Message: "unknown prompt"}
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
	}

	return resp
}

func writeHTTPRPCResponse(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	payload, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func acceptsRPCResponse(accept string) bool {
	accept = strings.ToLower(strings.TrimSpace(accept))
	if accept == "" {
		return true
	}
	if strings.Contains(accept, "*/*") {
		return true
	}
	if strings.Contains(accept, "application/json") {
		return true
	}
	if strings.Contains(accept, "text/event-stream") {
		return true
	}
	return false
}

func (s *Server) isHTTPAuthorized(r *http.Request) bool {
	expected := s.cfg.HTTPAuthToken
	if expected == "" {
		return true
	}
	provided, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func (s *Server) isOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if len(s.cfg.AllowedOrigins) == 0 {
		return false
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if allowed == "*" || strings.EqualFold(origin, allowed) {
			return true
		}
	}
	return false
}

func bearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return "", false
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}
	return token, true
}
