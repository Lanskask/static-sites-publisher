"""Browser check of the page editor against a local admin: does it work in a real browser, and is the preview sandboxed?

Start an empty admin (DEV_IDENTITY logs you in without OAuth; it only works on localhost):

    cd src && SITES_DIR=$(mktemp -d) DEV_IDENTITY=dev:me ALLOWLIST=dev:me BASE_URL=http://localhost:8092 ADDR=:8092 \
        PAGES_DOMAIN=sites.test go run .

then: uvx --with playwright python scripts/editor_check.py

Answer (2026-11-24, Chrome): all checks pass. Inline <style> and <script> of a page run in the preview; the preview
cannot read the admin (parent.document throws) and its write to the API is refused; no CSP violations on the admin.
"""

import json
import re
import time

from playwright.sync_api import Page, expect, sync_playwright

URL = "http://localhost:8092"
NEW = f"hello-{int(time.time())}"  # a fresh name, so the check can run again on the same admin
PAGE = """<!doctype html><html><head><title>t</title><style>h1{color:rgb(1, 2, 3)}</style></head><body><h1>v1</h1>
<script>
document.body.insertAdjacentHTML("beforeend", "<p id=ran>script ran</p>");
let leak = "blocked"; try { leak = parent.document.title; } catch (e) {}
fetch("/api/pages/demo", { method: "DELETE", credentials: "include" })
  .then(r => r.status, () => "network error")
  .then(s => document.body.insertAdjacentHTML("beforeend", `<p id=probe>${leak} ${s}</p>`));
</script></body></html>"""

failures: list[str] = []


def check(what: str, ok: bool, got: object = "") -> None:
    print(("PASS " if ok else "FAIL ") + what + ("" if ok else f": got {got}"))
    if not ok:
        failures.append(what)


def set_source(pg: Page, text: str) -> None:
    """Replace the editor's text the way a user does: select all, then paste. (keyboard.insert_text would make
    Chrome edit the DOM directly, which logs a harmless inline-style CSP report that real typing never causes.)"""
    pg.evaluate("t => navigator.clipboard.writeText(t)", text)
    pg.click(".cm-content")
    pg.keyboard.press("ControlOrMeta+a")
    pg.keyboard.press("ControlOrMeta+v")


def wait_source(pg: Page, needle: str) -> str:
    for _ in range(50):
        src = pg.evaluate("source.get()")
        if needle in src:
            return src
        pg.wait_for_timeout(100)
    raise AssertionError(f"editor never contained {needle!r}")


def preview_text(pg: Page, selector: str) -> str:
    frame = pg.frame_locator("#ed-preview")
    return frame.locator(selector).inner_text(timeout=5000)


with sync_playwright() as p:
    b = p.chromium.launch(channel="chrome")
    ctx = b.new_context(
        extra_http_headers={"Origin": URL},
        permissions=["clipboard-read", "clipboard-write"],
    )
    pg = ctx.new_page()
    csp: list[str] = []
    pg.on(
        "console",
        lambda m: (
            csp.append(f"{m.text} at {m.location}")
            if "Content Security Policy" in m.text
            else None
        ),
    )
    pg.goto(URL)
    file = {"name": "index.html", "mimeType": "text/html", "buffer": PAGE.encode()}
    r = pg.request.post(
        f"{URL}/api/pages",
        multipart={"name": "demo", "path": "index.html", "file": file},
    )
    assert r.ok, r.text()
    pg.request.delete(
        f"{URL}/api/drafts/demo"
    )  # a draft left by an earlier run would be opened instead
    pg.reload()

    # edit an existing page
    pg.click("tr:has-text('demo') >> text=Edit")
    check(
        "preview runs the page's inline script",
        preview_text(pg, "#ran") == "script ran",
    )
    color = (
        pg.frame_locator("#ed-preview")
        .locator("h1")
        .evaluate("e => getComputedStyle(e).color")
    )
    check("preview applies the page's inline style", color == "rgb(1, 2, 3)", color)
    probe = preview_text(pg, "#probe")
    check(
        "preview cannot read the admin or write to its API",
        probe.startswith("blocked") and not probe.endswith("204"),
        probe,
    )

    expect(pg.locator(".cm-editor")).to_be_visible()
    check(
        "CodeMirror replaced the textarea and highlights HTML",
        not pg.locator("#ed-src").is_visible()
        and pg.locator(".cm-content span[class]").count() > 5,
    )
    set_source(pg, PAGE.replace("v1", "v2"))
    pg.keyboard.press("ControlOrMeta+End")
    pg.keyboard.type(
        "\n<!-- typed -->"
    )  # real keystrokes too, for the CSP check at the end
    wait_source(pg, "<!-- typed -->")
    expect(pg.locator("#ed-state")).to_have_text(re.compile("^Draft saved"))
    live = pg.request.get(f"{URL}/api/pages/demo/source").text()
    check("saving a draft leaves the live page alone", ">v1<" in live)
    check("preview shows the draft", "v2" in preview_text(pg, "h1"))

    pg.fill("#ed-emoji", "🚀")
    pg.press("#ed-emoji", "Enter")
    src = wait_source(pg, 'rel="icon"')
    if src.count('rel="icon"') != 1 or "v2" not in src:
        print(repr(src))
    check(
        "emoji favicon is inserted once, rest unchanged",
        src.count('rel="icon"') == 1
        and src.replace(re.search(r'<link rel="icon"[^>]*>', src)[0], "")
        == PAGE.replace("v1", "v2") + "\n<!-- typed -->",
    )
    # visual text editing: only for pages without scripts, and the frame stays sandboxed
    check(
        "visual edit is off for a page with scripts",
        pg.locator("#ed-visual").is_disabled(),
    )
    before = pg.evaluate("source.get()")
    SIMPLE = "<!doctype html><html><head><title>t</title><style>h1{color:rgb(1, 2, 3)}</style></head><body><h1>v3</h1></body></html>"
    set_source(pg, SIMPLE)
    expect(pg.locator("#ed-visual")).to_be_enabled()
    pg.click("#ed-visual")
    h1 = pg.frame_locator("#ed-preview").locator("h1")
    h1.click()
    pg.keyboard.press("End")
    pg.keyboard.type(" edited")
    src = wait_source(pg, "v3 edited")
    check(
        "visual edit lands in the source with doctype, head and style intact",
        src.lower().startswith("<!doctype html>")
        and "<title>t</title>" in src
        and "h1{color:rgb(1, 2, 3)}" in src
        and "<base" not in src,
        src,
    )
    leak = h1.evaluate(
        "() => { try { parent.document.title; return 'leak' } catch { return 'blocked' } }"
    )
    check("visual frame cannot read the admin", leak == "blocked", leak)
    pg.click(
        "#ed-visual"
    )  # back to the preview, and put the earlier draft back for the checks below
    set_source(pg, before)
    wait_source(pg, "<!-- typed -->")

    pg.click("#ed-close")
    expect(pg.locator("tr:has-text('demo') .badge")).to_have_count(1)
    check("closing keeps the draft (row shows a badge)", True)

    # reopening shows the draft, and publishing makes it live
    pg.click("tr:has-text('demo') >> text=Edit")
    wait_source(pg, "v2")
    pg.click("#ed-publish")
    pg.wait_for_selector("#status.ok")
    live = pg.request.get(f"{URL}/api/pages/demo/source").text()
    check(
        "publish makes the draft live, favicon included",
        ">v2<" in live and 'rel="icon"' in live,
    )

    # a new page from the template
    # (waits use expect(), not wait_for_function: the admin's CSP forbids the eval that one needs)
    pg.click("#new-page")
    expect(pg.frame_locator("#ed-preview").locator("h1")).to_have_text("Hello")
    check("new page shows the template in the preview", True)
    pg.fill("#ed-name", NEW)
    pg.press("#ed-name", "Tab")
    expect(pg.locator("#ed-state")).to_have_text(re.compile("^Draft saved"))
    check(
        "unpublished draft is listed",
        NEW in json.dumps(pg.request.get(f"{URL}/api/drafts").json()),
    )
    pg.click("#ed-publish")
    pg.wait_for_selector("#status.ok")
    check("new page is published", pg.request.get(f"{URL}/api/pages/{NEW}/source").ok)
    check("no CSP violations on the admin", not csp, csp)
    b.close()

raise SystemExit(f"{len(failures)} check(s) failed" if failures else 0)
