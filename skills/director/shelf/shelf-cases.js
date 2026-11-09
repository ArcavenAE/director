// Cases for the Reply Shelf's logic block (/*shelf:start*/ to /*shelf:end*/ in shelf.html). The runner
// (verify-shelf-page.sh) puts the block first, then this file, and supplies print(). All ids and times
// here are placeholders.
var passN = 0, failN = 0;
function eq(name, got, want) {
  var g = JSON.stringify(got), w = JSON.stringify(want);
  if (g === w) { print("PASS " + name); passN++; } else { print("FAIL " + name + " -- got " + g + " want " + w); failN++; }
}
var DAY = 86400000;
var NOW = Date.parse("2030-01-20T12:00:00Z");
var iso = function (ms) { return new Date(ms).toISOString().replace(/\.\d+Z$/, "Z"); };

eq("a new row views as new", shelfView({ state: "new" }, NOW), "new");
eq("a read row views as read", shelfView({ state: "read" }, NOW), "read");
eq("a row cleared 1 day ago views as cleared", shelfView({ state: "cleared", cleared_at: iso(NOW - DAY) }, NOW), "cleared");
eq("a row cleared 13 days ago views as cleared", shelfView({ state: "cleared", cleared_at: iso(NOW - 13 * DAY) }, NOW), "cleared");
eq("a row cleared exactly 14 days ago is gone", shelfView({ state: "cleared", cleared_at: iso(NOW - 14 * DAY) }, NOW), "gone");
eq("a row cleared 15 days ago is gone", shelfView({ state: "cleared", cleared_at: iso(NOW - 15 * DAY) }, NOW), "gone");
eq("a cleared row with no time is gone, never shown for ever", shelfView({ state: "cleared" }, NOW), "gone");
eq("a cleared row with an unreadable time is gone", shelfView({ state: "cleared", cleared_at: "soon" }, NOW), "gone");
eq("an unknown state is gone", shelfView({ state: "weird" }, NOW), "gone");
eq("a row with no state is gone", shelfView({}, NOW), "gone");

eq("opening a new row marks it read", shelfOpenPatch({ state: "new" }, "2030-01-20T12:00:00Z"), { state: "read", read_at: "2030-01-20T12:00:00Z" });
eq("opening a read row writes nothing", shelfOpenPatch({ state: "read" }, "2030-01-20T12:00:00Z"), null);
eq("opening a cleared row writes nothing", shelfOpenPatch({ state: "cleared" }, "2030-01-20T12:00:00Z"), null);
eq("clearing writes the state and the time", shelfClearPatch("2030-01-20T12:00:00Z"), { state: "cleared", cleared_at: "2030-01-20T12:00:00Z" });
eq("recovering returns the row to read and empties cleared_at", shelfRecoverPatch(), { state: "read", cleared_at: "" });
eq("ask for more writes the text and the time", shelfMorePatch("  one more line please ", "2030-01-20T12:00:00Z"), { more: "one more line please", more_at: "2030-01-20T12:00:00Z" });
eq("ask for more with only spaces writes nothing", shelfMorePatch("   \n ", "2030-01-20T12:00:00Z"), null);
eq("ask for more with nothing writes nothing", shelfMorePatch(null, "2030-01-20T12:00:00Z"), null);
eq("ask for more over 2000 characters is cut at 2000", shelfMorePatch(new Array(2101).join("x"), "2030-01-20T12:00:00Z").more.length, 2000);

var allowed = ["state", "read_at", "cleared_at", "more", "more_at"];
var patches = [shelfOpenPatch({ state: "new" }, "t"), shelfClearPatch("t"), shelfRecoverPatch(), shelfMorePatch("x", "t")];
eq("every patch writes only the operator's own fields", patches.every(function (p) { return Object.keys(p).every(function (k) { return allowed.indexOf(k) >= 0; }); }), true);
eq("no patch touches the summary, source, ask or who asked", patches.every(function (p) {
  return ["summary", "source", "sha256", "ask", "asked_by", "asked_of", "created_at"].every(function (k) { return !(k in p); });
}), true);

var rows = [{ id: "r-20300101-1", created_at: "2030-01-01T00:00:00Z" }, { id: "r-20300103-1", created_at: "2030-01-03T00:00:00Z" }, { id: "r-20300102-1", created_at: "2030-01-02T00:00:00Z" }, { id: "r-bad", created_at: "x" }];
eq("rows sort newest first, unreadable times last", shelfSort(rows).map(function (r) { return r.id; }), ["r-20300103-1", "r-20300102-1", "r-20300101-1", "r-bad"]);
eq("recover days left counts down from 14", shelfDaysLeft({ state: "cleared", cleared_at: iso(NOW - 4 * DAY) }, NOW), 10);
eq("recover days left rounds up so the last day reads 1", shelfDaysLeft({ state: "cleared", cleared_at: iso(NOW - 13.5 * DAY) }, NOW), 1);

print((failN === 0 ? "PASS" : "FAIL") + " shelf: " + passN + " passed, " + failN + " failed");
