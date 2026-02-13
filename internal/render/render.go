package render

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/akrisanov/readeck-mcp/internal/readeck"
)

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)
var wsRe = regexp.MustCompile(`\s+`)

func BookmarkContentMarkdown(bookmark readeck.Bookmark, includeHighlights bool) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeYAML(&b, "title", bookmark.Title)
	writeYAML(&b, "url", bookmark.URL)
	writeYAML(&b, "author", bookmark.Author)
	writeYAML(&b, "site_name", bookmark.SiteName)
	writeYAML(&b, "published_at", bookmark.PublishedAt)
	writeYAML(&b, "created_at", bookmark.CreatedAt)
	writeYAML(&b, "updated_at", bookmark.UpdatedAt)
	writeYAML(&b, "readeck_id", bookmark.ID)
	writeYAMLBool(&b, "archived", bookmark.IsArchived)

	labels := make([]string, 0, len(bookmark.Labels))
	for _, l := range bookmark.Labels {
		if strings.TrimSpace(l.Name) != "" {
			labels = append(labels, l.Name)
		}
	}
	b.WriteString("labels:\n")
	if len(labels) == 0 {
		b.WriteString("  []\n")
	} else {
		for _, label := range labels {
			b.WriteString("  - ")
			b.WriteString(quoteYAML(label))
			b.WriteByte('\n')
		}
	}
	b.WriteString("---\n\n")

	text := BookmarkContentText(bookmark)
	if text == "" {
		text = "(content unavailable)"
	}
	b.WriteString(text)
	b.WriteByte('\n')

	if includeHighlights && len(bookmark.Highlights) > 0 {
		b.WriteString("\n## Highlights\n\n")
		b.WriteString(HighlightsMarkdown(bookmark.Highlights))
	}

	return b.String()
}

func BookmarkContentText(bookmark readeck.Bookmark) string {
	if strings.TrimSpace(bookmark.ContentText) != "" {
		return normalizeWhitespace(bookmark.ContentText)
	}
	if strings.TrimSpace(bookmark.ContentHTML) != "" {
		return htmlToText(bookmark.ContentHTML)
	}
	return ""
}

func HighlightsMarkdown(highlights []readeck.Highlight) string {
	if len(highlights) == 0 {
		return ""
	}
	var b strings.Builder
	wrote := false
	for _, h := range highlights {
		text := strings.TrimSpace(h.Text)
		if text == "" {
			continue
		}
		if wrote {
			b.WriteByte('\n')
		}
		b.WriteString("> ")
		b.WriteString(text)
		b.WriteByte('\n')
		if note := strings.TrimSpace(h.Note); note != "" {
			b.WriteString("- Note: ")
			b.WriteString(note)
			b.WriteByte('\n')
		}
		b.WriteString("- Highlight ID: `")
		b.WriteString(h.ID)
		b.WriteString("`\n")
		wrote = true
	}
	return b.String()
}

func writeYAML(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(quoteYAML(value))
	b.WriteByte('\n')
}

func writeYAMLBool(b *strings.Builder, key string, value bool) {
	b.WriteString(key)
	b.WriteString(": ")
	if value {
		b.WriteString("true\n")
		return
	}
	b.WriteString("false\n")
}

func quoteYAML(v string) string {
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.TrimSpace(v)
	return fmt.Sprintf("%q", v)
}

func htmlToText(input string) string {
	text := tagRe.ReplaceAllString(input, " ")
	text = html.UnescapeString(text)
	return normalizeWhitespace(text)
}

func normalizeWhitespace(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = wsRe.ReplaceAllString(strings.TrimSpace(lines[i]), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
