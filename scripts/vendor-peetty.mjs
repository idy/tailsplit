// Rebuild the self-hosted companion without adding Node to normal Go builds.
import { execFileSync } from 'node:child_process';
import { mkdtemp, mkdir, readFile, writeFile, rm, copyFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const revision = 'ba28bf822a1fee9f1961b2427b38548f23f27612';
const root = fileURLToPath(new URL('../', import.meta.url));
const target = resolve(root, 'internal/configweb/assets/vendor/peetty');
const temporary = await mkdtemp(join(tmpdir(), 'tailsplit-peetty-'));
const source = join(temporary, 'source');
const run = (command, args) => execFileSync(command, args, { cwd: root, stdio: 'inherit' });
try {
  run('git', ['clone', '--quiet', 'https://github.com/idy/peetty.git', source]);
  run('git', ['-C', source, 'checkout', '--quiet', '--detach', revision]);
  const uiPath = join(source, 'src/pet/ui.ts');
  const ui = await readFile(uiPath, 'utf8');
  const sheet = ui.match(/const STYLE = `([\s\S]*?)`;/);
  const inline = "const style = document.createElement('style');\n  style.textContent = STYLE;";
  if (!sheet || !ui.includes(inline)) throw new Error('Upstream overlay changed; review the CSP adaptation.');
  // Shadow-root styles must obey the panel's existing style-src 'self' policy.
  const external = "const style = document.createElement('link');\n  style.rel = 'stylesheet';\n  style.href = '/vendor/peetty/peetty.css';";
  await writeFile(uiPath, ui.replace(sheet[0], '').replace(inline, external));
  await mkdir(target, { recursive: true });
  await writeFile(join(target, 'peetty.css'), sheet[1]);
  run('npm', ['exec', '--yes', '--package=esbuild@0.28.2', '--', 'esbuild', join(source, 'src/global.ts'),
    '--bundle', '--format=iife', '--global-name=Peetty', '--target=es2022', '--minify',
    '--legal-comments=none', '--external:./panel.ts', `--outfile=${join(target, 'peetty.global.js')}`,
    `--banner:js=/*! @idy/peetty 0.2.0 | MIT | ${revision} | see LICENSE.txt */`]);
  await copyFile(join(source, 'LICENSE'), join(target, 'LICENSE.txt'));
} finally {
  await rm(temporary, { recursive: true, force: true });
}
