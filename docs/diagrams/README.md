# Diagrams

`*.mmd` files are the [Mermaid](https://mermaid.js.org/) sources and the
only files to edit. The `*.svg` files are generated from them and embedded
in the docs, so they display in any Markdown viewer.

Regenerate an SVG after editing its source:

```bash
npx -y @mermaid-js/mermaid-cli -i docs/diagrams/test-run-workflow.mmd -o docs/diagrams/test-run-workflow.svg -b white
```

- **Browser:** `mermaid-cli` drives a headless browser. To use an installed
  Edge or Chrome instead of downloading Chromium, set
  `PUPPETEER_SKIP_DOWNLOAD=true` during install and pass
  `-p puppeteer.json`, containing
  `{"executablePath": "<path to msedge.exe or chrome.exe>"}`.
- **Layout:** each source starts with a config block setting
  `wrappingWidth: 320`. That widens the boxes so the diagrams are less
  tall.
