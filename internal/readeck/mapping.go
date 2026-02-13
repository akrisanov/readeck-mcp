package readeck

import (
	"encoding/json"
	"fmt"
	"strings"
)

func mapBookmark(obj map[string]any) Bookmark {
	labels := extractLabels(obj, "labels", "tags")
	highlights := extractHighlights(obj, "highlights")

	bm := Bookmark{
		ID:          firstNonEmptyString(obj, "id", "uid"),
		URL:         firstNonEmptyString(obj, "url", "link"),
		Title:       firstNonEmptyString(obj, "title"),
		SiteName:    firstNonEmptyString(obj, "site_name", "site", "domain"),
		Author:      firstNonEmptyString(obj, "author", "byline"),
		PublishedAt: normalizeTimeField(obj, "published_at", "published"),
		CreatedAt:   normalizeTimeField(obj, "created_at", "created"),
		UpdatedAt:   normalizeTimeField(obj, "updated_at", "updated"),
		IsArchived:  firstBool(obj, "is_archived", "archived"),
		IsFavorite:  firstBool(obj, "is_favorite", "favorite"),
		Labels:      labels,
		ContentText: firstNonEmptyString(obj, "content_text", "text", "content"),
		ContentHTML: firstNonEmptyString(obj, "content_html", "html"),
		Highlights:  highlights,
	}
	if bm.Title == "" && bm.URL != "" {
		bm.Title = bm.URL
	}
	return bm
}

func mapLabel(obj map[string]any) Label {
	name := firstNonEmptyString(obj, "name", "label")
	if name == "" {
		if s, ok := objValue(obj, "title").(string); ok {
			name = s
		}
	}
	return Label{
		ID:    firstNonEmptyString(obj, "id", "uid"),
		Name:  name,
		Color: firstNonEmptyString(obj, "color", "hex"),
	}
}

func mapHighlight(obj map[string]any) Highlight {
	loc := json.RawMessage(nil)
	if raw, ok := objValue(obj, "location").(map[string]any); ok {
		if b, err := json.Marshal(raw); err == nil {
			loc = b
		}
	}
	if raw, ok := objValue(obj, "location").([]any); ok {
		if b, err := json.Marshal(raw); err == nil {
			loc = b
		}
	}
	return Highlight{
		ID:         firstNonEmptyString(obj, "id", "uid"),
		BookmarkID: firstNonEmptyString(obj, "bookmark_id", "article_id"),
		Text:       firstNonEmptyString(obj, "text", "quote"),
		Note:       firstNonEmptyString(obj, "note", "comment"),
		Color:      firstNonEmptyString(obj, "color"),
		CreatedAt:  normalizeTimeField(obj, "created_at", "created"),
		Location:   loc,
	}
}

func extractLabels(obj map[string]any, keys ...string) []Label {
	for _, key := range keys {
		arr := extractArray(obj, key)
		if len(arr) == 0 {
			continue
		}
		labels := make([]Label, 0, len(arr))
		for _, item := range arr {
			switch v := item.(type) {
			case map[string]any:
				label := mapLabel(v)
				if strings.TrimSpace(label.Name) == "" {
					continue
				}
				labels = append(labels, label)
			case string:
				name := strings.TrimSpace(v)
				if name == "" {
					continue
				}
				labels = append(labels, Label{Name: name})
			}
		}
		if len(labels) > 0 {
			return labels
		}
	}
	return nil
}

func extractHighlights(obj map[string]any, key string) []Highlight {
	arr := extractArrayMap(obj, key)
	if len(arr) == 0 {
		return nil
	}
	out := make([]Highlight, 0, len(arr))
	for _, item := range arr {
		h := mapHighlight(item)
		if h.ID == "" {
			continue
		}
		out = append(out, h)
	}
	return out
}

func extractArray(obj map[string]any, key string) []any {
	if obj == nil {
		return nil
	}
	raw, ok := obj[key]
	if !ok || raw == nil {
		return nil
	}
	arr, ok := raw.([]any)
	if ok {
		return arr
	}
	if typed, ok := raw.([]map[string]any); ok {
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	}
	return nil
}

func extractArrayMap(obj map[string]any, key string) []map[string]any {
	arr := extractArray(obj, key)
	if len(arr) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func firstNonEmptyString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return v
			}
		case float64:
			return fmt.Sprintf("%.0f", v)
		case int:
			return fmt.Sprintf("%d", v)
		}
	}
	return ""
}

func normalizeTimeField(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if raw, ok := obj[key]; ok && raw != nil {
			if s, ok := raw.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func firstBool(obj map[string]any, keys ...string) bool {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case bool:
			return v
		case string:
			lv := strings.ToLower(strings.TrimSpace(v))
			if lv == "true" || lv == "1" || lv == "yes" {
				return true
			}
			if lv == "false" || lv == "0" || lv == "no" {
				return false
			}
		case float64:
			return v != 0
		}
	}
	return false
}

func objValue(obj map[string]any, key string) any {
	if obj == nil {
		return nil
	}
	return obj[key]
}
