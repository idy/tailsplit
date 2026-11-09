import { win32 } from 'node:path';

// Use npm's JavaScript entry on Windows; execFile cannot execute npm.cmd.
export function npmInvocation({ platform = process.platform, execPath = process.execPath, env = process.env } = {}) {
  if (platform !== 'win32') return { command: 'npm', args: [] };
  const cli = env.npm_execpath || win32.join(win32.dirname(execPath), 'node_modules', 'npm', 'bin', 'npm-cli.js');
  return { command: execPath, args: [cli] };
}

export const normalizeSource = (source) => source.replace(/\r\n?/g, '\n');

function replaceOnce(source, before, after) {
  if (source.split(before).length !== 2) throw new Error('Upstream Peetty changed; review the TailSplit adaptation.');
  return source.replace(before, after);
}

export function adaptPet(source) {
  source = normalizeSource(source);
  source = replaceOnce(source, '  private enter() {', `  /** Instantly choose the nearest safe spot; explicit moves choose another surface. */
  private settle(exclude: Element | null = null, another = false): boolean {
    let best: { perch: Perch; x: number; distance: number } | null = null;
    const opts = this.perchOptions;
    for (const perch of this.scan(true)) {
      if (!perch.clear || (another && perch.el === this.perch?.el)) continue;
      if (exclude && (perch.el === exclude || (perch.el && exclude.contains(perch.el)))) continue;
      let x = clamp(this.x, perch.left, perch.right);
      if (!clearAt(perch, x, opts)) {
        // A span's center can be supported while its rounded endpoints are not.
        let supported = (perch.left + perch.right) / 2;
        if (!clearAt(perch, supported, opts)) continue;
        let blocked = x;
        for (let step = 0; step < 8 && Math.abs(supported - blocked) > 0.5; step++) {
          const point = (supported + blocked) / 2;
          if (clearAt(perch, point, opts)) supported = point;
          else blocked = point;
        }
        x = supported;
      }
      const distance = Math.hypot(x - this.x, perch.y - this.y);
      if (!best || distance < best.distance) best = { perch, x, distance };
    }
    if (!best) { if (!another) this.park(); return false; }
    this.overlay!.host.style.visibility = 'visible';
    this.setPetVisible(true);
    this.x = best.x;
    this.y = best.perch.y;
    this.land(best.perch, 0);
    this.place();
    return true;
  }

  private enter() {
    if (this.view!.reducedMotion) { this.settle(); return; }`);
  source = replaceOnce(source,
    '  private leap(exclude: Element | null = null): boolean {',
    '  private leap(exclude: Element | null = null): boolean {\n    if (this.view!.reducedMotion) return this.settle(exclude, true);');
  source = replaceOnce(source,
    '  private startFall(calm = false) {',
    '  private startFall(calm = false) {\n    if (this.view?.reducedMotion) { this.settle(); return; }');
  return replaceOnce(source,
    '    const now = this.clock;\n    switch (this.mode) {',
    '    const now = this.clock;\n' +
    "    if (this.view.reducedMotion && ['jump', 'fall', 'hang'].includes(this.mode)) this.settle();\n" +
    "    if (this.view.reducedMotion && this.mode === 'ground' && this.action === 'walk') this.setAction('idle', 2);\n" +
    '    switch (this.mode) {');
}

export function adaptPerches(source) {
  return replaceOnce(normalizeSource(source),
    '      if (text.length > MAX_TEXT || !spend(result.budget, Math.ceil(text.length / 64))) { result.complete = false; break; }',
    '      if (text.length > MAX_TEXT) {\n' +
    '        // Bound work for long logs: one conservative, clipped text obstacle.\n' +
    '        range ??= document.createRange();\n        range.selectNodeContents(node);\n' +
    '        addObstacle(range.getBoundingClientRect(), null, false, context.flow);\n        continue;\n      }\n' +
    '      if (!spend(result.budget, Math.ceil(text.length / 64))) { result.complete = false; break; }');
}
