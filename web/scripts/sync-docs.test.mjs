import assert from 'node:assert/strict';
import path from 'node:path';
import { test } from 'node:test';

import {
  BASE,
  DOCS_ROOT,
  REPO_ROOT,
  classify,
  cleanLabel,
  extractTitleAndBody,
  gitVersion,
  groupLabel,
  outputRelFor,
  parseIndex,
  rewriteImages,
  rewriteLinks,
  siteLink,
  slugFor,
  stripLanguageLine,
  stripProvenanceLine,
  yamlQuote,
} from './sync-docs.mjs';

test('cleanLabel strips heading markers and backticks', () => {
  assert.equal(cleanLabel('# `render env`'), 'render env');
  assert.equal(cleanLabel('deploy.yml / reset.yml'), 'deploy.yml / reset.yml');
});

test('groupLabel drops trailing path-hint parentheticals but keeps file names', () => {
  assert.equal(groupLabel('Concepts (`concepts/`)'), 'Concepts');
  assert.equal(groupLabel('Render packs (`render/`)'), 'Render packs');
  assert.equal(groupLabel('deploy.yml / reset.yml'), 'deploy.yml / reset.yml');
  assert.equal(groupLabel('commands/'), 'commands/');
});

test('yamlQuote escapes quotes and backslashes', () => {
  assert.equal(yamlQuote('a "b" c'), '"a \\"b\\" c"');
  assert.equal(yamlQuote('workspace.yml / defaults.yml'), '"workspace.yml / defaults.yml"');
});

test('extractTitleAndBody pulls H1 and removes that line', () => {
  const { title, body } = extractTitleAndBody('# Title\n\nintro\n');
  assert.equal(title, 'Title');
  assert.ok(!body.includes('# Title'));
  assert.ok(body.includes('intro'));
});

test('extractTitleAndBody throws when no H1', () => {
  assert.throws(() => extractTitleAndBody('no heading here', 'x.md'), /no H1/);
});

test('stripLanguageLine removes EN and RU language lines', () => {
  const en = '# T\n\n*Languages: **English** · [Русский](../i18n/ru/x.md)*\n\nbody\n';
  const ru = '# T\n\n*Языки: [English](../../../x.md) · **Русский***\n\nbody\n';
  assert.ok(!stripLanguageLine(en).includes('Languages'));
  assert.ok(!stripLanguageLine(ru).includes('Языки'));
  assert.ok(stripLanguageLine(en).includes('body'));
});

test('stripProvenanceLine removes the "Translated from:" note', () => {
  const md = '> Translated from: reference/index.md @ fd1149875211\n\n# T\n\nbody\n';
  const out = stripProvenanceLine(md);
  assert.ok(!out.includes('Translated from'));
  assert.ok(out.includes('# T'));
  assert.ok(out.includes('body'));
});

test('outputRelFor remaps the ru i18n tree', () => {
  assert.equal(outputRelFor('reference/config/workspace.md'), 'reference/config/workspace.md');
  assert.equal(outputRelFor('i18n/ru/reference/config/workspace.md'), 'ru/reference/config/workspace.md');
});

test('slugFor drops extension and trailing index', () => {
  assert.equal(slugFor('reference/render/config.md'), 'reference/render/config');
  assert.equal(slugFor('reference/config/index.md'), 'reference/config');
  assert.equal(slugFor('ru/guides/index.md'), 'ru/guides');
});

test('siteLink is base-prefixed with trailing slash + anchor', () => {
  assert.equal(siteLink('reference/render/config', '#x'), `${BASE}reference/render/config/#x`);
  assert.equal(siteLink('reference'), `${BASE}reference/`);
});

test('classify buckets published / excluded / other', () => {
  assert.equal(classify('reference/config/workspace.md'), 'published');
  assert.equal(classify('guides/add-a-service.md'), 'published');
  assert.equal(classify('i18n/ru/reference/index.md'), 'published');
  assert.equal(classify('internals/packages.md'), 'excluded');
  assert.equal(classify('plans/foo.md'), 'excluded');
  assert.equal(classify('README.md'), 'other');
});

// rewriteLinks with an injected `exists` so we never touch the real fs.
const fakeSrc = path.join(DOCS_ROOT, 'reference', 'config', 'index.md');
const exists = () => true;

test('rewriteLinks rewrites a published cross-tree link to a base slug', () => {
  const out = rewriteLinks('see [render](../render/config.md#x)', { srcFileAbs: fakeSrc, exists });
  assert.equal(out, `see [render](${BASE}reference/render/config/#x)`);
});

test('rewriteLinks resolves a same-dir leaf link', () => {
  const out = rewriteLinks('[ws](workspace.md)', { srcFileAbs: fakeSrc, exists });
  assert.equal(out, `[ws](${BASE}reference/config/workspace/)`);
});

test('rewriteLinks preserves a ru source link under the ru slug', () => {
  const ruSrc = path.join(DOCS_ROOT, 'i18n', 'ru', 'reference', 'index.md');
  const out = rewriteLinks('[guides](../guides/index.md)', { srcFileAbs: ruSrc, exists });
  assert.equal(out, `[guides](${BASE}ru/guides/)`);
});

test('rewriteLinks sends internals links to GitHub blob', () => {
  const out = rewriteLinks('[pkgs](../../internals/packages.md#x)', { srcFileAbs: fakeSrc, exists });
  assert.equal(out, '[pkgs](https://github.com/semsemyonoff/dwe/blob/main/docs/internals/packages.md#x)');
});

test('rewriteLinks leaves external, mailto and pure-anchor links alone', () => {
  const input = '[a](https://x.io) [b](mailto:x@y.z) [c](#section) [d](./img.svg)';
  assert.equal(rewriteLinks(input, { srcFileAbs: fakeSrc, exists }), input);
});

test('rewriteLinks degrades a missing doc link to plain text and reports it', () => {
  const seen = [];
  const out = rewriteLinks('see [gone](./nope.md) end', {
    srcFileAbs: fakeSrc,
    exists: () => false,
    onMissing: (m) => seen.push(m),
  });
  assert.equal(out, 'see gone end');
  assert.equal(seen.length, 1);
  assert.match(seen[0].href, /nope\.md/);
});

// README-specific rewriting (links resolve from the repo root; non-docs repo
// files become GitHub blob URLs; images are copied + base-rewritten).
const readmeSrc = path.join(REPO_ROOT, 'README.md');

test('rewriteLinks sends a README docs link to a site slug', () => {
  const out = rewriteLinks('[bridge](docs/reference/concepts/bridge.md)', { srcFileAbs: readmeSrc, exists });
  assert.equal(out, `[bridge](${BASE}reference/concepts/bridge/)`);
});

test('rewriteLinks blobs README links to non-docs repo files', () => {
  const skill = rewriteLinks('[skill](skills/dwe/SKILL.md)', { srcFileAbs: readmeSrc, exists });
  assert.equal(skill, '[skill](https://github.com/semsemyonoff/dwe/blob/main/skills/dwe/SKILL.md)');
  const license = rewriteLinks('[License](LICENSE)', { srcFileAbs: readmeSrc, exists });
  assert.equal(license, '[License](https://github.com/semsemyonoff/dwe/blob/main/LICENSE)');
});

test('rewriteLinks leaves links outside the repo unchanged', () => {
  const out = rewriteLinks('[up](../../elsewhere/x.md)', { srcFileAbs: readmeSrc, exists });
  assert.equal(out, '[up](../../elsewhere/x.md)');
});

test('preferLocale=ru redirects an English-tree link to the RU page when it exists', () => {
  const ruReadme = path.join(DOCS_ROOT, 'i18n', 'ru', 'README.md');
  // RU README path "../../reference/..." resolves into the English tree.
  const out = rewriteLinks('[bridge](../../reference/concepts/bridge.md)', {
    srcFileAbs: ruReadme,
    preferLocale: 'ru',
    exists,
  });
  assert.equal(out, `[bridge](${BASE}ru/reference/concepts/bridge/)`);
});

test('preferLocale=ru falls back to English when no RU page exists', () => {
  const ruReadme = path.join(DOCS_ROOT, 'i18n', 'ru', 'README.md');
  const onlyEnglish = (p) => !String(p).includes(`${path.sep}i18n${path.sep}ru${path.sep}reference`);
  const out = rewriteLinks('[x](../../reference/x.md)', {
    srcFileAbs: ruReadme,
    preferLocale: 'ru',
    exists: onlyEnglish,
  });
  assert.equal(out, `[x](${BASE}reference/x/)`);
});

test('rewriteImages copies an in-repo image and base-rewrites the src', () => {
  const copied = [];
  const out = rewriteImages('<div><img src="assets/logo.png" alt="x" width="500"/></div>', {
    srcFileAbs: readmeSrc,
    exists,
    onCopy: (c) => copied.push(c),
  });
  assert.equal(out, `<div><img src="${BASE}logo.png" alt="x" width="500"/></div>`);
  assert.equal(copied.length, 1);
  assert.equal(copied[0].base, 'logo.png');
});

test('rewriteImages leaves external image srcs alone', () => {
  const input = '<img src="https://x.io/a.png"/>';
  assert.equal(rewriteImages(input, { srcFileAbs: readmeSrc, exists }), input);
});

test('gitVersion returns the describe output, or "dev" on failure', () => {
  assert.equal(gitVersion('/x', () => 'v1.2.3\n'), 'v1.2.3');
  assert.equal(gitVersion('/x', () => { throw new Error('no git'); }), 'dev');
  assert.equal(gitVersion('/x', () => '  '), 'dev');
});

test('parseIndex reads a flat "Pages" section, skipping Contents and Related', () => {
  const md = [
    '# Config',
    '## Contents',
    '- [Anchor](#file-inventory)',
    '## Pages',
    '- [workspace](workspace.md) — x',
    '- [services](services/index.md) — y',
    '- [Templates](../templates.md) — out of subtree, still parsed here',
    '## Related commands',
    '- [other](../other.md)',
  ].join('\n');
  const { flat, hasSubgroups } = parseIndex(md);
  assert.equal(hasSubgroups, false);
  assert.deepEqual(flat.map((c) => c.target), ['workspace.md', 'services/index.md', '../templates.md']);
});

test('parseIndex captures ### subgroups (guides shape)', () => {
  const md = [
    '# Guides',
    '## Pages',
    '### Starting',
    '- [a](a.md)',
    '### Authoring',
    '- [b](b.md)',
    '- [c](c.md)',
    '## See also',
    '- [ref](../reference/index.md)',
  ].join('\n');
  const { groups, hasSubgroups } = parseIndex(md);
  assert.equal(hasSubgroups, true);
  assert.deepEqual(groups.map((g) => g.label), ['Starting', 'Authoring']);
  assert.deepEqual(groups[1].items.map((i) => i.target), ['b.md', 'c.md']);
});

test('parseIndex recognises a Russian child-list heading', () => {
  const md = '# Заголовок\n## Страницы\n- [Поля](fields.md)\n';
  assert.deepEqual(parseIndex(md).flat.map((c) => c.target), ['fields.md']);
});
