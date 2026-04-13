/**
 * generate-icon-data.mjs
 *
 * Build-time script that reads @phosphor-icons/core metadata and SVG assets,
 * then outputs:
 *   - public/icons/{weight}.json      (keyed by PascalName, value = path string or [paths] for duotone)
 *   - src/features/workspace/generated/icon-meta.json   (array of {n, t, c})
 *   - src/features/workspace/generated/icon-names.ts    (exports ICON_NAMES, ICON_NAME_SET)
 */

import { readFileSync, writeFileSync, mkdirSync } from 'fs';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';
import { createRequire } from 'module';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const ROOT = resolve(__dirname, '..');

// ---- helpers ----------------------------------------------------------------

function ensureDir(dir) {
  mkdirSync(dir, { recursive: true });
}

/**
 * Extract path data from an SVG file.
 * Returns an array where each element is either:
 *   - a plain string (no opacity attribute), or
 *   - an object { d: string, o: number } (when opacity attribute is present)
 */
function extractPaths(svgContent) {
  const paths = [];
  // Match all <path ...> elements and capture the attributes
  const pathRe = /<path\b([^>]*)>/gi;
  let match;
  while ((match = pathRe.exec(svgContent)) !== null) {
    const attrs = match[1];
    const dMatch = /\bd="([^"]*)"/.exec(attrs);
    if (dMatch) {
      const opacityMatch = /\bopacity="([^"]*)"/.exec(attrs);
      if (opacityMatch) {
        paths.push({ d: dMatch[1], o: parseFloat(opacityMatch[1]) });
      } else {
        paths.push(dMatch[1]);
      }
    }
  }
  return paths;
}

// ---- load icon metadata from @phosphor-icons/core ---------------------------

const coreModule = resolve(ROOT, 'node_modules/@phosphor-icons/core/dist/index.mjs');
const { icons } = await import(coreModule);

console.log(`Loaded ${icons.length} icons from @phosphor-icons/core`);

// ---- weight → filename suffix mapping ---------------------------------------

const WEIGHTS = ['bold', 'regular', 'thin', 'light', 'fill', 'duotone'];

/**
 * For a given kebab-case icon name and weight, return the SVG filename (no path).
 * Regular weight: {name}.svg (no suffix)
 * All others:     {name}-{weight}.svg
 */
function svgFilename(name, weight) {
  if (weight === 'regular') {
    return `${name}.svg`;
  }
  return `${name}-${weight}.svg`;
}

// ---- generate per-weight JSON files -----------------------------------------

const assetsBase = resolve(ROOT, 'node_modules/@phosphor-icons/core/assets');
const publicIconsDir = resolve(ROOT, 'public/icons');
ensureDir(publicIconsDir);

for (const weight of WEIGHTS) {
  const weightDir = resolve(assetsBase, weight);
  const result = {};

  let missing = 0;

  for (const icon of icons) {
    const filename = svgFilename(icon.name, weight);
    const svgPath = resolve(weightDir, filename);

    let svgContent;
    try {
      svgContent = readFileSync(svgPath, 'utf8');
    } catch {
      // SVG file not found — skip silently (shouldn't happen for well-formed package)
      missing++;
      continue;
    }

    const paths = extractPaths(svgContent);

    if (weight === 'duotone') {
      // Duotone may have 1 path (no opacity layer) or 2 paths (with opacity layer)
      result[icon.pascal_name] = paths.length === 1 ? paths[0] : paths;
    } else {
      // All other weights always have exactly 1 path
      result[icon.pascal_name] = paths[0] ?? '';
    }
  }

  const outputPath = resolve(publicIconsDir, `${weight}.json`);
  writeFileSync(outputPath, JSON.stringify(result));
  const count = Object.keys(result).length;
  console.log(`  ${weight}.json: ${count} icons${missing > 0 ? ` (${missing} missing)` : ''}`);
}

// ---- generate icon-meta.json ------------------------------------------------

const generatedDir = resolve(ROOT, 'src/features/workspace/generated');
ensureDir(generatedDir);

/**
 * Filter out `*new*` tag (Phosphor uses it as a marker, not a real tag).
 */
function filterTags(tags) {
  return (tags ?? []).filter(t => t !== '*new*');
}

const metaArray = icons.map(icon => ({
  n: icon.pascal_name,
  t: filterTags(icon.tags),
  c: icon.categories ?? [],
}));

const metaPath = resolve(generatedDir, 'icon-meta.json');
writeFileSync(metaPath, JSON.stringify(metaArray));
console.log(`  icon-meta.json: ${metaArray.length} entries`);

// ---- generate icon-names.ts -------------------------------------------------

const iconNames = icons.map(icon => icon.pascal_name);

const namesTs = `// AUTO-GENERATED — do not edit manually.
// Run: pnpm generate:icons

export const ICON_NAMES: string[] = ${JSON.stringify(iconNames, null, 2)};

export const ICON_NAME_SET: Set<string> = new Set(ICON_NAMES);
`;

const namesPath = resolve(generatedDir, 'icon-names.ts');
writeFileSync(namesPath, namesTs);
console.log(`  icon-names.ts: ${iconNames.length} names`);

console.log('Done.');
