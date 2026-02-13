package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akrisanov/readeck-mcp/internal/citation"
	"github.com/akrisanov/readeck-mcp/internal/config"
	"github.com/akrisanov/readeck-mcp/internal/readeck"
	"github.com/akrisanov/readeck-mcp/internal/render"
)

type Server struct {
	cfg     config.Config
	client  *readeck.Client
	logger  *log.Logger
	in      io.Reader
	out     io.Writer
	writeMu sync.Mutex
}

func NewServer(cfg config.Config, client *readeck.Client, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Server{
		cfg:    cfg,
		client: client,
		logger: logger,
		in:     os.Stdin,
		out:    os.Stdout,
	}
}

func (s *Server) Run(ctx context.Context) error {
	reader := bufio.NewReader(s.in)
	for {
		payload, err := readMessage(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		var req rpcRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			_ = s.writeError(nil, -32700, "parse error", nil)
			continue
		}

		if req.Method == "" {
			_ = s.writeError(req.ID, -32600, "invalid request", nil)
			continue
		}

		if !req.hasID() {
			s.handleNotification(req)
			continue
		}

		if err := s.handleRequest(ctx, req); err != nil {
			if writeErr := s.writeError(req.ID, -32000, err.Error(), nil); writeErr != nil {
				return writeErr
			}
		}
	}
}

func (s *Server) handleNotification(req rpcRequest) {
	if req.Method == "notifications/initialized" || req.Method == "initialized" {
		s.logger.Printf("client initialized")
	}
}

func (s *Server) handleRequest(ctx context.Context, req rpcRequest) error {
	requestID := req.idString()
	ctx = readeck.WithRequestID(ctx, requestID)

	s.logger.Printf("request_id=%s method=%s", requestID, req.Method)
	start := time.Now()
	defer func() {
		s.logger.Printf("request_id=%s method=%s duration_ms=%d", requestID, req.Method, time.Since(start).Milliseconds())
	}()

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return s.writeResult(req.ID, map[string]any{})
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	case "resources/list":
		return s.handleResourcesList(req)
	case "resources/templates/list":
		return s.handleResourcesList(req)
	case "resources/read":
		return s.handleResourcesRead(ctx, req)
	case "prompts/list":
		return s.handlePromptsList(req)
	case "prompts/get":
		return s.handlePromptsGet(req)
	default:
		return s.writeError(req.ID, -32601, "method not found", nil)
	}
}

func (s *Server) handleInitialize(req rpcRequest) error {
	result := map[string]any{
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
	return s.writeResult(req.ID, result)
}

func (s *Server) handleToolsList(req rpcRequest) error {
	tools := []map[string]any{
		{
			"name":        "readeck.search",
			"description": "Search and filter bookmarks.",
			"inputSchema": searchInputSchema(),
		},
		{
			"name":        "readeck.get",
			"description": "Fetch one bookmark with optional content and highlights.",
			"inputSchema": getInputSchema(),
		},
		{
			"name":        "readeck.archive",
			"description": "Archive or unarchive a bookmark.",
			"inputSchema": archiveInputSchema(),
		},
		{
			"name":        "readeck.labels.list",
			"description": "List all labels.",
			"inputSchema": labelsListInputSchema(),
		},
		{
			"name":        "readeck.labels.set",
			"description": "Replace labels on a bookmark.",
			"inputSchema": labelsSetInputSchema(),
		},
		{
			"name":        "readeck.highlights.list",
			"description": "List annotations/highlights globally or per bookmark, with optional date filtering.",
			"inputSchema": highlightsListInputSchema(),
		},
		{
			"name":        "readeck.cite",
			"description": "Generate citations for a bookmark in multiple styles.",
			"inputSchema": citeInputSchema(),
		},
	}
	return s.writeResult(req.ID, map[string]any{"tools": tools})
}

func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) error {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, -32602, "invalid params", nil)
	}

	result, err := s.executeTool(ctx, params.Name, params.Arguments)
	if err != nil {
		mapped := mapToolError(err)
		payload := map[string]any{
			"isError": true,
			"content": []map[string]any{{"type": "text", "text": mapped.Message}},
			"structuredContent": map[string]any{
				"error": mapped,
			},
		}
		return s.writeResult(req.ID, payload)
	}

	outText := mustJSON(result)
	payload := map[string]any{
		"content":           []map[string]any{{"type": "text", "text": outText}},
		"structuredContent": result,
	}
	return s.writeResult(req.ID, payload)
}

func (s *Server) executeTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	switch name {
	case "readeck.search":
		var in struct {
			Query     string   `json:"query"`
			Title     string   `json:"title"`
			Text      string   `json:"text"`
			Labels    []string `json:"labels"`
			Archived  string   `json:"archived"`
			Favorites *bool    `json:"favorites"`
			Sort      string   `json:"sort"`
			Limit     int      `json:"limit"`
			Cursor    string   `json:"cursor"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		opts := readeck.SearchOptions{
			Query:     in.Query,
			Title:     in.Title,
			Text:      in.Text,
			Labels:    in.Labels,
			Archived:  readeck.ArchivedMode(in.Archived),
			Favorites: in.Favorites,
			Sort:      readeck.SortMode(in.Sort),
			Limit:     in.Limit,
			Cursor:    in.Cursor,
		}
		if opts.Archived == "" {
			opts.Archived = readeck.ArchivedExclude
		}
		return s.client.Search(ctx, opts)

	case "readeck.get":
		var in struct {
			ID      string `json:"id"`
			Include struct {
				Content    *bool `json:"content"`
				Highlights *bool `json:"highlights"`
				Labels     *bool `json:"labels"`
			} `json:"include"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.ID) == "" {
			return nil, newInputError("id is required")
		}
		include := readeck.IncludeOptions{Content: false, Highlights: true, Labels: true}
		if in.Include.Content != nil {
			include.Content = *in.Include.Content
		}
		if in.Include.Highlights != nil {
			include.Highlights = *in.Include.Highlights
		}
		if in.Include.Labels != nil {
			include.Labels = *in.Include.Labels
		}
		bookmark, err := s.client.GetBookmark(ctx, in.ID, include)
		if err != nil {
			return nil, err
		}
		return map[string]any{"bookmark": bookmark}, nil

	case "readeck.archive":
		var in struct {
			ID       string `json:"id"`
			Archived *bool  `json:"archived"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.ID) == "" {
			return nil, newInputError("id is required")
		}
		archived := true
		if in.Archived != nil {
			archived = *in.Archived
		}
		return s.client.SetArchived(ctx, in.ID, archived)

	case "readeck.labels.list":
		var in struct {
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		return s.client.ListLabels(ctx, in.Limit, in.Cursor)

	case "readeck.labels.set":
		var in struct {
			ID     string   `json:"id"`
			Labels []string `json:"labels"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.ID) == "" {
			return nil, newInputError("id is required")
		}
		if len(in.Labels) == 0 {
			return nil, newInputError("labels is required")
		}
		return s.client.SetLabels(ctx, in.ID, in.Labels)

	case "readeck.highlights.list":
		var in struct {
			BookmarkID string `json:"bookmark_id"`
			Limit      int    `json:"limit"`
			Offset     int    `json:"offset"`
			Date       string `json:"date"`
			DateFrom   string `json:"date_from"`
			DateTo     string `json:"date_to"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		if in.Offset < 0 {
			return nil, newInputError("offset must be >= 0")
		}
		dateFilter, err := parseHighlightDateFilter(in.Date, in.DateFrom, in.DateTo)
		if err != nil {
			return nil, newInputError(err.Error())
		}
		return s.listHighlights(ctx, in.BookmarkID, in.Limit, in.Offset, dateFilter)

	case "readeck.cite":
		var in struct {
			BookmarkID string `json:"bookmark_id"`
			Highlight  string `json:"highlight_id"`
			Quote      string `json:"quote"`
			Style      string `json:"style"`
			AccessedAt string `json:"accessed_at"`
		}
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.BookmarkID) == "" {
			return nil, newInputError("bookmark_id is required")
		}
		bookmark, err := s.client.GetBookmark(ctx, in.BookmarkID, readeck.IncludeOptions{Highlights: true, Labels: true})
		if err != nil {
			return nil, err
		}

		var selected *readeck.Highlight
		if strings.TrimSpace(in.Highlight) != "" {
			for i := range bookmark.Highlights {
				if bookmark.Highlights[i].ID == in.Highlight {
					selected = &bookmark.Highlights[i]
					break
				}
			}
			if selected == nil {
				return nil, newInputError("highlight_id not found for bookmark")
			}
		}

		accessedAt := time.Now().UTC()
		if strings.TrimSpace(in.AccessedAt) != "" {
			parsed, err := time.Parse(time.RFC3339, in.AccessedAt)
			if err != nil {
				return nil, newInputError("accessed_at must be RFC3339")
			}
			accessedAt = parsed
		}

		style := readeck.CitationStyle(strings.TrimSpace(in.Style))
		cite := citation.Generate(bookmark, selected, in.Quote, style, accessedAt)
		return map[string]any{"citation": cite}, nil

	default:
		return nil, newInputError("unknown tool: " + name)
	}
}

func (s *Server) handleResourcesList(req rpcRequest) error {
	resources := []map[string]any{
		{"uriTemplate": "readeck://bookmark/{id}", "name": "Bookmark metadata", "mimeType": "application/json"},
		{"uriTemplate": "readeck://bookmark/{id}/content.md", "name": "Bookmark content markdown", "mimeType": "text/markdown"},
		{"uriTemplate": "readeck://bookmark/{id}/content.txt", "name": "Bookmark content text", "mimeType": "text/plain"},
		{"uriTemplate": "readeck://bookmark/{id}/highlights.json", "name": "Bookmark highlights JSON", "mimeType": "application/json"},
		{"uriTemplate": "readeck://bookmark/{id}/highlights.md", "name": "Bookmark highlights markdown", "mimeType": "text/markdown"},
	}
	return s.writeResult(req.ID, map[string]any{"resources": resources})
}

func (s *Server) handleResourcesRead(ctx context.Context, req rpcRequest) error {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, -32602, "invalid params", nil)
	}
	if strings.TrimSpace(params.URI) == "" {
		return s.writeError(req.ID, -32602, "uri is required", nil)
	}

	parsed, err := parseReadeckURI(params.URI)
	if err != nil {
		return s.writeError(req.ID, -32602, "invalid resource uri", nil)
	}

	bookmark, err := s.client.GetBookmark(ctx, parsed.ID, readeck.IncludeOptions{
		Content:    parsed.Kind == "content.md" || parsed.Kind == "content.txt",
		Highlights: parsed.Kind == "highlights.json" || parsed.Kind == "highlights.md",
		Labels:     true,
	})
	if err != nil {
		mapped := mapToolError(err)
		return s.writeError(req.ID, -32000, mapped.Message, map[string]any{"error": mapped})
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
		return s.writeError(req.ID, -32602, "unsupported resource uri", nil)
	}

	result := map[string]any{
		"contents": []map[string]any{{
			"uri":      params.URI,
			"mimeType": mime,
			"text":     text,
		}},
	}
	return s.writeResult(req.ID, result)
}

func (s *Server) handlePromptsList(req rpcRequest) error {
	prompts := []map[string]any{
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
	}
	return s.writeResult(req.ID, map[string]any{"prompts": prompts})
}

func (s *Server) handlePromptsGet(req rpcRequest) error {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.writeError(req.ID, -32602, "invalid params", nil)
	}

	bookmarkID, _ := params.Arguments["bookmark_id"].(string)
	if strings.TrimSpace(bookmarkID) == "" {
		return s.writeError(req.ID, -32602, "bookmark_id is required", nil)
	}

	switch params.Name {
	case "readeck.prompt.summarize":
		focus, _ := params.Arguments["focus"].(string)
		if focus == "" {
			focus = "key_ideas"
		}
		text := fmt.Sprintf("Summarize this article with focus on %s. Read:\n- readeck://bookmark/%s/content.md\n- readeck://bookmark/%s/highlights.md", focus, bookmarkID, bookmarkID)
		return s.writeResult(req.ID, promptResult("Summarize bookmark", text))
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
		return s.writeResult(req.ID, promptResult("Flashcards from bookmark", text))
	default:
		return s.writeError(req.ID, -32602, "unknown prompt", nil)
	}
}

func promptResult(description, text string) map[string]any {
	return map[string]any{
		"description": description,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": map[string]any{
					"type": "text",
					"text": text,
				},
			},
		},
	}
}

func (s *Server) writeResult(id json.RawMessage, result any) error {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
	return s.writeMessage(resp)
}

func (s *Server) writeError(id json.RawMessage, code int, message string, data any) error {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}}
	return s.writeMessage(resp)
}

func (s *Server) writeMessage(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := io.WriteString(s.out, frame); err != nil {
		return err
	}
	_, err = s.out.Write(payload)
	return err
}

func readMessage(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		if key == "content-length" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid content-length")
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing content-length")
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodeArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return newInputError("invalid arguments")
	}
	return nil
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (r rpcRequest) hasID() bool {
	trimmed := strings.TrimSpace(string(r.ID))
	return trimmed != "" && trimmed != "null"
}

func (r rpcRequest) idString() string {
	if !r.hasID() {
		return ""
	}
	var s string
	if err := json.Unmarshal(r.ID, &s); err == nil {
		return s
	}
	var n float64
	if err := json.Unmarshal(r.ID, &n); err == nil {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return string(r.ID)
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type inputError struct {
	msg string
}

func (e inputError) Error() string { return e.msg }

func newInputError(msg string) error {
	return inputError{msg: msg}
}

func mapToolError(err error) toolError {
	if err == nil {
		return toolError{Code: "upstream_error", Message: "unknown error"}
	}

	var inputErr inputError
	if errors.As(err, &inputErr) {
		return toolError{Code: "invalid_input", Message: err.Error()}
	}

	var httpErr *readeck.HTTPError
	if errors.As(err, &httpErr) {
		code := "upstream_error"
		switch httpErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			code = "unauthorized"
		case http.StatusNotFound:
			code = "not_found"
		case http.StatusTooManyRequests:
			code = "rate_limited"
		}
		return toolError{
			Code:    code,
			Message: err.Error(),
			Details: map[string]any{
				"http_status": httpErr.StatusCode,
				"endpoint":    httpErr.Endpoint,
				"request_id":  httpErr.RequestID,
			},
		}
	}

	return toolError{Code: "upstream_error", Message: err.Error()}
}

type parsedURI struct {
	ID   string
	Kind string
}

type highlightDateFilter struct {
	Start time.Time
	End   time.Time
}

func (f highlightDateFilter) enabled() bool {
	return !f.Start.IsZero() || !f.End.IsZero()
}

func (s *Server) listHighlights(ctx context.Context, bookmarkID string, limit, offset int, filter highlightDateFilter) (readeck.HighlightListResult, error) {
	if !filter.enabled() {
		return s.client.ListHighlights(ctx, bookmarkID, limit, offset)
	}

	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}

	batchSize := limit
	if batchSize < 100 {
		batchSize = 100
	}
	if batchSize > 500 {
		batchSize = 500
	}

	scanOffset := 0
	filteredSeen := 0
	out := make([]readeck.Highlight, 0, limit)
	hasMore := false

	for {
		page, err := s.client.ListHighlights(ctx, bookmarkID, batchSize, scanOffset)
		if err != nil {
			return readeck.HighlightListResult{}, err
		}
		if len(page.Highlights) == 0 {
			break
		}

		for _, h := range page.Highlights {
			if !highlightMatchesDateFilter(h, filter) {
				continue
			}
			if filteredSeen < offset {
				filteredSeen++
				continue
			}
			if len(out) >= limit {
				hasMore = true
				break
			}
			out = append(out, h)
			filteredSeen++
		}
		if hasMore {
			break
		}

		nextOffset, ok := parseNonNegativeInt(page.NextCursor)
		if !ok {
			if len(page.Highlights) < batchSize {
				break
			}
			nextOffset = scanOffset + len(page.Highlights)
		}
		if nextOffset <= scanOffset {
			break
		}
		scanOffset = nextOffset
	}

	nextCursor := ""
	if hasMore {
		nextCursor = strconv.Itoa(offset + len(out))
	}
	return readeck.HighlightListResult{Highlights: out, NextCursor: nextCursor}, nil
}

func parseHighlightDateFilter(date, dateFrom, dateTo string) (highlightDateFilter, error) {
	date = strings.TrimSpace(date)
	dateFrom = strings.TrimSpace(dateFrom)
	dateTo = strings.TrimSpace(dateTo)

	if date != "" && (dateFrom != "" || dateTo != "") {
		return highlightDateFilter{}, errors.New("date cannot be combined with date_from/date_to")
	}

	if date != "" {
		day, err := parseISODate(date)
		if err != nil {
			return highlightDateFilter{}, errors.New("date must be YYYY-MM-DD")
		}
		return highlightDateFilter{
			Start: day,
			End:   day.Add(24 * time.Hour),
		}, nil
	}

	var filter highlightDateFilter
	if dateFrom != "" {
		day, err := parseISODate(dateFrom)
		if err != nil {
			return highlightDateFilter{}, errors.New("date_from must be YYYY-MM-DD")
		}
		filter.Start = day
	}
	if dateTo != "" {
		day, err := parseISODate(dateTo)
		if err != nil {
			return highlightDateFilter{}, errors.New("date_to must be YYYY-MM-DD")
		}
		filter.End = day.Add(24 * time.Hour)
	}
	if !filter.Start.IsZero() && !filter.End.IsZero() && !filter.Start.Before(filter.End) {
		return highlightDateFilter{}, errors.New("date_from must be <= date_to")
	}
	return filter, nil
}

func parseISODate(raw string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func parseHighlightTimestamp(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("empty timestamp")
	}

	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed.UTC(), nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed.UTC(), nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.UTC); err == nil {
		return parsed.UTC(), nil
	}
	if parsed, err := parseISODate(raw); err == nil {
		return parsed.UTC(), nil
	}
	return time.Time{}, errors.New("unsupported timestamp format")
}

func highlightMatchesDateFilter(h readeck.Highlight, filter highlightDateFilter) bool {
	if !filter.enabled() {
		return true
	}
	createdAt, err := parseHighlightTimestamp(h.CreatedAt)
	if err != nil {
		return false
	}
	if !filter.Start.IsZero() && createdAt.Before(filter.Start) {
		return false
	}
	if !filter.End.IsZero() && !createdAt.Before(filter.End) {
		return false
	}
	return true
}

func parseNonNegativeInt(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

func parseReadeckURI(raw string) (parsedURI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return parsedURI{}, err
	}
	if u.Scheme != "readeck" || u.Host != "bookmark" {
		return parsedURI{}, fmt.Errorf("unsupported uri")
	}

	parts := strings.Split(strings.Trim(strings.TrimSpace(u.Path), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return parsedURI{}, fmt.Errorf("missing id")
	}
	id := parts[0]
	if len(parts) == 1 {
		return parsedURI{ID: id, Kind: "metadata"}, nil
	}
	kind := strings.Join(parts[1:], "/")
	switch kind {
	case "content.md", "content.txt", "highlights.json", "highlights.md":
		return parsedURI{ID: id, Kind: kind}, nil
	default:
		return parsedURI{}, fmt.Errorf("unsupported kind")
	}
}

func toFloat(v any, fallback float64) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return fallback
	}
}

func searchInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"title": map[string]any{"type": "string"},
			"text":  map[string]any{"type": "string"},
			"labels": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"archived":  map[string]any{"type": "string", "enum": []string{"exclude", "include", "only"}},
			"favorites": map[string]any{"type": "boolean"},
			"sort":      map[string]any{"type": "string", "enum": []string{"relevance", "updated_desc", "created_desc", "published_desc"}},
			"limit":     map[string]any{"type": "integer", "minimum": 1},
			"cursor":    map[string]any{"type": "string"},
		},
	}
}

func getInputSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"id"},
		"properties": map[string]any{
			"id": map[string]any{"type": "string"},
			"include": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content":    map[string]any{"type": "boolean"},
					"highlights": map[string]any{"type": "boolean"},
					"labels":     map[string]any{"type": "boolean"},
				},
			},
		},
	}
}

func archiveInputSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"id"},
		"properties": map[string]any{
			"id":       map[string]any{"type": "string"},
			"archived": map[string]any{"type": "boolean"},
		},
	}
}

func labelsListInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit":  map[string]any{"type": "integer", "minimum": 1},
			"cursor": map[string]any{"type": "string"},
		},
	}
}

func labelsSetInputSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"id", "labels"},
		"properties": map[string]any{
			"id": map[string]any{"type": "string"},
			"labels": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	}
}

func highlightsListInputSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "When bookmark_id is omitted, returns a global annotations feed across all bookmarks. Date filters are applied by this MCP server.",
		"properties": map[string]any{
			"bookmark_id": map[string]any{
				"type":        "string",
				"description": "Optional bookmark ID for bookmark-scoped annotations.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Page size.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Zero-based pagination offset.",
			},
			"date": map[string]any{
				"type":        "string",
				"description": "Filter annotations by a specific UTC date (YYYY-MM-DD).",
			},
			"date_from": map[string]any{
				"type":        "string",
				"description": "Filter annotations created on or after this UTC date (YYYY-MM-DD).",
			},
			"date_to": map[string]any{
				"type":        "string",
				"description": "Filter annotations created on or before this UTC date (YYYY-MM-DD).",
			},
		},
	}
}

func citeInputSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"bookmark_id"},
		"properties": map[string]any{
			"bookmark_id":  map[string]any{"type": "string"},
			"highlight_id": map[string]any{"type": "string"},
			"quote":        map[string]any{"type": "string"},
			"style": map[string]any{
				"type": "string",
				"enum": []string{"apa", "mla", "chicago", "bibtex", "csl-json", "markdown"},
			},
			"accessed_at": map[string]any{"type": "string", "format": "date-time"},
		},
	}
}
