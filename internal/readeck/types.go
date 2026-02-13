package readeck

import "encoding/json"

type ArchivedMode string

const (
	ArchivedExclude ArchivedMode = "exclude"
	ArchivedInclude ArchivedMode = "include"
	ArchivedOnly    ArchivedMode = "only"
)

type SortMode string

const (
	SortRelevance     SortMode = "relevance"
	SortUpdatedDesc   SortMode = "updated_desc"
	SortCreatedDesc   SortMode = "created_desc"
	SortPublishedDesc SortMode = "published_desc"
)

type CitationStyle string

const (
	StyleAPA      CitationStyle = "apa"
	StyleMLA      CitationStyle = "mla"
	StyleChicago  CitationStyle = "chicago"
	StyleBibTeX   CitationStyle = "bibtex"
	StyleCSLJSON  CitationStyle = "csl-json"
	StyleMarkdown CitationStyle = "markdown"
)

type Label struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type Highlight struct {
	ID         string          `json:"id"`
	BookmarkID string          `json:"bookmark_id"`
	Text       string          `json:"text"`
	Note       string          `json:"note,omitempty"`
	Color      string          `json:"color,omitempty"`
	CreatedAt  string          `json:"created_at,omitempty"`
	Location   json.RawMessage `json:"location,omitempty"`
}

type Bookmark struct {
	ID          string      `json:"id"`
	URL         string      `json:"url"`
	Title       string      `json:"title"`
	SiteName    string      `json:"site_name,omitempty"`
	Author      string      `json:"author,omitempty"`
	PublishedAt string      `json:"published_at,omitempty"`
	CreatedAt   string      `json:"created_at,omitempty"`
	UpdatedAt   string      `json:"updated_at,omitempty"`
	IsArchived  bool        `json:"is_archived"`
	IsFavorite  bool        `json:"is_favorite,omitempty"`
	Labels      []Label     `json:"labels,omitempty"`
	ContentText string      `json:"content_text,omitempty"`
	ContentHTML string      `json:"content_html,omitempty"`
	Highlights  []Highlight `json:"highlights,omitempty"`
}

type BookmarkSummary struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	IsArchived  bool     `json:"is_archived"`
	Labels      []string `json:"labels,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	PublishedAt string   `json:"published_at,omitempty"`
	Snippet     string   `json:"snippet,omitempty"`
}

type SearchOptions struct {
	Query     string       `json:"query,omitempty"`
	Title     string       `json:"title,omitempty"`
	Text      string       `json:"text,omitempty"`
	Labels    []string     `json:"labels,omitempty"`
	Archived  ArchivedMode `json:"archived,omitempty"`
	Favorites *bool        `json:"favorites,omitempty"`
	Sort      SortMode     `json:"sort,omitempty"`
	Limit     int          `json:"limit,omitempty"`
	Cursor    string       `json:"cursor,omitempty"`
}

type IncludeOptions struct {
	Content    bool `json:"content,omitempty"`
	Highlights bool `json:"highlights,omitempty"`
	Labels     bool `json:"labels,omitempty"`
}

type CitationMetadata struct {
	Title       string `json:"title,omitempty"`
	Author      string `json:"author,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	URL         string `json:"url,omitempty"`
	AccessedAt  string `json:"accessed_at"`
}

type Citation struct {
	Style    CitationStyle    `json:"style"`
	Text     string           `json:"text,omitempty"`
	CSLJSON  map[string]any   `json:"csl_json,omitempty"`
	BibTeX   string           `json:"bibtex,omitempty"`
	Metadata CitationMetadata `json:"metadata"`
}

type SearchResult struct {
	Items      []BookmarkSummary `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type LabelListResult struct {
	Labels     []Label `json:"labels"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

type HighlightListResult struct {
	Highlights []Highlight `json:"highlights"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

type ArchiveResult struct {
	ID         string `json:"id"`
	IsArchived bool   `json:"is_archived"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

type SetLabelsResult struct {
	ID     string   `json:"id"`
	Labels []string `json:"labels"`
}

type HTTPError struct {
	StatusCode int
	Endpoint   string
	RequestID  string
	Message    string
}

func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return "upstream request failed"
}
