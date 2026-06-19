// Client-only: wrap every astro-mermaid diagram with the interactive view
// (pan/zoom/fit/expand/export). astro-mermaid renders each `<pre class="mermaid">`
// to an inline <svg> (client-side, and again on theme change); we watch for that
// <svg> and mount the view over it. Re-running keeps it working across theme
// switches and Astro view-transition navigations.
//
// Injected on every page via src/integrations/mermaid-zoom.mjs; styles live in
// src/styles/mermaid-zoom.css (wired through Starlight customCss).
import { mountView, encodeSource } from './mermaid-view.js';

const BASE = import.meta.env.BASE_URL; // e.g. "/dwe/"

function enhance(pre) {
  if (pre.querySelector(':scope > .mz')) return; // already wrapped
  const svg = pre.querySelector(':scope > svg');
  if (!svg) return; // not rendered yet (or a theme re-render is in flight)

  pre.classList.add('mz-host');
  const source = pre.getAttribute('data-diagram') || svg.textContent || '';
  const popoutHref = `${BASE}mermaid/#${encodeSource(source)}`;
  const view = mountView({ svgEl: svg, source, popoutHref });
  pre.replaceChildren(view);
}

function enhanceAll() {
  document.querySelectorAll('pre.mermaid').forEach(enhance);
}

function start() {
  enhanceAll();
  // astro-mermaid renders asynchronously and re-renders on theme change; observe
  // the DOM and (re)enhance whenever a fresh <svg> lands in a mermaid <pre>.
  new MutationObserver(() => enhanceAll()).observe(document.body, { childList: true, subtree: true });
}

if (document.readyState !== 'loading') start();
else document.addEventListener('DOMContentLoaded', start);
document.addEventListener('astro:page-load', enhanceAll);
document.addEventListener('astro:after-swap', enhanceAll);
