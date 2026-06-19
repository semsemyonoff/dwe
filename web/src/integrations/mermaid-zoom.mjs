// Client-only Astro integration: inject the pan/zoom enhancer on every page. It
// does NOT touch the markdown / remark / rehype pipeline (so the MDX transform is
// untouched and can't break) — astro-mermaid still does the fence→<svg> render;
// our script just wraps each rendered diagram with controls. The styles are wired
// via Starlight `customCss` in astro.config.mjs.
export default function mermaidZoom() {
  return {
    name: 'dwe-mermaid-zoom',
    hooks: {
      'astro:config:setup': ({ injectScript }) => {
        injectScript('page', 'import("/src/scripts/mermaid-zoom.js");');
      },
    },
  };
}
