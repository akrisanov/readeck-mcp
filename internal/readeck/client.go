package readeck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akrisanov/readeck-mcp/internal/config"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

const (
	defaultSearchLimit = 20
	defaultListLimit   = 200
	maxLabelsLimit     = 500
)

type Client struct {
	apiBase     string
	token       string
	userAgent   string
	httpClient  *http.Client
	maxPageSize int
	logger      *log.Logger
}

func NewClient(cfg config.Config, logger *log.Logger) *Client {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Client{
		apiBase:     strings.TrimRight(cfg.APIBaseURL, "/"),
		token:       cfg.APIToken,
		userAgent:   cfg.UserAgent,
		httpClient:  config.NewHTTPClient(cfg),
		maxPageSize: cfg.MaxPageSize,
		logger:      logger,
	}
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, requestID)
}

func (c *Client) Search(ctx context.Context, opts SearchOptions) (SearchResult, error) {
	opts = normalizeSearchOptions(opts, c.maxPageSize)
	params := buildSearchQuery(opts)

	respMap, err := c.getObject(ctx, "/bookmarks", params)
	if err != nil {
		return SearchResult{}, err
	}

	rawItems, next := extractItemsAndCursor(respMap)
	items := make([]BookmarkSummary, 0, len(rawItems))
	for _, raw := range rawItems {
		bm := mapBookmark(raw)
		if !matchesFilters(bm, opts) {
			continue
		}
		summary := BookmarkSummary{
			ID:          bm.ID,
			Title:       bm.Title,
			URL:         bm.URL,
			IsArchived:  bm.IsArchived,
			Labels:      labelNames(bm.Labels),
			CreatedAt:   bm.CreatedAt,
			UpdatedAt:   bm.UpdatedAt,
			PublishedAt: bm.PublishedAt,
			Snippet:     snippetFromMap(raw),
		}
		items = append(items, summary)
	}

	sortSummaries(items, opts.Sort)
	if len(items) > opts.Limit {
		items = items[:opts.Limit]
	}

	return SearchResult{Items: items, NextCursor: next}, nil
}

func (c *Client) GetBookmark(ctx context.Context, id string, include IncludeOptions) (Bookmark, error) {
	if strings.TrimSpace(id) == "" {
		return Bookmark{}, errors.New("id is required")
	}

	respMap, err := c.getObject(ctx, "/bookmarks/"+url.PathEscape(id), nil)
	if err != nil {
		return Bookmark{}, err
	}
	bookmark := mapBookmark(respMap)

	if include.Content {
		text, html, err := c.fetchContent(ctx, id)
		if err == nil {
			bookmark.ContentText = text
			bookmark.ContentHTML = html
		}
	}

	if include.Highlights {
		highlights, err := c.ListHighlights(ctx, id, defaultListLimit, "")
		if err == nil {
			bookmark.Highlights = highlights.Highlights
		}
	}

	if !include.Labels {
		bookmark.Labels = nil
	}

	return bookmark, nil
}

func (c *Client) SetArchived(ctx context.Context, id string, archived bool) (ArchiveResult, error) {
	if strings.TrimSpace(id) == "" {
		return ArchiveResult{}, errors.New("id is required")
	}

	body := map[string]any{"is_archived": archived, "archived": archived}
	_, err := c.requestObject(ctx, http.MethodPatch, "/bookmarks/"+url.PathEscape(id), nil, body)
	if err != nil {
		if httpErr := new(HTTPError); errors.As(err, &httpErr) {
			switch httpErr.StatusCode {
			case http.StatusMethodNotAllowed:
				err = c.archiveFallback(ctx, id, archived)
			case http.StatusConflict, http.StatusUnprocessableEntity:
				err = nil
			}
		}
	}
	if err != nil {
		return ArchiveResult{}, err
	}

	bm, err := c.GetBookmark(ctx, id, IncludeOptions{Labels: true})
	if err != nil {
		return ArchiveResult{}, err
	}

	return ArchiveResult{ID: bm.ID, IsArchived: bm.IsArchived, UpdatedAt: bm.UpdatedAt}, nil
}

func (c *Client) archiveFallback(ctx context.Context, id string, archived bool) error {
	pathID := "/bookmarks/" + url.PathEscape(id) + "/archive"
	if archived {
		_, err := c.requestObject(ctx, http.MethodPost, pathID, nil, map[string]any{"archived": true})
		if err != nil {
			if httpErr := new(HTTPError); errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
				return c.patchArchivedField(ctx, id, archived)
			}
			return err
		}
		return nil
	}

	_, err := c.requestObject(ctx, http.MethodDelete, pathID, nil, nil)
	if err != nil {
		if httpErr := new(HTTPError); errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusMethodNotAllowed || httpErr.StatusCode == http.StatusNotFound) {
			return c.patchArchivedField(ctx, id, archived)
		}
		return err
	}
	return nil
}

func (c *Client) patchArchivedField(ctx context.Context, id string, archived bool) error {
	_, err := c.requestObject(ctx, http.MethodPatch, "/bookmarks/"+url.PathEscape(id), nil, map[string]any{"archived": archived})
	return err
}

func (c *Client) ListLabels(ctx context.Context, limit int, cursor string) (LabelListResult, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxLabelsLimit {
		limit = maxLabelsLimit
	}

	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	respMap, err := c.getObject(ctx, "/labels", params)
	if err != nil {
		if httpErr := new(HTTPError); errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			respMap, err = c.getObject(ctx, "/bookmarks/labels", params)
		}
	}
	if err != nil {
		return LabelListResult{}, err
	}

	rawItems, next := extractItemsAndCursor(respMap)
	labels := make([]Label, 0, len(rawItems))
	for _, raw := range rawItems {
		label := mapLabel(raw)
		if strings.TrimSpace(label.Name) == "" {
			continue
		}
		labels = append(labels, label)
	}
	return LabelListResult{Labels: labels, NextCursor: next}, nil
}

func (c *Client) SetLabels(ctx context.Context, id string, labels []string) (SetLabelsResult, error) {
	if strings.TrimSpace(id) == "" {
		return SetLabelsResult{}, errors.New("id is required")
	}
	if len(labels) == 0 {
		return SetLabelsResult{}, errors.New("labels is required")
	}

	normalized := normalizeLabels(labels)
	body := map[string]any{"labels": normalized}

	obj, err := c.requestObject(ctx, http.MethodPatch, "/bookmarks/"+url.PathEscape(id), nil, body)
	if err != nil {
		if httpErr := new(HTTPError); errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusMethodNotAllowed {
			obj, err = c.requestObject(ctx, http.MethodPut, "/bookmarks/"+url.PathEscape(id)+"/labels", nil, body)
		}
	}
	if err != nil {
		return SetLabelsResult{}, err
	}

	bookmark := mapBookmark(obj)
	result := SetLabelsResult{ID: bookmark.ID, Labels: normalized}
	if len(bookmark.Labels) > 0 {
		result.Labels = labelNames(bookmark.Labels)
	}
	return result, nil
}

func (c *Client) ListHighlights(ctx context.Context, bookmarkID string, limit int, cursor string) (HighlightListResult, error) {
	if strings.TrimSpace(bookmarkID) == "" {
		return HighlightListResult{}, errors.New("bookmark_id is required")
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxLabelsLimit {
		limit = maxLabelsLimit
	}

	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	respMap, err := c.getObject(ctx, "/bookmarks/"+url.PathEscape(bookmarkID)+"/highlights", params)
	if err != nil {
		if httpErr := new(HTTPError); errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			respMap, err = c.getObject(ctx, "/bookmarks/"+url.PathEscape(bookmarkID)+"/annotations", params)
		}
	}
	if err != nil {
		if httpErr := new(HTTPError); errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			params.Set("bookmark_id", bookmarkID)
			respMap, err = c.getObject(ctx, "/bookmarks/annotations", params)
		}
	}
	if err != nil {
		if httpErr := new(HTTPError); errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			respMap, err = c.getObject(ctx, "/highlights", params)
		}
	}
	if err != nil {
		return HighlightListResult{}, err
	}

	rawItems, next := extractItemsAndCursor(respMap)
	highlights := make([]Highlight, 0, len(rawItems))
	for _, raw := range rawItems {
		h := mapHighlight(raw)
		if h.ID == "" {
			continue
		}
		if h.BookmarkID == "" {
			h.BookmarkID = bookmarkID
		}
		if h.BookmarkID != bookmarkID {
			continue
		}
		highlights = append(highlights, h)
	}

	return HighlightListResult{Highlights: highlights, NextCursor: next}, nil
}

func (c *Client) fetchContent(ctx context.Context, id string) (string, string, error) {
	candidates := []string{
		"/bookmarks/" + url.PathEscape(id) + "/content",
		"/bookmarks/" + url.PathEscape(id) + "/article",
		"/bookmarks/" + url.PathEscape(id) + "/text",
	}
	for _, endpoint := range candidates {
		obj, err := c.getObject(ctx, endpoint, nil)
		if err != nil {
			if httpErr := new(HTTPError); errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
				continue
			}
			return "", "", err
		}
		text := firstNonEmptyString(obj, "content_text", "text", "content", "article")
		html := firstNonEmptyString(obj, "content_html", "html")
		if text != "" || html != "" {
			return text, html, nil
		}
	}
	return "", "", nil
}

func normalizeSearchOptions(opts SearchOptions, maxPageSize int) SearchOptions {
	opts.Archived = normalizeArchivedMode(opts.Archived)
	if opts.Sort == "" {
		opts.Sort = SortUpdatedDesc
	}
	if opts.Limit <= 0 {
		opts.Limit = defaultSearchLimit
	}
	if opts.Limit > maxPageSize {
		opts.Limit = maxPageSize
	}
	opts.Labels = normalizeLabels(opts.Labels)
	return opts
}

func normalizeArchivedMode(mode ArchivedMode) ArchivedMode {
	switch mode {
	case ArchivedExclude, ArchivedInclude, ArchivedOnly:
		return mode
	default:
		return ArchivedExclude
	}
}

func buildSearchQuery(opts SearchOptions) url.Values {
	params := url.Values{}
	search := strings.TrimSpace(opts.Query)
	if text := strings.TrimSpace(opts.Text); text != "" {
		if search == "" {
			search = text
		} else {
			search = search + " " + text
		}
	}
	if search != "" {
		params.Set("q", search)
	}
	if title := strings.TrimSpace(opts.Title); title != "" {
		params.Set("title", title)
	}
	if len(opts.Labels) > 0 {
		params.Set("labels", strings.Join(opts.Labels, ","))
	}
	if opts.Favorites != nil {
		params.Set("favorite", strconv.FormatBool(*opts.Favorites))
	}
	if opts.Sort != "" {
		params.Set("sort", string(opts.Sort))
	}
	if opts.Limit > 0 {
		params.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		params.Set("cursor", opts.Cursor)
	}
	return params
}

func (c *Client) getObject(ctx context.Context, endpoint string, query url.Values) (map[string]any, error) {
	respBytes, statusCode, reqID, err := c.do(ctx, http.MethodGet, endpoint, query, nil)
	if err != nil {
		return nil, err
	}
	if len(respBytes) == 0 {
		return map[string]any{}, nil
	}

	var obj map[string]any
	if err := json.Unmarshal(respBytes, &obj); err == nil {
		return obj, nil
	}

	var arr []map[string]any
	if err := json.Unmarshal(respBytes, &arr); err == nil {
		return map[string]any{"items": arr}, nil
	}

	return nil, &HTTPError{
		StatusCode: statusCode,
		Endpoint:   endpoint,
		RequestID:  reqID,
		Message:    "decode response: unsupported JSON shape",
	}
}

func (c *Client) requestObject(ctx context.Context, method, endpoint string, query url.Values, body any) (map[string]any, error) {
	respBytes, statusCode, reqID, err := c.do(ctx, method, endpoint, query, body)
	if err != nil {
		return nil, err
	}
	if len(respBytes) == 0 {
		return map[string]any{}, nil
	}

	var obj map[string]any
	if unmarshalErr := json.Unmarshal(respBytes, &obj); unmarshalErr != nil {
		return nil, &HTTPError{StatusCode: statusCode, Endpoint: endpoint, RequestID: reqID, Message: fmt.Sprintf("decode response: %v", unmarshalErr)}
	}
	return obj, nil
}

func (c *Client) do(ctx context.Context, method, endpoint string, query url.Values, body any) ([]byte, int, string, error) {
	endpoint = "/" + strings.TrimLeft(endpoint, "/")
	u, err := url.Parse(c.apiBase)
	if err != nil {
		return nil, 0, "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + endpoint
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, 0, "", err
		}
	}

	attempt := 0
	for {
		attempt++
		statusCode, requestID, respBytes, reqErr := c.doOnce(ctx, method, endpoint, u.String(), payload, attempt-1)
		if reqErr != nil {
			return nil, statusCode, requestID, reqErr
		}

		if method == http.MethodGet && (statusCode == http.StatusTooManyRequests || statusCode >= 500) && attempt < 4 {
			backoff := retryBackoff(attempt)
			if err := waitForRetry(ctx, backoff); err != nil {
				return nil, statusCode, requestID, err
			}
			continue
		}

		if statusCode >= 400 {
			return nil, statusCode, requestID, &HTTPError{
				StatusCode: statusCode,
				Endpoint:   endpoint,
				RequestID:  requestID,
				Message:    fmt.Sprintf("upstream returned status %d", statusCode),
			}
		}
		return respBytes, statusCode, requestID, nil
	}
}

func (c *Client) doOnce(ctx context.Context, method, endpoint, fullURL string, payload []byte, retries int) (int, string, []byte, error) {
	var body io.Reader
	if len(payload) > 0 {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return 0, "", nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logRequest(ctx, method, endpoint, 0, time.Since(start), 0, retries)
		return 0, "", nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", nil, err
	}

	requestID := firstNonEmpty(resp.Header.Get("X-Request-Id"), resp.Header.Get("X-Request-ID"))
	c.logRequest(ctx, method, endpoint, resp.StatusCode, time.Since(start), len(respBytes), retries)

	return resp.StatusCode, requestID, respBytes, nil
}

func (c *Client) logRequest(ctx context.Context, method, endpoint string, status int, latency time.Duration, size int, retries int) {
	requestID, _ := ctx.Value(requestIDKey).(string)
	c.logger.Printf("request_id=%s method=%s endpoint=%s status=%d latency_ms=%d retries=%d bytes=%d", requestID, method, endpoint, status, latency.Milliseconds(), retries, size)
}

func extractItemsAndCursor(obj map[string]any) ([]map[string]any, string) {
	if obj == nil {
		return nil, ""
	}

	next := firstNonEmptyString(obj, "next_cursor", "next", "cursor")
	items := extractArrayMap(obj, "items")
	if len(items) == 0 {
		items = extractArrayMap(obj, "results")
	}
	if len(items) == 0 {
		items = extractArrayMap(obj, "bookmarks")
	}
	if len(items) == 0 {
		items = extractArrayMap(obj, "labels")
	}
	if len(items) == 0 {
		items = extractArrayMap(obj, "highlights")
	}
	if len(items) == 0 {
		items = extractArrayMap(obj, "data")
	}

	if len(items) == 0 {
		if _, hasID := obj["id"]; hasID {
			items = []map[string]any{obj}
		}
	}

	return items, next
}

func matchesFilters(b Bookmark, opts SearchOptions) bool {
	if opts.Archived == ArchivedExclude && b.IsArchived {
		return false
	}
	if opts.Archived == ArchivedOnly && !b.IsArchived {
		return false
	}
	if opts.Favorites != nil && b.IsFavorite != *opts.Favorites {
		return false
	}
	if opts.Title != "" && !containsFold(b.Title, opts.Title) {
		return false
	}
	if len(opts.Labels) > 0 {
		have := map[string]struct{}{}
		for _, l := range b.Labels {
			have[strings.ToLower(strings.TrimSpace(l.Name))] = struct{}{}
		}
		for _, want := range opts.Labels {
			if _, ok := have[strings.ToLower(want)]; !ok {
				return false
			}
		}
	}
	return true
}

func sortSummaries(items []BookmarkSummary, mode SortMode) {
	if mode == "" {
		mode = SortUpdatedDesc
	}
	if mode == SortRelevance {
		return
	}

	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		switch mode {
		case SortPublishedDesc:
			return left.PublishedAt > right.PublishedAt
		case SortCreatedDesc:
			return left.CreatedAt > right.CreatedAt
		default:
			return left.UpdatedAt > right.UpdatedAt
		}
	})
}

func normalizeLabels(labels []string) []string {
	out := make([]string, 0, len(labels))
	seen := map[string]struct{}{}
	for _, label := range labels {
		clean := strings.TrimSpace(label)
		if clean == "" {
			continue
		}
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func labelNames(labels []Label) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if strings.TrimSpace(l.Name) != "" {
			out = append(out, l.Name)
		}
	}
	return out
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func snippetFromMap(obj map[string]any) string {
	s := firstNonEmptyString(obj, "snippet", "excerpt", "summary", "description", "content_text")
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 280 {
		s = s[:280] + "..."
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func retryBackoff(attempt int) time.Duration {
	// attempt is 1-based in caller; retries start after first attempt.
	return 200 * time.Millisecond * time.Duration(1<<(attempt-1))
}

func waitForRetry(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
