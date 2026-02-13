package citation

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/akrisanov/readeck-mcp/internal/readeck"
)

func Generate(bookmark readeck.Bookmark, highlight *readeck.Highlight, quote string, style readeck.CitationStyle, accessedAt time.Time) readeck.Citation {
	if style == "" {
		style = readeck.StyleMarkdown
	}
	if accessedAt.IsZero() {
		accessedAt = time.Now().UTC()
	}

	metadata := readeck.CitationMetadata{
		Title:       bookmark.Title,
		Author:      bookmark.Author,
		SiteName:    bookmark.SiteName,
		PublishedAt: bookmark.PublishedAt,
		URL:         bookmark.URL,
		AccessedAt:  accessedAt.Format(time.RFC3339),
	}

	citation := readeck.Citation{
		Style:    style,
		Metadata: metadata,
	}

	switch style {
	case readeck.StyleCSLJSON:
		csl := toCSLJSON(bookmark, accessedAt)
		citation.CSLJSON = csl
		citation.Text = toJSONString(csl)
	case readeck.StyleBibTeX:
		bib := toBibTeX(bookmark, accessedAt)
		citation.BibTeX = bib
		citation.Text = bib
	case readeck.StyleAPA:
		citation.Text = formatAPA(bookmark, accessedAt)
	case readeck.StyleMLA:
		citation.Text = formatMLA(bookmark, accessedAt)
	case readeck.StyleChicago:
		citation.Text = formatChicago(bookmark, accessedAt)
	default:
		citation.Style = readeck.StyleMarkdown
		citation.Text = formatMarkdown(bookmark, highlight, quote, accessedAt)
	}

	return citation
}

func formatMarkdown(bookmark readeck.Bookmark, highlight *readeck.Highlight, quote string, accessedAt time.Time) string {
	var b strings.Builder
	author := authorOrSite(bookmark)
	date := publishedOrND(bookmark)
	if author != "" {
		b.WriteString(author)
		b.WriteString(". ")
	}
	b.WriteString("[")
	b.WriteString(nonEmpty(bookmark.Title, bookmark.URL))
	b.WriteString("](")
	b.WriteString(bookmark.URL)
	b.WriteString(")")
	if date != "" {
		b.WriteString(" (")
		b.WriteString(date)
		b.WriteString(")")
	}
	b.WriteString(". Accessed ")
	b.WriteString(accessedAt.Format("2006-01-02"))
	b.WriteString(".")
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString(bookmark.URL)
	b.WriteByte('\n')

	selectedQuote := strings.TrimSpace(quote)
	if selectedQuote == "" && highlight != nil {
		selectedQuote = strings.TrimSpace(highlight.Text)
	}
	if selectedQuote != "" {
		b.WriteString("\n> ")
		b.WriteString(selectedQuote)
		b.WriteByte('\n')
	}

	return b.String()
}

func formatAPA(bookmark readeck.Bookmark, accessedAt time.Time) string {
	author := authorOrSite(bookmark)
	date := publishedOrND(bookmark)
	title := nonEmpty(bookmark.Title, bookmark.URL)
	if author == "" {
		return fmt.Sprintf("%s. (%s). Retrieved %s, from %s", title, date, accessedAt.Format("2006-01-02"), bookmark.URL)
	}
	return fmt.Sprintf("%s. (%s). %s. %s", author, date, title, bookmark.URL)
}

func formatMLA(bookmark readeck.Bookmark, accessedAt time.Time) string {
	author := authorOrSite(bookmark)
	title := nonEmpty(bookmark.Title, bookmark.URL)
	site := nonEmpty(bookmark.SiteName, "")
	date := nonEmpty(bookmark.PublishedAt, "n.d.")
	if author == "" {
		return fmt.Sprintf("\"%s.\" %s, %s, %s. Accessed %s.", title, site, date, bookmark.URL, accessedAt.Format("2 Jan 2006"))
	}
	return fmt.Sprintf("%s. \"%s.\" %s, %s, %s. Accessed %s.", author, title, site, date, bookmark.URL, accessedAt.Format("2 Jan 2006"))
}

func formatChicago(bookmark readeck.Bookmark, accessedAt time.Time) string {
	author := authorOrSite(bookmark)
	title := nonEmpty(bookmark.Title, bookmark.URL)
	date := nonEmpty(bookmark.PublishedAt, "n.d.")
	if author == "" {
		return fmt.Sprintf("\"%s.\" Accessed %s. %s.", title, accessedAt.Format("January 2, 2006"), bookmark.URL)
	}
	return fmt.Sprintf("%s. \"%s.\" %s. Accessed %s. %s.", author, title, date, accessedAt.Format("January 2, 2006"), bookmark.URL)
}

func toCSLJSON(bookmark readeck.Bookmark, accessedAt time.Time) map[string]any {
	result := map[string]any{
		"type":     "webpage",
		"title":    nonEmpty(bookmark.Title, bookmark.URL),
		"URL":      bookmark.URL,
		"accessed": dateParts(accessedAt),
	}
	if bookmark.Author != "" {
		result["author"] = []map[string]string{parseAuthor(bookmark.Author)}
	} else if bookmark.SiteName != "" {
		result["author"] = []map[string]string{{"literal": bookmark.SiteName}}
	}
	if t, ok := parseDate(bookmark.PublishedAt); ok {
		result["issued"] = dateParts(t)
	}
	return result
}

func toBibTeX(bookmark readeck.Bookmark, accessedAt time.Time) string {
	year := ""
	if t, ok := parseDate(bookmark.PublishedAt); ok {
		year = t.Format("2006")
	}
	if year == "" {
		year = "n.d."
	}
	keyBase := strings.ToLower(strings.ReplaceAll(nonEmpty(bookmark.SiteName, "source"), " ", ""))
	h := sha1.Sum([]byte(bookmark.URL))
	key := fmt.Sprintf("%s%s%s", keyBase, year, hex.EncodeToString(h[:])[:6])

	var b strings.Builder
	b.WriteString("@online{")
	b.WriteString(key)
	b.WriteString(",\n")
	b.WriteString("  title = {")
	b.WriteString(escapeBib(nonEmpty(bookmark.Title, bookmark.URL)))
	b.WriteString("},\n")
	if bookmark.Author != "" {
		b.WriteString("  author = {")
		b.WriteString(escapeBib(bookmark.Author))
		b.WriteString("},\n")
	}
	if year != "n.d." {
		b.WriteString("  year = {")
		b.WriteString(year)
		b.WriteString("},\n")
	}
	b.WriteString("  url = {")
	b.WriteString(escapeBib(bookmark.URL))
	b.WriteString("},\n")
	b.WriteString("  urldate = {")
	b.WriteString(accessedAt.Format("2006-01-02"))
	b.WriteString("},\n")
	b.WriteString("}\n")
	return b.String()
}

func parseAuthor(author string) map[string]string {
	author = strings.TrimSpace(author)
	parts := strings.Fields(author)
	if len(parts) == 0 {
		return map[string]string{"literal": ""}
	}
	if len(parts) == 1 {
		return map[string]string{"literal": parts[0]}
	}
	given := strings.Join(parts[:len(parts)-1], " ")
	family := parts[len(parts)-1]
	return map[string]string{"given": given, "family": family}
}

func parseDate(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{time.RFC3339, "2006-01-02", time.RFC1123, time.RFC1123Z}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func dateParts(t time.Time) map[string]any {
	ut := t.UTC()
	return map[string]any{"date-parts": [][]int{{ut.Year(), int(ut.Month()), ut.Day()}}}
}

func toJSONString(v any) string {
	b, err := jsonMarshalIndent(v)
	if err != nil {
		return "{}"
	}
	return b
}

func jsonMarshalIndent(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func authorOrSite(bookmark readeck.Bookmark) string {
	if strings.TrimSpace(bookmark.Author) != "" {
		return strings.TrimSpace(bookmark.Author)
	}
	return strings.TrimSpace(bookmark.SiteName)
}

func publishedOrND(bookmark readeck.Bookmark) string {
	if strings.TrimSpace(bookmark.PublishedAt) != "" {
		return strings.TrimSpace(bookmark.PublishedAt)
	}
	return "n.d."
}

func nonEmpty(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return strings.TrimSpace(primary)
	}
	return strings.TrimSpace(fallback)
}

func escapeBib(v string) string {
	v = strings.ReplaceAll(v, "{", "\\{")
	v = strings.ReplaceAll(v, "}", "\\}")
	return v
}
