"""Take the README screenshots of a local admin, with padding around each element.

Start an empty admin first (DEV_IDENTITY logs you in without OAuth; it only works on localhost):

    cd src && SITES_DIR=$(mktemp -d) DEV_IDENTITY=dev:me ALLOWLIST=dev:me BASE_URL=http://localhost:8091 ADDR=:8091 \
        PAGES_DOMAIN=sites.example.com go run .

then: uvx --with playwright python docs/screenshots/take.py
"""

from pathlib import Path

from playwright.sync_api import Locator, Page, sync_playwright

URL = "http://localhost:8091"
OUT = Path(__file__).parent
PAD = 24  # CSS px of page background around each element shot
DEMO = {  # page name -> (size in bytes, tags)
    "go-channel-exercises": (4000, ["go", "learning", "programming"]),
    "password-to-vault": (9000, ["learning", "security"]),
    "pointer-dojo": (12000, ["go", "learning", "programming"]),
    "sql-practice-studio": (30000, ["learning", "programming"]),
    "trip-budget": (6000, ["finance", "travel"]),
    "city-map": (80000, ["travel"]),
    "time-management-tools": (3000, ["productivity"]),
}


def seed(pg: Page) -> None:
    for name, (size, tags) in DEMO.items():
        html = "<!doctype html><title>demo</title>" + "x" * size
        file = {"name": "index.html", "mimeType": "text/html", "buffer": html.encode()}
        r = pg.request.post(
            f"{URL}/api/pages",
            multipart={"name": name, "path": "index.html", "file": file},
        )
        assert r.ok, r.text()
        r = pg.request.put(f"{URL}/api/pages/{name}/tags", data=tags)
        assert r.ok, r.text()


def shot(pg: Page, el: Locator, name: str) -> None:
    box = el.bounding_box()
    assert box, name
    scroll = pg.evaluate(
        "[scrollX, scrollY]"
    )  # bounding_box is viewport-relative, clip is page-relative
    clip = {
        "x": max(box["x"] + scroll[0] - PAD, 0),
        "y": max(box["y"] + scroll[1] - PAD, 0),
        "width": box["width"] + 2 * PAD,
        "height": box["height"] + 2 * PAD,
    }
    pg.screenshot(path=OUT / name, clip=clip, full_page=True)


with sync_playwright() as p:
    b = p.chromium.launch(channel="chrome")
    pg = b.new_page(
        extra_http_headers={"Origin": URL},
        viewport={"width": 1100, "height": 900},
        device_scale_factor=2,
        color_scheme="light",
    )
    pg.goto(URL)
    seed(pg)
    pg.reload()
    pg.wait_for_selector("#pages tr td a")
    pg.screenshot(path=OUT / "admin.png", full_page=True)
    pg.click("text=Size")
    pg.click(".chip >> text=go")
    shot(pg, pg.locator("#list"), "filter-and-sort.png")
    pg.click("text=Show all")
    pg.evaluate(
        "document.querySelectorAll('#list h2, #tag-filter, thead, #pages tr:not(:has(a[href*=trip-budget]))').forEach(e => e.hidden = true)"
    )
    pg.locator("tr:has-text('trip-budget') input.tags").click()
    shot(pg, pg.locator("tr:has-text('trip-budget')"), "tags-edit.png")
    pg.reload()  # the other rows back
    pg.wait_for_selector("#pages tr td a")
    pg.click("#tokens summary")
    shot(pg, pg.locator("#tokens"), "tokens.png")
    b.close()
