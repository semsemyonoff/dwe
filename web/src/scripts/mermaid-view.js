// Shared interactive Mermaid view — pan / zoom / fit / expand-to-modal / export —
// a dependency-free port of podlapka's MermaidView. Used inline (mermaid-zoom.js
// wraps every astro-mermaid diagram) and full-window (the /mermaid popout page).
//
// Layout matches the original: a bordered canvas with a top-right control cluster
// (+ / − / fit / expand) and a bottom-right action cluster (export popover +
// "open in new tab"). Wheel: Ctrl/⌘+wheel zooms toward the cursor, a plain wheel
// pans. Expand opens a centered modal; export re-renders at a chosen theme.

const MIN_SCALE = 0.2;
const MAX_SCALE = 8;
const PAN_STEP = 60;
const ZOOM = 1.2;
const DARK_BG = '#1b1b1f';
const LIGHT_BG = '#ffffff';

const prefersDark = () => window.matchMedia('(prefers-color-scheme: dark)').matches;
const docDark = () => document.documentElement.dataset.theme === 'dark';

// Base64 (UTF-8 safe) for the popout URL hash — far shorter than %-encoding.
export function encodeSource(code) {
  const bytes = new TextEncoder().encode(code);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}
export function decodeSource(b64) {
  return new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)));
}

// Re-render a source to an SVG string at a specific theme (for themed export and
// the standalone page). Mermaid is already a dependency (astro-mermaid uses it).
export async function renderSource(source, dark) {
  const mermaid = (await import('mermaid')).default;
  mermaid.initialize({ startOnLoad: false, theme: dark ? 'dark' : 'neutral' });
  const id = 'mz-x-' + Math.random().toString(36).slice(2);
  const { svg } = await mermaid.render(id, source);
  return svg;
}

function el(tag, cls, html) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html != null) e.innerHTML = html;
  return e;
}

function download(blob, filename) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function rasterize(svgString, dark, name) {
  // Read dimensions via a tolerant HTML parse — mermaid emits HTML <br> inside
  // foreignObject labels, which strict XML parsing rejects.
  const probe = document.createElement('div');
  probe.innerHTML = svgString;
  const svg = probe.querySelector('svg');
  const vb = svg && svg.viewBox && svg.viewBox.baseVal;
  const w = (svg && parseFloat(svg.getAttribute('width'))) || (vb && vb.width) || 800;
  const h = (svg && parseFloat(svg.getAttribute('height'))) || (vb && vb.height) || 600;
  const img = new Image();
  img.onload = () => {
    const scale = 2;
    const canvas = document.createElement('canvas');
    canvas.width = Math.round(w * scale);
    canvas.height = Math.round(h * scale);
    const ctx = canvas.getContext('2d');
    ctx.fillStyle = dark ? DARK_BG : LIGHT_BG;
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    ctx.scale(scale, scale);
    ctx.drawImage(img, 0, 0, w, h);
    canvas.toBlob((b) => b && download(b, name + '.png'), 'image/png');
  };
  // SVGs with HTML <br> in foreignObjects can't be rasterized through an <img>
  // (the image SVG parser is XML-strict); fall back to downloading the SVG.
  img.onerror = () => download(new Blob([svgString], { type: 'image/svg+xml;charset=utf-8' }), name + '.svg');
  img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svgString);
}

/**
 * Build and wire an interactive view around an <svg> element; returns the `.mz`
 * root. opts: { svgEl, source, fill, expanded, popoutHref, onClose, name }.
 */
export function mountView({ svgEl, source, fill = false, expanded = false, popoutHref = null, onClose = null, name = 'diagram' }) {
  const root = el('div', 'mz' + (expanded ? ' mz--modal' : fill ? ' mz--fill' : ''));
  if (fill) root.style.background = docDark() || prefersDark() ? DARK_BG : LIGHT_BG;

  const viewport = el('div', 'mz-viewport');
  viewport.tabIndex = 0;
  viewport.setAttribute('role', 'application');
  viewport.setAttribute('aria-label', 'Diagram — drag to pan, Ctrl/⌘+scroll or +/− to zoom');
  const inner = el('div', 'mz-inner');
  inner.appendChild(svgEl);
  viewport.appendChild(inner);

  const view = { x: 0, y: 0, scale: 1 };
  const apply = () => {
    // Bounded pan: when the diagram is larger than the viewport its edges stop at
    // the viewport edges; when smaller it stays fully inside — no scrolling off
    // into empty space.
    const sw = inner.offsetWidth * view.scale;
    const sh = inner.offsetHeight * view.scale;
    const vw = viewport.clientWidth;
    const vh = viewport.clientHeight;
    view.x = Math.max(Math.min(0, vw - sw), Math.min(Math.max(0, vw - sw), view.x));
    view.y = Math.max(Math.min(0, vh - sh), Math.min(Math.max(0, vh - sh), view.y));
    inner.style.transform = `translate(${view.x}px, ${view.y}px) scale(${view.scale})`;
  };
  const clamp = (s) => Math.max(MIN_SCALE, Math.min(MAX_SCALE, s));
  const zoomAt = (next, cx, cy) => {
    const s = clamp(next);
    const k = s / view.scale;
    view.x = cx - (cx - view.x) * k;
    view.y = cy - (cy - view.y) * k;
    view.scale = s;
    apply();
  };
  const zoomButton = (f) => {
    const r = viewport.getBoundingClientRect();
    zoomAt(view.scale * f, r.width / 2, r.height / 2);
  };
  const fit = () => {
    if (!inner.querySelector('svg')) return;
    const cw = inner.offsetWidth;
    const ch = inner.offsetHeight;
    const vw = viewport.clientWidth;
    const vh = viewport.clientHeight;
    if (!cw || !ch || !vw || !vh) return;
    const scale = clamp(Math.min(vw / cw, vh / ch) * 0.95);
    view.x = (vw - cw * scale) / 2;
    view.y = (vh - ch * scale) / 2;
    view.scale = scale;
    apply();
  };

  // Pan via pointer drag.
  let drag = null;
  viewport.addEventListener('pointerdown', (e) => {
    if (e.button !== 0 || e.target.closest('.mz-controls, .mz-actions')) return;
    viewport.setPointerCapture(e.pointerId);
    drag = { px: e.clientX, py: e.clientY, x: view.x, y: view.y };
  });
  viewport.addEventListener('pointermove', (e) => {
    if (!drag) return;
    view.x = drag.x + (e.clientX - drag.px);
    view.y = drag.y + (e.clientY - drag.py);
    apply();
  });
  const endDrag = (e) => {
    drag = null;
    if (viewport.hasPointerCapture?.(e.pointerId)) viewport.releasePointerCapture(e.pointerId);
  };
  viewport.addEventListener('pointerup', endDrag);
  viewport.addEventListener('pointercancel', endDrag);

  // Wheel: Ctrl/⌘ → zoom toward cursor; plain → pan (matches the trackpad
  // convention in the original).
  viewport.addEventListener(
    'wheel',
    (e) => {
      e.preventDefault();
      if (e.ctrlKey || e.metaKey) {
        const r = viewport.getBoundingClientRect();
        const factor = Math.exp(Math.max(-0.3, Math.min(0.3, -e.deltaY * 0.01)));
        zoomAt(view.scale * factor, e.clientX - r.left, e.clientY - r.top);
      } else {
        view.x -= e.deltaX;
        view.y -= e.deltaY;
        apply();
      }
    },
    { passive: false },
  );

  // Keyboard: arrows pan, +/− zoom, 0/f fit.
  viewport.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowUp') view.y += PAN_STEP;
    else if (e.key === 'ArrowDown') view.y -= PAN_STEP;
    else if (e.key === 'ArrowLeft') view.x += PAN_STEP;
    else if (e.key === 'ArrowRight') view.x -= PAN_STEP;
    else if (e.key === '+' || e.key === '=') return zoomButton(ZOOM);
    else if (e.key === '-') return zoomButton(1 / ZOOM);
    else if (e.key === '0' || e.key === 'f' || e.key === 'F') return fit();
    else return;
    e.preventDefault();
    apply();
  });

  // Top-right control cluster.
  const controls = el('div', 'mz-controls');
  const cbtn = (mz, label, glyph) =>
    `<button type="button" data-mz="${mz}" aria-label="${label}" title="${label}">${glyph}</button>`;
  controls.innerHTML =
    cbtn('in', 'Zoom in', '+') +
    cbtn('out', 'Zoom out', '−') +
    cbtn('fit', 'Fit to view (0)', '⛶') +
    (fill ? '' : cbtn('expand', expanded ? 'Close' : 'Expand', expanded ? '✕' : '⤢'));
  controls.addEventListener('click', (e) => {
    const a = e.target.closest('[data-mz]')?.dataset.mz;
    if (a === 'in') zoomButton(ZOOM);
    else if (a === 'out') zoomButton(1 / ZOOM);
    else if (a === 'fit') fit();
    else if (a === 'expand') expanded ? onClose?.() : openModal(svgEl, source, name);
  });

  // Bottom-right action cluster: export popover + popout.
  const actions = el('div', 'mz-actions');
  const exportWrap = el('div', 'mz-export-wrap');
  const trigger = el('button', 'mz-export-trigger', '⤓');
  trigger.type = 'button';
  trigger.title = 'Export diagram';
  trigger.setAttribute('aria-label', 'Export diagram');
  exportWrap.appendChild(trigger);
  actions.appendChild(exportWrap);
  if (popoutHref && !expanded && !fill) {
    const a = el('a', 'mz-popout', '↗');
    a.href = popoutHref;
    a.target = '_blank';
    a.rel = 'noopener';
    a.title = 'Open in new tab';
    a.setAttribute('aria-label', 'Open in new tab');
    actions.appendChild(a);
  }

  // Export popover (Format + Theme + Download), opening upward from the trigger.
  let pop = null;
  let fmt = 'svg';
  let theme = 'system';
  const closePop = () => {
    pop?.remove();
    pop = null;
    document.removeEventListener('mousedown', onDocDown, true);
    document.removeEventListener('keydown', onPopKey, true);
  };
  const onDocDown = (e) => {
    if (!exportWrap.contains(e.target)) closePop();
  };
  const onPopKey = (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      closePop();
    }
  };
  const seg = (options, current, set) => {
    const wrap = el('div', 'mz-seg');
    for (const [val, lbl] of options) {
      const b = el('button', current() === val ? 'on' : '', lbl);
      b.type = 'button';
      b.onclick = () => {
        set(val);
        rebuildPop();
      };
      wrap.appendChild(b);
    }
    return wrap;
  };
  const rebuildPop = () => {
    if (!pop) return;
    pop.innerHTML = '';
    const r1 = el('div', 'mz-export-row');
    r1.appendChild(el('span', 'mz-export-key', 'Format'));
    r1.appendChild(seg([['svg', 'SVG'], ['png', 'PNG']], () => fmt, (v) => (fmt = v)));
    const r2 = el('div', 'mz-export-row');
    r2.appendChild(el('span', 'mz-export-key', 'Theme'));
    r2.appendChild(seg([['system', 'System'], ['light', 'Light'], ['dark', 'Dark']], () => theme, (v) => (theme = v)));
    const go = el('button', 'mz-export-go', 'Download ' + fmt.toUpperCase());
    go.type = 'button';
    go.onclick = runExport;
    pop.append(r1, r2, go);
  };
  async function runExport() {
    const dark = theme === 'dark' || (theme === 'system' && prefersDark());
    const svgString = await renderSource(source, dark);
    if (fmt === 'svg') download(new Blob([svgString], { type: 'image/svg+xml;charset=utf-8' }), name + '.svg');
    else rasterize(svgString, dark, name);
    closePop();
  }
  trigger.addEventListener('click', () => {
    if (pop) return closePop();
    pop = el('div', 'mz-export-pop');
    pop.setAttribute('role', 'dialog');
    pop.setAttribute('aria-label', 'Export options');
    exportWrap.appendChild(pop);
    rebuildPop();
    document.addEventListener('mousedown', onDocDown, true);
    document.addEventListener('keydown', onPopKey, true);
  });

  root.append(controls, actions, viewport);
  requestAnimationFrame(fit);
  new ResizeObserver(() => fit()).observe(viewport);
  return root;
}

// Expand: a centered modal (portaled to <body>) with a fresh view over a clone of
// the diagram. Body scroll locked; closes on the ✕ button, backdrop click, or Esc.
function openModal(svgEl, source, name) {
  const backdrop = el('div', 'mz-backdrop');
  const prevOverflow = document.body.style.overflow;
  const onKey = (e) => {
    if (e.key === 'Escape') close();
  };
  function close() {
    backdrop.remove();
    document.body.style.overflow = prevOverflow;
    document.removeEventListener('keydown', onKey);
  }
  const modal = mountView({ svgEl: svgEl.cloneNode(true), source, expanded: true, name, onClose: close });
  backdrop.appendChild(modal);
  backdrop.addEventListener('click', (e) => {
    if (e.target === backdrop) close();
  });
  document.body.style.overflow = 'hidden';
  document.addEventListener('keydown', onKey);
  document.body.appendChild(backdrop);
}
