package datadog

import "testing"

func TestRenderMarkdown(t *testing.T) {
	type testCaseT struct {
		name     string
		message  string
		expected string
	}

	testCases := []testCaseT{
		{
			name:     "empty message",
			message:  "",
			expected: "",
		},
		{
			name:     "plain text",
			message:  "CPU usage is high {{value}} @slack-ops",
			expected: "<p>CPU usage is high {{value}} @slack-ops</p>",
		},
		{
			name:     "line breaks are kept",
			message:  "a\nb\n\nc",
			expected: "<p>a<br>\nb</p>\n<p>c</p>",
		},
		{
			name:     "special characters are escaped",
			message:  `a < b & c > d "e"`,
			expected: "<p>a &lt; b &amp; c &gt; d &#34;e&#34;</p>",
		},
		{
			name:     "formatting",
			message:  "## Title\n**bold** _italic_ ~~deleted~~ `code`\n\n---",
			expected: "<h2>Title</h2>\n<p><strong>bold</strong> <em>italic</em> <del>deleted</del> <code>code</code></p>\n<hr>",
		},
		{
			name:     "lists",
			message:  "- a\n- b\n\n3. c\n4. d",
			expected: "<ul>\n<li>a</li>\n<li>b</li>\n</ul>\n<ol start=\"3\">\n<li>c</li>\n<li>d</li>\n</ol>",
		},
		{
			name:     "code blocks",
			message:  "1. a\n   ```sh\n   uptime\n   ```",
			expected: "<ol>\n<li>a\n<pre><code>uptime\n</code></pre>\n</li>\n</ol>",
		},
		{
			name:     "blockquotes",
			message:  "> a\n> b",
			expected: "<blockquote>\n<p>a<br>\nb</p>\n</blockquote>",
		},
		{
			name:     "tables",
			message:  "| a | b |\n|:--|--:|\n| 1 | 2 |",
			expected: "<table>\n<thead>\n<tr>\n<th align=\"left\">a</th>\n<th align=\"right\">b</th>\n</tr>\n</thead>\n<tbody>\n<tr>\n<td align=\"left\">1</td>\n<td align=\"right\">2</td>\n</tr>\n</tbody>\n</table>",
		},
		{
			name:     "links",
			message:  "[runbook](https://example.com/runbook) https://example.com/a [mail](mailto:sre@example.com)",
			expected: `<p><a href="https://example.com/runbook" rel="noreferrer noopener" target="_blank">runbook</a> <a href="https://example.com/a" rel="noreferrer noopener" target="_blank">https://example.com/a</a> <a href="mailto:sre@example.com">mail</a></p>`,
		},
		{
			name:     "links to unsafe or relative URLs are removed",
			message:  "[a](javascript:alert(1)) [b](/relative) [c](data:text/html,c)",
			expected: "<p>a b c</p>",
		},
		{
			name:     "raw HTML is removed",
			message:  "<script>alert(1)</script>\n\n<b onclick=\"alert(1)\">bold</b> <a href=\"https://example.com\" onclick=\"alert(1)\">link</a>",
			expected: "<p>bold link</p>",
		},
		{
			name:     "images are removed",
			message:  "graph: ![graph](https://example.com/graph.png)",
			expected: "<p>graph: </p>",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderMarkdown(tc.message); got != tc.expected {
				t.Errorf("renderMarkdown(%q) returned %q, expected %q", tc.message, got, tc.expected)
			}
		})
	}
}
