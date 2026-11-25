# Plan: edit deployed pages and create new ones in the browser (WYSIWYG editor)

Status: research and plan, nothing built yet. Date: 2026-11-24.

The code was read in full: `src/*.go`, `src/static/*`, `deploy/Caddyfile` and `docs/DECISION-LOG.md`. Library facts
marked **(unverified)** come from memory or from a single secondary source and need checking before you rely on them.
Sizes are approximate.

## TL;DR

- **Most rich-text editors cannot do this job.** TipTap/ProseMirror, Lexical, Quill, Trix and Editor.js all convert
  content to their own schema. Loading an AI-generated page into one of them drops its `<head>`, `<style>`, `<script>`
  and any markup they don't recognise. CKEditor 5 and TinyMCE have "full page" modes, but they are GPL or premium and
  still reformat the HTML.
- **The only lossless option is to edit the source.** Use a code editor with a live preview and add a few targeted
  helpers: set the favicon, start a new page from a template. This is how Neocities, Tiiny.host (Code Editor mode) and
  GitHub's web editor work.
- **Smallest useful version:** a `<textarea>`, a sandboxed live-preview iframe, a "Set favicon" button and a "New page"
  button. Add CodeMirror 6 syntax highlighting next, as a vendored file. Click-to-edit text (`designMode`) comes later,
  and only for pages without scripts.
- **There is no Go WYSIWYG library, because WYSIWYG runs in the browser.** The Go side needs:
  - read one file
  - save one file through the existing `Deploy` (so quota, the scanner and the atomic swap all still apply)
  - a conflict check, so a save cannot overwrite a newer upload (for example one made with an API token)
  - a favicon helper that edits the file losslessly with the `golang.org/x/net/html` tokenizer
  - a `/preview` page with its own sandbox CSP
- **Security rule:** page HTML is never rendered on the admin origin. Previews run in a document with an opaque origin
  (CSP `sandbox allow-scripts`, never `allow-same-origin`). File reads are always `text/plain` with `nosniff`. The
  admin's CSP stays `default-src 'self'`, so every editor library is vendored, not loaded from a CDN.

## 1. Libraries

### What the admin's CSP allows

`securityHeaders` in `src/main.go` sets `Content-Security-Policy: default-src 'self'; frame-ancestors 'none'`. That
means:

- no scripts from a CDN
- no inline `<style>` or `<script>`
- iframes only from `'self'`

Any editor must therefore be vendored into `src/static/` and must not inject `<style>` elements. Constructable
stylesheets are fine.

The same CSP also covers `srcdoc` iframes: they inherit the admin's policy (Firefox fixed the exception in v55,
CVE-2017-7788). So a page previewed through `srcdoc` would lose its inline CSS and JS. Previews need their own response,
with their own CSP header (section 4).

### Rich-text editors

| Library              | Licence                                                                    | Size (approx.)           | Loads and saves a whole HTML document?                                                                                                                                                                                                       | No-build use                      |
| -------------------- | -------------------------------------------------------------------------- | ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------- |
| TipTap / ProseMirror | MIT (TipTap Pro add-ons paid)                                              | ~100-150 KB gz           | No. Body fragment only; the schema drops unknown tags, `<style>`, `<script>`                                                                                                                                                                 | ESM from CDN or a vendored bundle |
| Lexical              | MIT                                                                        | ~25 KB gz core + nodes   | No. Its own node tree; imports a fragment and serializes its own HTML                                                                                                                                                                        | Needs a bundle in practice        |
| Quill 2              | BSD-3-Clause                                                               | ~45 KB gz                | No. Delta model; most markup is normalized away                                                                                                                                                                                              | Yes, UMD file                     |
| CKEditor 5           | GPL-2.0+ or commercial; a key is needed even for GPL (`licenseKey: 'GPL'`) | ~300 KB+ gz              | Partly. "Full page HTML" plus General HTML Support keep `<head>` and many tags; scripts and head styles need opt-ins (`fullPage.allowRenderStylesFromHead`); output is still re-serialized. Whether it is free under GPL is **(unverified)** | CDN or ZIP build; injects styles  |
| TinyMCE 7/8          | GPL-2.0+ (since v7) or commercial                                          | ~200 KB+ gz plus plugins | Not free. The open-source `fullpage` plugin was removed in 6.0; "Full Page HTML" is premium                                                                                                                                                  | Self-host ZIP; injects styles     |
| Trix                 | MIT                                                                        | ~50 KB gz                | No. Its own document model, a small fixed tag set                                                                                                                                                                                            | Yes, one JS and one CSS file      |
| Editor.js            | Apache-2.0                                                                 | ~35 KB gz core           | No. Saves JSON blocks, not HTML                                                                                                                                                                                                              | Yes, UMD                          |

### Whole-page and visual builders

| Library  | Licence                              | Size (approx.)        | Loads and saves an existing page?                                                                                                                                                                                                                            | No-build use                                                                        |
| -------- | ------------------------------------ | --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------- |
| GrapesJS | BSD-3-Clause (Studio SDK commercial) | ~250 KB gz, ~1 MB min | Lossy. HTML is parsed into its component model and exported with `getHtml()`/`getCss()`. Scripts are stripped unless `parser.optionsHtml.allowScripts` is set; `<head>` needs `asDocument` / a custom parser (v0.23.1+). CSS is rewritten into its own rules | Yes (UMD and CSS), but it injects inline styles, so the admin's CSP must be relaxed |
| Craft.js | MIT                                  | small, plus React     | No. A React framework for building your own editor; state is JSON                                                                                                                                                                                            | No, needs React and a build                                                         |
| Puck     | MIT                                  | plus React            | No. JSON data rendered by your own React components                                                                                                                                                                                                          | No, needs React and a build                                                         |
| Silex    | AGPL-3.0                             | full application      | Its own GrapesJS-based app with its own storage. Not a library to embed                                                                                                                                                                                      | No                                                                                  |

### Code editors

| Library      | Licence      | Size (approx.)                                   | Whole document?          | HTML highlighting                                         | No-build use                                                                                                                                                                                              |
| ------------ | ------------ | ------------------------------------------------ | ------------------------ | --------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| CodeMirror 6 | MIT          | ~150-250 KB min for basic setup plus `lang-html` | Yes, byte-exact text     | Yes, plus embedded CSS and JS inside `<style>`/`<script>` | No official single file. Use an import map or esm.sh (watch for duplicate `@codemirror/state`), or vendor a one-off esbuild bundle. Styles use `adoptedStyleSheets`, which fits a strict CSP **(verify)** |
| Ace          | BSD-3-Clause | ~400 KB min `ace.js` plus mode and theme files   | Yes                      | Yes (`mode-html` includes CSS and JS)                     | Yes: prebuilt `ace-builds` files, no toolchain. Strict CSP needs `useStrictCSP` (Ace ≥ 1.4.13) plus its CSS files; HTML lint runs in a worker (`worker-src 'self'`)                                       |
| Monaco       | MIT          | several MB, plus workers                         | Yes                      | Yes (VS Code quality)                                     | Possible with the AMD loader and workers; heavy and CSP-unfriendly. Too much for this admin                                                                                                               |
| `<textarea>` | platform     | 0                                                | Yes (newlines become LF) | No                                                        | Built in                                                                                                                                                                                                  |

**Q5, which editors highlight HTML code:** CodeMirror 6, Ace and Monaco do. The rich-text editors do not highlight their
own content; CKEditor 5 and TinyMCE only offer a "source" view, which is a plain or premium code view, and it shows HTML
they have already re-serialized. GrapesJS's code viewer uses CodeMirror 5 to show the exported code, not the original.

## 2. CDN or vendored files

Vendor the files. Reasons:

- The CSP is `default-src 'self'`. Allowing a CDN would let a third party run script with the admin's session.
- The binary stays self-contained (`//go:embed static`), which matches how the project works today.
- It works offline and in the Pebble e2e test.

How:

- **Ace:** copy a pinned set of `ace-builds/src-min-noconflict` files (`ace.js`, `mode-html.js`, `mode-css.js`,
  `mode-javascript.js`, one theme, `worker-html.js`, and `css/` for `useStrictCSP`) into `src/static/vendor/ace/`. No
  Node needed.
- **CodeMirror 6:** run `npx esbuild --bundle --format=esm --minify` once over an entry that exports `EditorView`,
  `basicSetup` and `html()`. Commit the output as `src/static/vendor/codemirror.js` and add a `make vendor-editor`
  target. Node is then needed only to refresh that file, never to build the admin.

Recommendation: **CodeMirror 6, vendored.** It is smaller, works better on mobile and with screen readers, and is the
current standard. Choose Ace instead only if the owner refuses even a one-off Node step.

## 3. Approaches

| Approach                                                                               | Edits existing AI pages losslessly                                                                    | Favicon                          | Create from scratch        | Cost                   | Main risk                                                                                                                                                                     |
| -------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- | -------------------------------- | -------------------------- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) Code editor and live preview in a sandboxed iframe                                 | Yes, byte-exact                                                                                       | Helper button or edit the source | Template plus the editor   | Small                  | Not "visual" for non-coders                                                                                                                                                   |
| (b) `designMode` / `contenteditable` on the page in a sandboxed iframe, then serialize | Lossy: `outerHTML` re-serializes the page. If the page's scripts ran, the DOM they changed gets saved | Through (a)                      | Weak                       | Medium                 | Saving script-rendered DOM corrupts the source, so scripts must be off, and then Tailwind-CDN or JS-built pages render wrong; `designMode` adds junk (`<span style>`, `<br>`) |
| (c) GrapesJS-style builder                                                             | No (component-model round-trip, scripts off by default)                                               | Custom code                      | Good for block-built pages | Large; relaxes the CSP | Rewrites the user's page; mismatch with AI single files                                                                                                                       |
| (d) Rich-text editor for new simple pages only                                         | n/a                                                                                                   | Through the template             | Simple text pages only     | Medium                 | Pages cannot be reopened in it after anyone edits the code; a second editing model to maintain                                                                                |

How comparable products do it:

- **Tiiny.host:** a code editor, plus an "Edit site" view where you click text to change it, plus AI edit. Changes go
  live at the same URL, and paid plans keep about 5 past versions (from their marketing pages).
- **Neocities:** an Edit button on each file opens a browser code editor. The library is believed to be Ace
  **(unverified)**.
- **GitHub:** the web file editor is a code editor (CodeMirror historically) with a Preview tab, and github.dev is
  Monaco/VS Code.
- **Decap (Netlify) CMS:** form fields and a Markdown/rich-text widget write content files to git, with a React preview
  pane. It does not edit raw HTML pages.
- **Carrd:** a proprietary block builder with no import of arbitrary HTML, only "embed" elements.

In short: everyone who hosts arbitrary HTML uses (a), and adds a click-to-edit-text layer (b) as an extra. (c) and (d)
fit products that own the page format, which this one does not.

## 4. Backend (Go)

**Is there a Go WYSIWYG library?** No. WYSIWYG is a browser concern. Go only serves files, saves them and helps with
lossless edits. Relevant Go libraries:

- `golang.org/x/net/html`. Its `Tokenizer` with `Raw()` gives byte offsets, so you can splice a tag in and leave every
  other byte unchanged. `html.Parse` + `html.Render` would re-serialize the whole page, so don't use them for saving.
- `os.Root` / `os.OpenInRoot` (Go ≥ 1.24; the module uses 1.26) for reading files without path traversal or symlink
  escapes.

### API changes

| Route                                   | Purpose                                                                                           | Notes                                                                                                                                                                                                                                                                            |
| --------------------------------------- | ------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /api/pages/{name}/files/{path...}` | Raw source of one file                                                                            | `auth.Require` (API tokens work too). Response is always `text/plain; charset=utf-8` plus `nosniff` plus `Content-Security-Policy: sandbox; default-src 'none'`. `ETag` = `meta.Updated` (RFC 3339 nano). Refuse non-UTF-8 files (415) so a windows-1251 page can't be corrupted |
| `PUT /api/pages/{name}/files/{path...}` | Replace or add one file                                                                           | Body capped at `MaxPageBytes`. `If-Match` is required (missing: 428; stale: 412). Goes through `Deploy`, so quota, `scanTree` (password-phishing rule, clamd) and the atomic rename all apply                                                                                    |
| `PUT /api/pages/{name}/favicon`         | Set an embedded favicon in `index.html`                                                           | JSON `{"href":"data:image/...", "ifMatch": "..."}`. Only `data:image/(svg+xml\|png\|x-icon\|vnd.microsoft.icon\|gif\|webp)`, at most 64 KB. The value is attribute-escaped. Useful from API tokens (Claude) too                                                                  |
| `GET /preview`                          | Shell page for live previews                                                                      | Its own header replaces the admin CSP: `sandbox allow-scripts allow-forms allow-modals allow-popups; default-src * data: blob: 'unsafe-inline' 'unsafe-eval'; frame-ancestors 'self'`. Never `allow-same-origin`                                                                 |
| Create a page                           | **No new route.** The UI posts the template as one `index.html` to the existing `POST /api/pages` | The existing `existing` set in `app.js` already warns before replacing a page                                                                                                                                                                                                    |

### Store changes (`src/store.go`)

- Move the body of `Deploy` after the lock into `deployLocked(owner, name, kept, norm)`. `Deploy` keeps its signature
  and callers.
- Add `ReadFile(owner, name, p string) ([]byte, Meta, error)`: an `owned` check, then
  `os.OpenInRoot(s.sub("pages", name), p)`.
- Add `PutFile(owner, name, p string, body io.Reader, ifMatch time.Time) (Meta, error)`. Under `s.mu` it:
  1. runs the `owned` check
  2. compares `Updated` with `ifMatch` and returns `ErrConflict` on mismatch
  3. builds `[]Entry` from a `WalkDir` of the current page, with the edited path replaced or added
  4. calls `deployLocked`
  - Add
    `shortcut: copies the whole page on every save, fine under MaxPageBytes=20 MB; add an in-place write if pages get big`
    as a code comment.
- Add `ErrConflict`, and map it to 412 in `fail`.
- Add `favicon.go` with `SetFavicon(doc []byte, href string) ([]byte, error)`:
  - Tokenize the document. Remove every `<link>` whose space-separated `rel` contains the token `icon` (so `icon` and
    `shortcut icon`, but not `apple-touch-icon`).
  - Insert `<link rel="icon" href="…">` where the first removed tag was. If there was none, insert it after the
    `<head…>` start tag. If there is no head, insert it after the doctype or at the start of the file.
  - Text inside `<script>`, `<style>` and comments is never touched.
  - Adds `golang.org/x/net` to `go.mod` (same project as the `oauth2` dependency already used).

### Security invariants (keep XSS out of the admin origin)

1. Page HTML is never rendered on the admin origin: no `innerHTML`, no `srcdoc`, and never `allow-scripts` +
   `allow-same-origin` together.
2. File reads return `text/plain` with `nosniff` and a `sandbox` CSP. A `<script src="/api/...">` pointing at them is
   refused by the browser, and opening one directly cannot run script.
3. The preview runs at `/preview` in an opaque origin:
   - Its requests to the admin carry `Origin: null`. The existing Origin check in `auth.Require` refuses writes.
   - `SameSite=Lax` cookies are not sent on its cross-site subresource requests.
   - It cannot navigate the top window, because `allow-top-navigation` is not granted.
4. The parent sends HTML with `postMessage(html, "*")` (an opaque origin needs `"*"`). Messages coming back (phase 2)
   are accepted only if `event.source === iframe.contentWindow` and they are strings, and they are treated as text,
   never parsed into admin DOM.
5. The API never sends CORS headers. Pages on `*.PAGES_DOMAIN` are same-site with `admin.PAGES_DOMAIN`, so the Origin
   check, not SameSite, is what protects writes. That stays as it is.
6. The admin CSP stays unchanged. Editors are vendored.
7. Edited content goes through the same scanner as uploads.

## 5. Recommendation

Use approach **(a)**: a source editor, a sandboxed live preview, a Go favicon helper and a template for new pages.

- It is the only approach that keeps AI-generated single-file pages byte-exact, scripts and styles included.
- It needs no admin CSP change and no new frontend toolchain (the first version is a `<textarea>`).
- It also gives API-token users a favicon endpoint.

Leave visual click-to-edit (b) for phase 2, and only offer it for pages without scripts.

Not doing: GrapesJS or a builder (c), a rich-text editor (d), multi-file editing UI, version history, AI edit and
collaborative editing.

### Phase 1: edit, favicon and create (no new JS dependency)

Write the tests first, in the style of the existing table tests and `newServer`/`upload` helpers.

1. **`store_test.go`.** Write the tests first, then build the store changes from section 4 (`deployLocked`, `ReadFile`,
   `PutFile`, `ErrConflict`). Tests:
   - `ReadFile` of another owner's page returns `ErrNotFound`.
   - `../meta/x.json`, absolute paths and a symlink inside the page are refused.
   - `PutFile` changes only the target file, keeps the other files, `Tags` and `Created`, and moves `Updated` forward.
   - A stale `ifMatch` returns `ErrConflict` and leaves the files unchanged.
   - An edit adding `<input type=password>` returns `ErrRejected`.
   - An edit over the size limit returns `ErrTooBig`.
   - Adding a new path counts toward `MaxFiles`.
2. **`favicon_test.go`.** Write the tests first, then build `SetFavicon` (section 4). Tests:
   - A page with no head, then a head with no icon.
   - Replacing `rel=icon`, `rel="shortcut icon"` and `REL=ICON` written in uppercase.
   - `apple-touch-icon` is kept.
   - A `<link rel=icon>` written inside `<script>` text or a comment is left alone.
   - Every byte outside the spliced tag is identical (compare prefix and suffix).
   - `javascript:` and `http:` URLs, a non-image `data:` URL, input over 64 KB and an `href` with a `"` are rejected or
     escaped.
3. **`main_test.go`.** Write the tests first, then add the 4 routes and map `ErrConflict` in `fail`. Tests:
   - GET file returns 200 with `text/plain`, `nosniff`, a `sandbox` CSP and an `ETag`, and no
     `Access-Control-Allow-Origin`.
   - PUT without `Origin` returns 403, without `If-Match` returns 428, with a stale one returns 412.
   - `/preview` has a CSP that contains `sandbox allow-scripts`, not `allow-same-origin`, and `frame-ancestors 'self'`.
4. **UI**, in `index.html`, `app.js` and `style.css`, inside a `<dialog id="editor">`:
   - An "Edit" button on each table row loads `index.html` (`fetch` and `TextDecoder('utf-8', {fatal:true})`) into a
     `<textarea>`.
   - A preview `<iframe sandbox="allow-scripts allow-forms allow-modals allow-popups" src="/preview">` refreshes 500 ms
     after typing stops: reload the frame, then on load `postMessage` the source. Before sending, add
     `<base href="https://<name>.<domain>/">` to the copy sent to the preview (never to the saved file), so relative
     assets load from the live page.
   - The shim does `document.open(); document.write(html); document.close()`.
   - Save sends a PUT with `If-Match`. On 412, show "page changed elsewhere, reload". A `beforeunload` warning fires
     while there are unsaved changes.
   - "Set favicon" takes an image file (read with `FileReader` into a data URL) or an emoji (as an SVG data URL with
     `<text>`). It is disabled while there are unsaved changes, calls the favicon route, then reloads the source.
   - "New page" opens the same dialog with a template (HTML5, `charset`, `viewport`, `<title>`, an emoji favicon, a
     small `<style>`) and a name field. The first save is a `POST /api/pages` with a `Blob` named `index.html`; later
     saves use PUT.
5. **Docs:** add a README section and a DECISION-LOG entry ("source editor over WYSIWYG libraries: lossless; preview
   isolated with CSP sandbox").

Acceptance criteria:

- An AI page with inline `<style>`, `<script>`, a Tailwind CDN script and a base64 image opens, renders correctly in the
  preview, and after Save without changes is byte-identical on disk (CRLF and a BOM are the documented exceptions).
- Setting an emoji favicon on a page that already has two icon links leaves exactly one `rel=icon` and an unchanged rest
  of the file. The tab icon shows on `https://<name>.<PAGES_DOMAIN>`.
- "New page" with the name `hello` publishes `https://hello.<PAGES_DOMAIN>` and passes `/tls-check`.
- A script in the previewed page calling `fetch('/api/pages', {method:'POST', credentials:'include'})` fails, and
  `parent.document` throws.
- An upload made with an API token while the editor is open makes the next Save return 412, not overwrite it.
- `make test` passes (vet, golangci-lint, race).

### Phase 1b: syntax highlighting

1. Vendor CodeMirror 6 (section 2) as `src/static/vendor/codemirror.js` and load it as `<script type="module">`, which
   CSP `'self'` allows.
2. Replace the `<textarea>` behind the same `getValue`/`setValue` calls, and keep the textarea as a fallback if the
   module fails to load.
3. Acceptance: HTML, CSS and JS inside the page are highlighted, there are no CSP violations in the console, and a 5 MB
   file stays usable.

### Phase 2: only if the owner asks

- **Visual text editing:**
  - A `/preview?mode=edit` shim with `script-src 'nonce-…'`, which blocks the page's own scripts and inline handlers.
  - It loads the page with `DOMParser` (parsed scripts never run), turns on `document.designMode`, and on input posts
    back the original doctype plus `documentElement.outerHTML` with the `<base>` removed.
  - The parent puts the result into the code buffer, so the user can see the diff before saving.
  - Offer this mode only when the page has no `<script>`, or show a warning.
- **Multi-file pages:** a `GET /api/pages/{name}/files` list and a file picker in the dialog.
- **Version history:** keep the last N `pages/<name>` directories instead of deleting the trash directory, plus a
  restore button.

## 6. Risks

| Risk                                                                                                                        | Mitigation                                                                                   |
| --------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| A Save puts a broken page live immediately, with no undo                                                                    | The preview shows it before saving. Phase 2 adds version history                             |
| A save overwrites a newer upload made with a token or another tab                                                           | `If-Match` on `Updated`, required and checked under `s.mu`                                   |
| A preview misconfiguration (`allow-same-origin`) lets scripts run on the admin origin                                       | A test checks the `/preview` CSP header. Review rule: never `allow-same-origin`              |
| Non-UTF-8 pages get corrupted                                                                                               | 415 on read; the client decodes with `fatal: true`                                           |
| CRLF line endings and a UTF-8 BOM are lost by the textarea and `fetch().text()`                                             | Documented. Restore them on save only if the owner cares                                     |
| Saving copies the whole page (up to 20 MB) each time                                                                        | Fine at this scale. The code comment marks when to add an in-place write                     |
| CodeMirror has no official browser bundle                                                                                   | Commit the esbuild output with a pinned version. Ace is the fallback that needs no toolchain |
| Reloading the preview resets its scroll position                                                                            | Accepted for phase 1                                                                         |
| `document.write` behaves a little differently from loading the page normally (some `load` events, `document.currentScript`) | Accepted. Saving and opening the live page is the final check                                |

## Sources

- [CKEditor 5 Full page HTML](https://proxy-source.ckeditor.com/docs/ckeditor5/latest/features/html/full-page-html.html)
- [CKEditor 5 licence key (GPL)](https://ckeditor.com/docs/ckeditor5/latest/support/licensing/license-key-and-activation.html)
- [TinyMCE 7 release notes (GPL)](https://tiny.cloud/docs/tinymce/7/7.0-release-notes/)
- [TinyMCE migration guide: `fullpage` removed in 6.0, premium `fullpagehtml`](https://www.tiny.cloud/docs/tinymce/latest/migration-from-4x-to-8x/)
- [TinyMCE GPL discussion](https://www.github.com/tinymce/tinymce/discussions/9496)
- [GrapesJS custom HTML parser (`asDocument`, v0.23.1+)](https://grapesjs.com/docs/guides/Custom-HTML-parser.html)
- [GrapesJS parser API](https://grapesjs.com/docs/api/parser.html)
- [GrapesJS XSS in live preview with `allowScripts`](https://lightrun.com/answers/artf-grapesjs-xss-vulnerability-in-live-preview)
- [CodeMirror 6 browser ESM thread](https://discuss.codemirror.net/t/esm-compatible-codemirror-build-directly-importable-in-browser/5933)
- [CodeMirror 6 via esm.sh gist](https://gist.github.com/Potherca/028514c75f581db115797ecb50c6f945)
- [Ace `useStrictCSP` (CouchDB Fauxton PR)](https://github.com/apache/couchdb-fauxton/pull/1331)
- [Ace on a strict CSP (Drupal)](https://www.drupal.org/node/3082195)
- [Ace licence](https://en.wikipedia.com/wiki/Ace_editor)
- [Tiiny.host online editor](https://tiiny.host/feature/online-editor/)
- [Tiiny.host visual HTML editor](https://tiiny.host/use-case/visual-html-editor)
- [Neocities editor (HappyCoding)](https://happycoding.io/tutorials/html/online-code-editors)
- [Sandboxed `srcdoc` and CSP (Mozilla bug 1073952)](https://bugzilla.mozilla.org/show_bug.cgi?id=1073952)
- [Firefox test: sandboxed `srcdoc` and CSP](https://searchfox.org/firefox-main/source/testing/web-platform/tests/html/browsers/sandboxing/sandbox-inherited-from-required-csp.html)

### Critical files for implementation

- `src/store.go`
- `src/main.go`
- `src/static/app.js`
- `src/static/index.html`
- `src/auth.go` (Origin check that the preview's security relies on)
