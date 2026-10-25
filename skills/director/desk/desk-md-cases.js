// Cases for the Desk page's Markdown-subset renderer. Run by scripts/verify-desk-page.sh with the renderer
// block from desk.html loaded first (mdRender, mdInline, mdEsc) and a print(line) function.
var failed = 0, passed = 0;
function check(name, ok, detail) {
  if (ok) { passed++; print("PASS " + name); }
  else { failed++; print("FAIL " + name + (detail ? " -- " + detail : "")); }
}
function eq(name, got, want) { check(name, got === want, "got " + JSON.stringify(got) + ", want " + JSON.stringify(want)); }

var ALLOWED = ["p", "ul", "ol", "li", "strong", "em", "code"];
function tags(html) { var out = [], re = /<\/?([a-zA-Z][a-zA-Z0-9]*)/g, m; while ((m = re.exec(html))) out.push(m[1].toLowerCase()); return out; }
// Every "<" in the output opens a tag the renderer wrote, because all input text is escaped. Each such tag
// must be a bare allowed tag, or <ol start="digits">: no attribute can come from card text.
function wellFormedTags(html) {
  var all = html.match(/<[^>]*>?/g) || [];
  return all.every(function (t) { return /^<\/?(p|ul|ol|li|strong|em|code)>$/.test(t) || /^<ol start="\d+">$/.test(t); });
}
function onlyAllowed(html) { return tags(html).every(function (t) { return ALLOWED.indexOf(t) >= 0; }); }

// each construct
eq("paragraphs split on a blank line", mdRender("one\n\ntwo"), "<p>one</p><p>two</p>");
eq("lines of one paragraph stay together", mdRender("one\ntwo"), "<p>one\ntwo</p>");
eq("**bold**", mdRender("a **b** c"), "<p>a <strong>b</strong> c</p>");
eq("bold that ends in a full stop (a card label)", mdRender("**IN SHORT.** x"), "<p><strong>IN SHORT.</strong> x</p>");
eq("*italic*", mdRender("a *b* c"), "<p>a <em>b</em> c</p>");
eq("`inline code`", mdRender("run `ls -la` now"), "<p>run <code>ls -la</code> now</p>");
eq("code is not emphasised inside", mdRender("`**x** *y*`"), "<p><code>**x** *y*</code></p>");
eq("code content is escaped", mdRender("`<b>`"), "<p><code>&lt;b&gt;</code></p>");
eq("bullets", mdRender("- a\n- b"), "<ul><li>a</li><li>b</li></ul>");
eq("numbered list", mdRender("1. a\n2. b"), "<ol><li>a</li><li>b</li></ol>");
eq("a numbered list that does not start at 1 keeps its number", mdRender("3. a\n4. b"), "<ol start=\"3\"><li>a</li><li>b</li></ol>");
eq("a label line then bullets with no blank line between (a card's Terms)", mdRender("**Terms.**\n- *term*: meaning\n- other: meaning"),
   "<p><strong>Terms.</strong></p><ul><li><em>term</em>: meaning</li><li>other: meaning</li></ul>");
eq("a bullet list then a paragraph", mdRender("- a\n\nafter"), "<ul><li>a</li></ul><p>after</p>");
eq("an indented line continues the item above it", mdRender("- a\n  more\n- b"), "<ul><li>a\nmore</li><li>b</li></ul>");
eq("a bullet list then a numbered list are two lists", mdRender("- a\n1. b"), "<ul><li>a</li></ul><ol><li>b</li></ol>");
eq("CRLF line ends read as LF", mdRender("a\r\n\r\nb"), "<p>a</p><p>b</p>");
eq("empty text renders nothing", mdRender(""), "");
eq("null renders nothing", mdRender(null), "");
eq("undefined renders nothing", mdRender(undefined), "");
eq("a number is text", mdRender(7), "<p>7</p>");

// things that look like markup but are not
eq("an unclosed ** stays literal", mdRender("a **b"), "<p>a **b</p>");
eq("2 * 3 * 4 is arithmetic, not emphasis", mdRender("2 * 3 * 4"), "<p>2 * 3 * 4</p>");
eq("a*b*c inside a word is not emphasis", mdRender("a*b*c"), "<p>a*b*c</p>");
eq("a hyphen inside a line is not a bullet", mdRender("well-known"), "<p>well-known</p>");
eq("a number with no list marker is not a list", mdRender("2026 was"), "<p>2026 was</p>");

// no raw HTML, ever: it is shown as text
var xss = [
  "<script>alert(1)</script>",
  "<img src=x onerror=alert(1)>",
  "<a href=\"javascript:alert(1)\">x</a>",
  "[click](javascript:alert(1))",
  "[click](https://example.test)",
  "<svg onload=alert(1)>",
  "<iframe src=//evil.test></iframe>",
  "\"><script>alert(1)</script>",
  "**<script>alert(1)</script>**",
  "*<img src=x onerror=alert(1)>*",
  "- <img src=x onerror=alert(1)>",
  "1. <script>alert(1)</script>",
  "`<script>alert(1)</script>`",
  "<style>body{display:none}</style>",
  "&lt;script&gt;alert(1)&lt;/script&gt;",
  "<scr<script>ipt>alert(1)</scr</script>ipt>"
];
xss.forEach(function (p, i) {
  var h = mdRender(p);
  check("payload " + (i + 1) + " renders as text: only the renderer's own tags appear", onlyAllowed(h), h);
  check("payload " + (i + 1) + " writes no tag but a bare one (or ol start=N)", wellFormedTags(h), h);
  check("payload " + (i + 1) + " has no raw angle bracket from the input", !/<(script|img|svg|iframe|style|a)[\s>\/]/i.test(h), h);
});
// generated inputs: any element name the renderer does not write, with attributes, open and close; and "<"
// followed by every printable ASCII character. Whatever comes out must be bare allowed tags only.
var names = ["b", "u", "i", "s", "div", "span", "form", "input", "button", "object", "embed", "math", "details",
  "video", "audio", "source", "base", "meta", "link", "textarea", "select", "option", "table", "td", "h1", "pre",
  "br", "hr", "a", "img", "svg", "script", "style", "iframe", "body", "html", "marquee", "template", "noscript",
  "p", "ul", "ol", "li", "strong", "em", "code", "P", "STRONG", "Em", "CODE", "UL", "LI", "OL", "u onmouseover=\"alert(1)\"", "isindex", "keygen", "plaintext"];
names.forEach(function (n) {
  var inputs = ["<" + n + " x=\"1\" onclick=\"y\">z</" + n + ">", "<" + n + ">", "</" + n + ">", "<" + n + "/>", "**<" + n + " onload=y>**", "- <" + n + " onload=y>"];
  inputs.forEach(function (inp) {
    var h = mdRender(inp);
    check("generated " + JSON.stringify(inp) + " writes bare allowed tags only", wellFormedTags(h) && onlyAllowed(h), h);
  });
});
for (var cc = 32; cc < 127; cc++) {
  var ch = String.fromCharCode(cc);
  [ "<" + ch + "x onerror=1>", "a <" + ch + " b", "<" + ch + ">" ].forEach(function (inp) {
    var h = mdRender(inp);
    check("generated " + JSON.stringify(inp) + " writes bare allowed tags only", wellFormedTags(h) && onlyAllowed(h), h);
  });
}

// the oracles themselves must reject what a leaking renderer would write
check("the oracle rejects a raw tag with an attribute", !wellFormedTags("<p><u onmouseover=\"alert(1)\">x</u></p>"));
check("the oracle rejects a raw <b>", !wellFormedTags("<p><b>x</b></p>"));
check("the oracle rejects <br>", !wellFormedTags("<p>a<br>b</p>") && !onlyAllowed("<p>a<br>b</p>"));
check("the oracle accepts the renderer's own tags", wellFormedTags("<ol start=\"3\"><li><strong>a</strong><em>b</em><code>c</code></li></ol>"));

// crossed markers do not write mis-nested tags
function nested(html) {
  var st = [], re = /<(\/?)(strong|em|code|p|ul|ol|li)[^>]*>/g, m;
  while ((m = re.exec(html))) {
    if (!m[1]) st.push(m[2]);
    else if (st.pop() !== m[2]) return false;
  }
  return st.length === 0;
}
["**a *b** c*", "*a **b* c**", "***a** b*", "*a **b** c*", "**a *b* c**", "***a***"].forEach(function (inp) {
  var h = mdRender(inp);
  check("emphasis nests properly for " + JSON.stringify(inp), nested(h), h);
});
check("the nesting oracle rejects crossed tags", !nested("<p><strong>a <em>b</strong> c</em></p>"));

// a long line of unclosed markers is bounded
var t0 = Date.now(), big = new Array(50001).join("**a ");
mdRender(big);
check("200k characters of unclosed ** render in under 1500 ms", Date.now() - t0 < 1500, (Date.now() - t0) + " ms");

check("the script payload is visible as escaped text", mdRender("<script>alert(1)</script>").indexOf("&lt;script&gt;alert(1)&lt;/script&gt;") >= 0);
check("an onerror attribute is visible as text", mdRender("<img src=x onerror=alert(1)>").indexOf("onerror=alert(1)") >= 0 && mdRender("<img src=x onerror=alert(1)>").indexOf("<img") < 0);
check("a javascript: link is text, never a link", mdRender("[click](javascript:alert(1))").indexOf("<a") < 0 && mdRender("[click](javascript:alert(1))").indexOf("javascript:alert(1)") >= 0);
check("an already-escaped entity is escaped again, so it shows as typed", mdRender("&lt;").indexOf("&amp;lt;") >= 0);
check("quotes cannot end an attribute", mdRender("\"'").indexOf("\"") < 0 && mdRender("\"'").indexOf("'") < 0);

// the card body of a real shape
var card = "**IN SHORT.** the process\n\n**The problem.** each comment needs a read.\n\n**Terms.**\n- *seat*: one agent\n- *card*: this page\n\n**Options.**\n1. one\n2. two";
var h = mdRender(card);
check("a whole card keeps its sections apart", (h.match(/<p>/g) || []).length === 4 && h.indexOf("<ul>") > 0 && h.indexOf("<ol>") > 0, h);
check("a whole card uses only the renderer's tags", onlyAllowed(h), h);

print((failed ? "FAIL " : "PASS ") + "desk-md: " + passed + " passed, " + failed + " failed");
