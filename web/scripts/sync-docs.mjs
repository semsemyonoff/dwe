// Transforms the repo's canonical docs/ tree into a Starlight content tree.
//
// Source of truth is docs/reference/ + docs/guides/ (and their docs/i18n/ru/
// mirror). Those files stay byte-identical (they are embedded in the binary and
// content-hashed); EVERYTHING Starlight-specific happens here at build time:
//   - i18n path remap (docs/i18n/ru/X -> content/docs/ru/X)
//   - title frontmatter derived from the H1 (and the H1 stripped from the body)
//   - relative .md links rewritten to base-aware site slugs; links into
//     docs/internals or docs/plans rewritten to GitHub blob URLs
//   - the manual "*Languages: …*" line stripped (Starlight has a locale switcher)
//   - a splash landing page generated per locale
//   - a sidebar (src/sidebar.generated.mjs) derived from each index.md's TOC
//
// This module is the ONLY writer to src/content/docs/. Pure helpers are exported
// for unit tests (scripts/sync-docs.test.mjs); main() is the filesystem driver.

import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { copyFile, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const WEB_DIR = path.resolve(__dirname, '..');
export const REPO_ROOT = path.resolve(WEB_DIR, '..');

export const DOCS_ROOT = path.join(REPO_ROOT, 'docs');
const OUT_DIR = path.join(WEB_DIR, 'src', 'content', 'docs');
const PUBLIC_DIR = path.join(WEB_DIR, 'public');
const ASSETS_DIR = path.join(WEB_DIR, 'src', 'assets');
// The DWE mark used for desktop notifications (square bracket icon) — reused as
// the site header logo so the two surfaces share one brand asset.
const LOGO_SRC = path.join(REPO_ROOT, 'internal', 'core', 'notify', 'assets', 'icon.png');
const LOGO_OUT = path.join(ASSETS_DIR, 'dwe-logo.png');
const SIDEBAR_OUT = path.join(WEB_DIR, 'src', 'sidebar.generated.mjs');
const VERSION_OUT = path.join(WEB_DIR, 'src', 'version.generated.mjs');

// Shared with astro.config.mjs — internal links are emitted base-prefixed.
export const BASE = '/dwe/';
export const REPO = 'semsemyonoff/dwe';

// Trees published to the site. Everything else under docs/ (internals/, plans/)
// is excluded; links into it become GitHub blob URLs.
const PUBLISHED_TOP = new Set(['reference', 'guides']);

// Top-level sidebar groups, in order. Labels are chrome (the reference/guides
// index H1s are unsuitable), so the only hand-authored bilingual strings here.
const TOP_GROUPS = [
  { dir: 'guides', label: 'Guides', labelRu: 'Руководства' },
  { dir: 'reference', label: 'Reference', labelRu: 'Справочник' },
];

// Index headings whose list is the directory's child-page list (EN + RU). Pure
// in-page TOCs ("Contents"/"Содержание") and cross-links ("Related"/"См. также")
// are deliberately absent.
const CHILD_SECTIONS = new Set([
  'pages', 'sections', 'further reading',
  'страницы', 'разделы', 'дополнительное чтение',
]);

// ---------------------------------------------------------------------------
// Pure helpers (unit-tested)
// ---------------------------------------------------------------------------

/** Strip the leading `# `, surrounding whitespace, and inline-code backticks. */
export function cleanLabel(text) {
  return text.replace(/^#+\s+/, '').replace(/`/g, '').trim();
}

/**
 * Sidebar group label: cleanLabel plus drop a trailing path-hint parenthetical
 * ("Concepts (concepts/)" -> "Concepts"). Only strips a parenthetical that ends
 * in `/` so file-name labels ("deploy.yml / reset.yml", "commands/") are kept.
 */
export function groupLabel(text) {
  return cleanLabel(text).replace(/\s*\([^)]*\/\)$/, '').trim();
}

/** YAML double-quoted scalar (titles may contain `:`, `/`, `<>`, quotes). */
export function yamlQuote(s) {
  return `"${s.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
}

/**
 * Pull the title from the first H1 and return the body with that line removed.
 * Throws if the file has no H1 — every doc page must have a title.
 */
export function extractTitleAndBody(md, label = 'document') {
  const lines = md.split('\n');
  const idx = lines.findIndex((l) => /^#\s+\S/.test(l));
  if (idx === -1) throw new Error(`no H1 heading found in ${label}`);
  const title = cleanLabel(lines[idx]);
  lines.splice(idx, 1);
  return { title, body: lines.join('\n') };
}

/** Remove the manual "*Languages: …*" / "*Языки: …*" line (any locale). */
export function stripLanguageLine(md) {
  return md.replace(/^\*(?:Languages|Языки):.*$\n?/gm, '');
}

/** docs-relative source path -> content-tree-relative output path. */
export function outputRelFor(relFromDocs) {
  const p = relFromDocs.split(path.sep).join('/');
  return p.startsWith('i18n/ru/') ? 'ru/' + p.slice('i18n/ru/'.length) : p;
}

/** content-tree-relative path -> Starlight slug (drop .md and trailing /index). */
export function slugFor(outputRel) {
  return outputRel
    .replace(/\.md$/, '')
    .replace(/\/index$/, '')
    .replace(/^index$/, '');
}

/** Site link for a published target, base-prefixed, with optional #anchor. */
export function siteLink(slug, anchor = '') {
  return `${BASE}${slug}${slug ? '/' : ''}${anchor}`;
}

/** GitHub blob URL for an excluded (internals/plans) doc, repo-root-relative. */
export function githubBlob(relFromRepoRoot, anchor = '') {
  return `https://github.com/${REPO}/blob/main/${relFromRepoRoot}${anchor}`;
}

/** Where does a docs-relative path land? */
export function classify(relFromDocs) {
  const top = relFromDocs.split('/')[0];
  if (top === 'reference' || top === 'guides') return 'published';
  if (relFromDocs.startsWith('i18n/ru/reference/') || relFromDocs.startsWith('i18n/ru/guides/')) {
    return 'published';
  }
  if (top === 'internals' || top === 'plans') return 'excluded';
  return 'other';
}

/**
 * Rewrite every markdown link in `body`.
 *  - external / mailto / pure-anchor links: unchanged
 *  - .md link into a published tree: base-aware site slug
 *  - .md link into a non-published part of docs/ (internals/plans/…): GitHub blob
 *  - any link to a repo file OUTSIDE docs/ (LICENSE, skills/…, scripts/…): GitHub
 *    blob — lets the README's non-docs links work on the site
 *  - non-.md link inside docs/ (an asset): unchanged
 *  - link to a nonexistent in-repo file: degraded to plain text (words kept) and
 *    reported via `onMissing` — surfaces pre-existing link rot without
 *    hard-failing the build; starlight-links-validator gates resolvable links.
 *
 * `repoRoot` is the resolution base for outside-docs links (the README lives at
 * the repo root). `preferLocale: 'ru'` redirects a published link that resolved
 * to an English page to its Russian counterpart when one exists — the RU README
 * (and some RU pages) use relative paths that resolve into the English tree.
 * `exists` / `onMissing` are injected for testability.
 */
export function rewriteLinks(body, { srcFileAbs, docsRoot = DOCS_ROOT, repoRoot = REPO_ROOT, preferLocale, exists = existsSync, onMissing } = {}) {
  const srcDir = path.dirname(srcFileAbs);
  const report = (raw, resolved) =>
    onMissing?.({ from: path.relative(docsRoot, srcFileAbs).split(path.sep).join('/'), href: raw, resolved });

  return body.replace(/\[([^\]]*)\]\(([^)]+)\)/g, (whole, label, href) => {
    const raw = href.trim();
    if (/^(https?:|mailto:|#)/.test(raw) || raw === '') return whole;

    const hashAt = raw.indexOf('#');
    const pathPart = hashAt === -1 ? raw : raw.slice(0, hashAt);
    const anchor = hashAt === -1 ? '' : raw.slice(hashAt);
    if (!pathPart) return whole;

    const abs = path.resolve(srcDir, pathPart);
    const relFromDocs = path.relative(docsRoot, abs).split(path.sep).join('/');

    if (!relFromDocs.startsWith('..')) {
      // inside docs/
      if (!pathPart.endsWith('.md')) return whole; // asset — leave alone
      if (!exists(abs)) return report(raw, relFromDocs), label;
      const kind = classify(relFromDocs);
      if (kind === 'published') {
        let rel = relFromDocs;
        // RU source linking into the English tree -> use the RU page if it exists.
        if (preferLocale === 'ru' && !rel.startsWith('i18n/') && exists(path.join(docsRoot, 'i18n', 'ru', rel))) {
          rel = `i18n/ru/${rel}`;
        }
        return `[${label}](${siteLink(slugFor(outputRelFor(rel)), anchor)})`;
      }
      return `[${label}](${githubBlob('docs/' + relFromDocs, anchor)})`; // excluded / other
    }

    // outside docs/ — link to repo source (LICENSE, skills/, …) -> GitHub blob
    const relFromRepo = path.relative(repoRoot, abs).split(path.sep).join('/');
    if (relFromRepo.startsWith('..')) return whole; // outside the repo entirely
    if (!exists(abs)) return report(raw, relFromRepo), label;
    return `[${label}](${githubBlob(relFromRepo, anchor)})`;
  });
}

/**
 * Rewrite `<img src="…">` tags whose src is a relative in-repo file: the file is
 * recorded via `onCopy` (the driver copies it into web/public/) and the src is
 * rewritten to a base-absolute URL. Used for the README logo.
 */
export function rewriteImages(body, { srcFileAbs, exists = existsSync, onCopy } = {}) {
  const srcDir = path.dirname(srcFileAbs);
  return body.replace(/<img\b([^>]*?)\ssrc="([^"]+)"([^>]*)>/g, (whole, pre, src, post) => {
    if (/^(https?:|data:|\/)/.test(src)) return whole; // external / absolute
    const abs = path.resolve(srcDir, src);
    if (!exists(abs)) return whole;
    const base = path.basename(abs);
    onCopy?.({ abs, base });
    return `<img${pre} src="${BASE}${base}"${post}>`;
  });
}

/**
 * Parse a directory index.md's child-page list. Returns ordered flat items and,
 * when the list uses ### subheadings (guides), the same items grouped.
 */
export function parseIndex(md) {
  const lines = md.split('\n');
  const flat = [];
  const groups = [];
  let inSection = false;
  let current = null; // active ### subgroup

  for (const line of lines) {
    const h2 = line.match(/^##(?!#)\s+(.+?)\s*$/);
    if (h2) {
      if (inSection) break; // child-list section ended
      if (CHILD_SECTIONS.has(cleanLabel(h2[1]).toLowerCase())) {
        inSection = true;
        current = null;
      }
      continue;
    }
    if (!inSection) continue;

    const h3 = line.match(/^###\s+(.+?)\s*$/);
    if (h3) {
      current = { label: cleanLabel(h3[1]), items: [] };
      groups.push(current);
      continue;
    }

    const li = line.match(/^\s*[-*]\s+\[([^\]]+)\]\(([^)]+)\)/);
    if (li) {
      const target = li[2].split('#')[0].trim();
      if (!target) continue; // pure anchor
      const item = { label: cleanLabel(li[1]), target };
      if (current) current.items.push(item);
      else flat.push(item);
    }
  }
  return { flat, groups, hasSubgroups: groups.length > 0 };
}

// ---------------------------------------------------------------------------
// Sidebar construction (depends on the docs/ filesystem)
// ---------------------------------------------------------------------------

function readIndex(dirOutRel, locale) {
  const abs = locale === 'ru'
    ? path.join(DOCS_ROOT, 'i18n', 'ru', dirOutRel, 'index.md')
    : path.join(DOCS_ROOT, dirOutRel, 'index.md');
  return existsSync(abs) ? readFileSyncCached(abs) : null;
}

// tiny sync read cache to avoid re-reading indexes during recursion
const _cache = new Map();
function readFileSyncCached(abs) {
  if (!_cache.has(abs)) _cache.set(abs, readFileSync(abs, 'utf8'));
  return _cache.get(abs);
}

function withinSubtree(dirSrcAbs, target) {
  const rel = path.relative(dirSrcAbs, path.resolve(dirSrcAbs, target));
  return !rel.startsWith('..');
}

/** Build a Starlight group for a flat-index directory (reference, config, …). */
function buildGroup(dirOutRel, label, labelRu, includeIndex) {
  const enMd = readIndex(dirOutRel, 'en');
  const en = parseIndex(enMd ?? '');
  const ru = parseIndex(readIndex(dirOutRel, 'ru') ?? '');
  const ruByTarget = new Map(ru.flat.map((c) => [c.target, c.label]));
  const dirSrcAbs = path.join(DOCS_ROOT, dirOutRel);

  const items = [];
  if (includeIndex) items.push({ slug: slugFor(`${dirOutRel}/index.md`) });

  for (const child of en.flat) {
    if (!child.target.endsWith('.md')) continue;
    if (!withinSubtree(dirSrcAbs, child.target)) continue; // drops ../ cross-links

    const abs = path.resolve(dirSrcAbs, child.target);
    const relFromDocs = path.relative(DOCS_ROOT, abs).split(path.sep).join('/');
    const ruLabel = groupLabel(ruByTarget.get(child.target) ?? child.label);

    if (path.basename(child.target) === 'index.md') {
      const childDirOutRel = relFromDocs.replace(/\/index\.md$/, '');
      items.push(buildGroup(childDirOutRel, groupLabel(child.label), ruLabel, true));
    } else {
      items.push({ slug: slugFor(relFromDocs) });
    }
  }
  return { label, translations: { ru: labelRu }, items };
}

/** Build the Guides group from its ###-subgrouped index. */
function buildGuides(label, labelRu) {
  const en = parseIndex(readIndex('guides', 'en') ?? '');
  const ru = parseIndex(readIndex('guides', 'ru') ?? '');
  const dirSrcAbs = path.join(DOCS_ROOT, 'guides');

  const items = en.groups.map((g, i) => {
    const subItems = g.items
      .filter((it) => it.target.endsWith('.md') && withinSubtree(dirSrcAbs, it.target))
      .map((it) => ({ slug: slugFor('guides/' + it.target.replace(/^\.\//, '')) }));
    return {
      label: cleanLabel(g.label),
      translations: { ru: cleanLabel(ru.groups[i]?.label ?? g.label) },
      items: subItems,
    };
  });
  return { label, translations: { ru: labelRu }, items };
}

export function buildSidebar() {
  _cache.clear();
  return TOP_GROUPS.map(({ dir, label, labelRu }) =>
    dir === 'guides' ? buildGuides(label, labelRu) : buildGroup(dir, label, labelRu, false),
  );
}

// ---------------------------------------------------------------------------
// Filesystem driver
// ---------------------------------------------------------------------------

async function walkMarkdown(dirAbs) {
  const out = [];
  async function recurse(d) {
    for (const entry of await readdir(d, { withFileTypes: true })) {
      const full = path.join(d, entry.name);
      if (entry.isDirectory()) await recurse(full);
      else if (entry.name.endsWith('.md')) out.push(full);
    }
  }
  if (existsSync(dirAbs)) await recurse(dirAbs);
  return out;
}

function transform(md, srcFileAbs, onMissing, preferLocale) {
  const { title, body } = extractTitleAndBody(md, path.relative(DOCS_ROOT, srcFileAbs));
  let out = stripLanguageLine(body);
  out = rewriteLinks(out, { srcFileAbs, onMissing, preferLocale });
  out = out.replace(/^\n+/, '');
  return `---\ntitle: ${yamlQuote(title)}\n---\n\n${out}`;
}

/**
 * Root landing page, per locale, built from the repo README (NOT the splash
 * template — so the nav sidebar shows). Strips the language line and the
 * "> Translated from:" provenance note, derives the title from the H1, rewrites
 * links (docs -> site slugs, other repo files -> GitHub blobs), and rewrites
 * `<img>` srcs (the logo) to base URLs while recording the files to copy.
 */
function transformReadme(md, srcFileAbs, desc, preferLocale, onMissing, onCopy) {
  md = md.replace(/^>\s*Translated from:.*$\n?/m, '');
  // The centered hero wordmark stays in the homepage content (the small square
  // mark in the header is a different asset); rewriteImages ships + base-rewrites it.
  const { title, body } = extractTitleAndBody(md, path.relative(REPO_ROOT, srcFileAbs));
  let out = stripLanguageLine(body);
  out = rewriteImages(out, { srcFileAbs, onCopy });
  out = rewriteLinks(out, { srcFileAbs, preferLocale, onMissing });
  out = out.replace(/^\n+/, '');
  return `---\ntitle: ${yamlQuote(title)}\ndescription: ${yamlQuote(desc)}\n---\n\n${out}`;
}

/** Current version label, mirroring the Makefile's VERSION derivation. */
export function gitVersion(cwd = REPO_ROOT, run = execFileSync) {
  try {
    return run('git', ['describe', '--tags', '--always', '--dirty'], { cwd, encoding: 'utf8' }).trim() || 'dev';
  } catch {
    return 'dev';
  }
}

const SOURCES = [
  { srcRel: 'reference', locale: 'en' },
  { srcRel: 'guides', locale: 'en' },
  { srcRel: 'i18n/ru/reference', locale: 'ru' },
  { srcRel: 'i18n/ru/guides', locale: 'ru' },
];

const ROOTS = [
  {
    srcAbs: path.join(REPO_ROOT, 'README.md'),
    outRel: 'index.md',
    locale: 'en',
    desc: 'A single-binary CLI for declarative, containerised local development environments.',
  },
  {
    srcAbs: path.join(DOCS_ROOT, 'i18n', 'ru', 'README.md'),
    outRel: 'ru/index.md',
    locale: 'ru',
    desc: 'Однобинарный CLI для декларативных контейнеризированных локальных окружений разработки.',
  },
];

async function main() {
  await rm(OUT_DIR, { recursive: true, force: true });
  await mkdir(OUT_DIR, { recursive: true });

  let count = 0;
  const missing = [];
  const onMissing = (m) => missing.push(m);
  for (const { srcRel, locale } of SOURCES) {
    const dirAbs = path.join(DOCS_ROOT, srcRel);
    for (const srcFileAbs of await walkMarkdown(dirAbs)) {
      const relFromDocs = path.relative(DOCS_ROOT, srcFileAbs).split(path.sep).join('/');
      const outRel = outputRelFor(relFromDocs);
      const destAbs = path.join(OUT_DIR, outRel);
      await mkdir(path.dirname(destAbs), { recursive: true });
      await writeFile(destAbs, transform(await readFile(srcFileAbs, 'utf8'), srcFileAbs, onMissing, locale));
      count++;
    }
  }

  // Root landing pages from the README (per locale) + any images they reference.
  const images = new Map(); // basename -> source abs path
  const onCopy = ({ abs, base }) => images.set(base, abs);
  await mkdir(path.join(OUT_DIR, 'ru'), { recursive: true });
  let roots = 0;
  for (const { srcAbs, outRel, locale, desc } of ROOTS) {
    if (!existsSync(srcAbs)) continue;
    const out = transformReadme(await readFile(srcAbs, 'utf8'), srcAbs, desc, locale, onMissing, onCopy);
    await writeFile(path.join(OUT_DIR, outRel), out);
    roots++;
  }

  await rm(PUBLIC_DIR, { recursive: true, force: true });
  if (images.size) {
    await mkdir(PUBLIC_DIR, { recursive: true });
    for (const [base, abs] of images) await copyFile(abs, path.join(PUBLIC_DIR, base));
  }

  // Header logo (Starlight `logo` config imports it from src/assets/).
  let hasLogo = false;
  if (existsSync(LOGO_SRC)) {
    await mkdir(ASSETS_DIR, { recursive: true });
    await copyFile(LOGO_SRC, LOGO_OUT);
    hasLogo = true;
  }

  // Version label shown in the header (SiteTitle override imports it).
  const version = gitVersion();
  await writeFile(
    VERSION_OUT,
    `// Generated by scripts/sync-docs.mjs — DO NOT EDIT.\nexport const VERSION = ${JSON.stringify(version)};\n`,
  );

  const sidebar = buildSidebar();
  await writeFile(
    SIDEBAR_OUT,
    `// Generated by scripts/sync-docs.mjs — DO NOT EDIT.\nexport default ${JSON.stringify(sidebar, null, 2)};\n`,
  );

  console.log(`[sync-docs] transformed ${count} pages + ${roots} root page(s); copied ${images.size} image(s); logo=${hasLogo}; version=${version}; wrote sidebar (${sidebar.length} top groups).`);

  if (missing.length) {
    console.warn(`\n[sync-docs] WARNING: ${missing.length} dangling .md link(s) in canonical docs (degraded to plain text on the site):`);
    for (const m of missing) console.warn(`  - ${m.from}: ${m.href}`);
    console.warn('  Fix the links in docs/ to restore them (the site build still succeeds).');
  }
}

// Run only when invoked directly (not when imported by the test file).
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((err) => {
    console.error('[sync-docs] failed:', err.message);
    process.exit(1);
  });
}
