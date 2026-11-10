// Restore the pinned upstream files, or verify the local copy without network.
import { createHash } from 'node:crypto';
import { readFile, writeFile, mkdir } from 'node:fs/promises';
import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = new URL('../internal/configweb/assets/vendor/magpie/', import.meta.url);
const manifest = JSON.parse(await readFile(new URL('manifest.json', root), 'utf8'));
const check = process.argv.includes('--check');
const files = Object.entries(manifest.files);
// Workers share one iterator, keeping at most four files in flight.
const queue = files.values();
await Promise.all(Array.from({ length: 4 }, async () => {
  for (const [path, hash] of queue) {
    if (path.split('/').includes('..') || path.startsWith('/')) throw new Error(`Invalid asset path: ${path}`);
    const target = new URL(path, root);
    let data;
    if (check) {
      data = await readFile(target);
    } else {
      const response = await fetch(`https://raw.githubusercontent.com/${manifest.repository}/${manifest.commit}/${path}`);
      if (!response.ok) throw new Error(`${path}: HTTP ${response.status}`);
      data = Buffer.from(await response.arrayBuffer());
    }
    if (createHash('sha256').update(data).digest('hex') !== hash) throw new Error(`Checksum mismatch: ${path}`);
    if (!check) {
      await mkdir(dirname(fileURLToPath(target)), { recursive: true });
      await writeFile(target, data);
    }
  }
}));
console.log(`Magpie ${manifest.version}: ${files.length} upstream assets ${check ? 'verified' : 'restored'}.`);
