import { win32 } from 'node:path';

// Use npm's JavaScript entry on Windows; execFile cannot execute npm.cmd.
export function npmInvocation({ platform = process.platform, execPath = process.execPath, env = process.env } = {}) {
  if (platform !== 'win32') return { command: 'npm', args: [] };
  const cli = env.npm_execpath || win32.join(win32.dirname(execPath), 'node_modules', 'npm', 'bin', 'npm-cli.js');
  return { command: execPath, args: [cli] };
}

function replaceOnce(source, before, after) {
  if (source.split(before).length !== 2) throw new Error('Upstream Peetty changed; review the TailSplit adaptation.');
  return source.replace(before, after);
}

export function adaptPet(source) {
  source = replaceOnce(source,
    '    this.x = rand(target.left, target.right);\n    this.y = Math.min(target.y - 40, this.view!.height * 0.6);',
    '    this.x = rand(target.left, target.right);\n' +
    '    if (this.view!.reducedMotion) {\n' +
    '      this.y = target.y;\n      this.land(target, 0);\n      this.place();\n      return;\n    }\n' +
    '    this.y = Math.min(target.y - 40, this.view!.height * 0.6);');
  source = replaceOnce(source,
    '  private leap(exclude: Element | null = null): boolean {',
    '  private leap(exclude: Element | null = null): boolean {\n    if (this.view!.reducedMotion) return false;');
  source = replaceOnce(source,
    '  private startFall(calm = false) {',
    '  private startFall(calm = false) {\n    if (this.view?.reducedMotion) { this.enter(); return; }');
  return replaceOnce(source,
    '    const now = this.clock;\n    switch (this.mode) {',
    '    const now = this.clock;\n' +
    "    if (this.view.reducedMotion && ['jump', 'fall', 'hang'].includes(this.mode)) this.enter();\n" +
    "    if (this.view.reducedMotion && this.mode === 'ground' && this.action === 'walk') this.setAction('idle', 2);\n" +
    '    switch (this.mode) {');
}

export function adaptPerches(source) {
  return replaceOnce(source,
    '      if (text.length > MAX_TEXT || !spend(result.budget, Math.ceil(text.length / 64))) { result.complete = false; break; }',
    '      if (text.length > MAX_TEXT) {\n' +
    '        // Bound work for long logs: one conservative, clipped text obstacle.\n' +
    '        range ??= document.createRange();\n        range.selectNodeContents(node);\n' +
    '        addObstacle(range.getBoundingClientRect(), null, false, context.flow);\n        continue;\n      }\n' +
    '      if (!spend(result.budget, Math.ceil(text.length / 64))) { result.complete = false; break; }');
}
