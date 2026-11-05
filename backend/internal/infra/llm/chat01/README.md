# Chat01 response text parser

This package receives **decoded content deltas**. HTTP/SSE framing, cumulative
snapshot reconciliation, heartbeats, request lifetime, downloading, persistence,
and UI conversion belong to the adapter or shared services. It uses only the Go
standard library and performs no I/O or tool execution.

`NewParser`, `Feed`, `Finish`, and `Blocks` share one interpretation for streaming
and final persistence. `Feed` returns at most one append delta per contiguous
block within that call, with local stable IDs. Blocks preserve every input byte,
including protocol controls, in source order. Consumers hide `control` / `meta`
as appropriate; they must not send server process blocks to a local tool queue.
Offsets refer to the original UTF-8 byte stream. `Finish` is idempotent.

The scanner gives Markdown fences, inline literals, escaped text, Python
strings/comments, and shell strings/heredocs precedence over protocol markers.
Explicit process markers permit Python/Shell/Browser classification. Python-like
fragments without a language marker require two adjacent code-shaped lines in
an explicit process region. Language and narrative tone do not determine kind.
Multiple answer sections remain ordered. No result, call association, success,
or failure is inferred from missing records or the end of the response.

Version 2 handles the observed Russian `Ответ:` answer marker and exact trailing
`## Answer` / duration lines after process output (including image-only replies).
Missing opening think markers have a deliberately narrow recovery: a short
standalone activity heading must be followed, within 4096 bytes, by an observed
completed-activity label joined directly to an explicit Answer control. Adjacent
bold spans alone are ordinary answer text. The already-emitted stage answer is
never delayed or retracted. At the limit, every wait branch falls back to source
text; unsupported or unmarked prose is not silently hidden. This heuristic does
not establish semantic certainty for arbitrary malformed website output.

The adapter separately formats process-only lines: duplicate `Thinking` chrome
is removed, joined headings receive paragraph breaks, and skill annotations are
shown as process information rather than fictitious incomplete tool executions.
Its per-line buffer is bounded to 512 bytes. Streaming, final persistence and
process IDs use the same presentation; commands and available tool data remain
on the shared server-tool trace path.

Only an unresolved short suffix remains in the pending buffer. Heading/code
lookahead is bounded to short lines, resource-read records to 4096 bytes, and
heredoc identifiers to 64 bytes. Excessive delimiter runs or delimiter whitespace
(512 bytes) degrade to literal line content. Components stream directly to one
block; `Oversized` is set after 64 KiB. A damaged oversized component can recover
at the next explicit section boundary. A complete component is not executable
UI: downstream code must validate its type and payload and provide a readable
fallback. A component's `Complete` flag says nothing about a tool's status.

`ResourcesFor` describes explicit Markdown links outside code in answer and
process blocks, preserving `OriginKind`, as well as known declarative component
fields. A resource does not change the classification of its surrounding text.
It retains occurrence order and source offsets, and does not fetch or deduplicate
anything. Normal links are sources, Markdown images are external images, and
unmapped sandbox references are unavailable. The captured Chat01
`/python-generations/` namespace provides explicit code-output provenance;
generic CDN hosts, filename extensions, and textual claims of generation do not.
Native image captures 01, 03 and 22 also establish the exact HTTPS namespace
`files.chat01.ai/user-<24 alphanumeric characters>__<UUID>/<UUID>.png` as generated
image provenance. Only this captured path shape is accepted; `/user-uploads/`,
other CDN paths, unexpected extensions, credentials, escaped paths, query strings
and fragments do not qualify. `IsGeneratedImageURL` exposes this same check to
the source reader. The public site's `runtime.appData.opGenui.componentResults`
structures provide distinct AsyncImage content/source URLs and Link/Cite fields;
these are read without examining runtime code or constructing missing resources.
Internal citation references remain readable source text and never become made-up
URLs. No salt keyword filter exists because these captures contain no verified
salt control block; answer text and code mentioning salt remain intact.

`ComponentInfoFor` and `RenderComponentFallback` provide a bounded read-only
fallback for the observed `map_widget_v2` shape: title, cards, real links, and
ordered internal references. The JSON decoder retains complete scalar fields
from truncated prefixes, with 64 KiB / 2048 nodes / 32 nesting levels / 16 cards
as limits. Missing or invalid fields are not repaired. Unknown components do not
expose raw JSON as answer content. `NormalizeReferences` is a presentation helper
for complete answer text; it maps citation references only through caller-supplied
verified HTTP(S) URLs, otherwise showing a readable missing-link placeholder.
These helpers do not mutate source blocks or their offsets. A caller normalizing
streamed presentation must retain incomplete citation boundaries between chunks.

Run `go test ./internal/infra/llm/chat01` from `backend`. Tests cover literal and
process boundaries, malformed/incomplete structures, bounded pending buffers,
UTF-8 byte fragmentation, stable append events, resource provenance, and exact
source conservation. If the ignored `.cache/chat01-probes` captures are present,
the same suite replays thirteen real responses at fixed byte widths and actual SSE
boundaries. Captures and provider account/CDN identifiers are not committed as
fixtures. The tests establish these structural cases, not semantic certainty for
unmarked natural language, arbitrary shell/Python syntax, or a full upstream UI
runtime.

The November 5 serial cases add three specific regressions. The interrupted
family-event capture keeps its last real stage answer and incomplete process;
it does not gain a fabricated final answer. Its clipped shell wrappers and the
cafe capture's `PY**activity heading**` joins recover at actual structural
boundaries. The cafe map's named/addressed points remain readable even without
coordinates. The SSE tutorial's clipped `cat ... <<` wrapper owns its complete
README heredoc, so embedded Answer/Thought/think and sandbox examples remain
literal code and do not become controls or artifacts. These checks join the
original ten captures; all thirteen replay with byte-fragmentation and actual
SSE boundaries when present locally.

Known `opGenui` AsyncImage/Link/Cite data also receive a read-only Markdown
fallback with external image preview and real source links. This rendering
neither executes runtime code/actions nor fetches files. Map fallback preserves
explicit name/address points and narrative as text; no coordinates are inferred.

The November 5 user-prompt captures additionally confirm `charts_widget_v2` line,
bar and pie payloads. Only validated labels and finite numeric data map to the
existing `deeix-ui` `chart@1` renderer (up to 256 rows / 8 series); upstream runtime,
actions and formulas are never forwarded. The existing renderer provides legends,
side-panel viewing and copy-as-table. Incomplete readable chart rows retain a
table fallback. This does not enable DEEIX's prompt injection for Chat01.

The same API captures contain literal `[提交猜测]` and `Current value unavailable`
strings without game state or callbacks. They cannot be reconstructed into the
website games; ordinary answer text containing those strings is preserved, not
silently deleted or represented as functioning controls. Map payloads without
coordinates remain named/addressed points. Source information may be copied;
interactive controls are not themselves meaningful clipboard content.
