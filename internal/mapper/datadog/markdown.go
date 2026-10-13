package datadog

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// Monitor messages are Markdown, they are rendered to HTML so the UI can show
// them formatted. Line breaks are kept, like in Datadog notifications.
var markdown = goldmark.New(
	goldmark.WithExtensions(
		extension.Linkify,
		extension.Strikethrough,
		extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
	),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// HTML rendered from monitor messages only keeps formatting and links to
// absolute URLs, raw HTML and images are removed
var markdownPolicy = newMarkdownPolicy()

func newMarkdownPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(
		"p", "br", "hr", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre",
		"strong", "em", "del", "code", "ul", "ol", "li",
		"table", "thead", "tbody", "tr", "th", "td",
	)
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("start").Matching(bluemonday.Integer).OnElements("ol")
	p.AllowAttrs("align").Matching(regexp.MustCompile(`^(left|center|right)$`)).OnElements("th", "td")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.RequireNoReferrerOnFullyQualifiedLinks(true)
	return p
}

// renderMarkdown renders a monitor message to sanitized HTML
func renderMarkdown(message string) string {
	var b bytes.Buffer
	// rendering only fails when writing fails, which can't happen with a buffer
	_ = markdown.Convert([]byte(message), &b)
	return strings.TrimSpace(markdownPolicy.Sanitize(b.String()))
}
