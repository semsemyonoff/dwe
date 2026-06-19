// Full-window Mermaid view for the /mermaid popout (the ↗ "open in new tab"
// target). Reads the base64 source from the URL hash, renders it, and mounts the
// shared interactive view in fill mode. Pulls in the styles itself since this
// page is outside Starlight (no customCss).
import { mountView, decodeSource, renderSource } from './mermaid-view.js';
import '../styles/mermaid-zoom.css';

async function main() {
  const root = document.getElementById('mz-root');
  const b64 = location.hash.replace(/^#/, '');
  if (!b64) {
    root.textContent = 'No diagram source in the URL.';
    return;
  }
  let source;
  try {
    source = decodeSource(b64);
  } catch {
    root.textContent = 'Could not decode the diagram source.';
    return;
  }
  try {
    const dark = document.documentElement.dataset.theme === 'dark' || window.matchMedia('(prefers-color-scheme: dark)').matches;
    const svgString = await renderSource(source, dark);
    // Inject as HTML (not strict XML) so mermaid's HTML <br> labels parse.
    const tmp = document.createElement('div');
    tmp.innerHTML = svgString;
    const svgEl = tmp.querySelector('svg');
    if (!svgEl) {
      root.textContent = 'Failed to render diagram.';
      return;
    }
    root.replaceChildren(mountView({ svgEl, source, fill: true }));
  } catch (err) {
    root.textContent = 'Failed to render diagram: ' + (err?.message || err);
  }
}

main();
