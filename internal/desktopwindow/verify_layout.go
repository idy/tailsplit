package desktopwindow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// Exercise the real panel's categories, inner scrolling and save-bar alignment.
func verifyContentLayouts(ctx context.Context, window *mygo.Window, directory string) ([]any, error) {
	bounds := window.ContentBounds()
	defer window.SetContentBounds(bounds)
	var results []any
	for _, width := range []int{760, 1120, 1680} {
		window.SetContentSize(width, 560)
		value, err := window.Page().EvalContext(ctx, `(async () => {
			const check = (ok, message) => { if (!ok) throw new Error(message); };
			const tick = () => Promise.race([new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))), new Promise(resolve => setTimeout(resolve, 100))]);
			const until = async predicate => {
				const deadline = performance.now() + 5000;
				while (!predicate()) {
					check(performance.now() < deadline, 'editor mode did not finish switching');
					await new Promise(resolve => setTimeout(resolve, 20));
				}
				await tick();
			};
			const body = document.querySelector('#workspace-body'), bar = document.querySelector('#titlebar');
			const footer = document.querySelector('#actionbar'), column = document.querySelector('#page-settings');
			document.querySelector('[data-page="settings"]').click();
			document.querySelector('#form-tab').click();
			await tick();
			const expected = column.getBoundingClientRect();
			const categories = [];
			for (const name of ['general', 'network', 'dns', 'routing', 'advanced', 'about']) {
				document.querySelector('[data-section="' + name + '"]').click();
				await tick();
				const section = document.querySelector('#section-' + name);
				check(section.getClientRects().length, 'missing category: ' + name);
				const rect = column.getBoundingClientRect();
				check(Math.abs(rect.left - expected.left) < 1 && Math.abs(rect.width - expected.width) < 1, 'category width changes: ' + name);
				check(body.scrollWidth <= body.clientWidth, 'category overflows horizontally: ' + name);
				categories.push(name);
			}
			document.querySelector('#yaml-tab').click();
			await until(() => !document.querySelector('#yaml-editor').hidden && !document.querySelector('#form-tab').disabled);
			const raw = document.querySelector('#yaml-editor').getBoundingClientRect();
			check(Math.abs(raw.width - expected.width) < 1, 'YAML uses a different content width');
			document.querySelector('#form-tab').click();
			await until(() => !document.querySelector('#form-editor').hidden && !document.querySelector('#form-tab').disabled);
			document.querySelector('[data-section="routing"]').click();
			await until(() => document.querySelector('#section-routing').getClientRects().length);
			body.scrollTop = 0;
			await tick();
			const footerBefore = footer.getBoundingClientRect();
			const titleBefore = bar.getBoundingClientRect();
			check(body.scrollHeight > body.clientHeight, 'long Routing page is not scrollable');
			const dropdown = document.querySelector('#default-exit').closest('.field-control').querySelector('[role="combobox"]');
			if (dropdown && !dropdown.disabled) {
				dropdown.click();
				check(dropdown.getAttribute('aria-expanded') === 'true', 'routing dropdown did not open');
				body.scrollTop = 40;
				await tick();
				check(dropdown.getAttribute('aria-expanded') === 'false', 'dropdown detached from its scrolled field');
			}
			body.scrollTop = body.scrollHeight;
			await tick();
			const footerAfter = footer.getBoundingClientRect(), titleAfter = bar.getBoundingClientRect();
			check(body.scrollTop > 0, 'workspace did not scroll');
			check(scrollY === 0 && document.documentElement.scrollHeight === innerHeight, 'root document scrolls');
			check(titleAfter.top === 0 && titleAfter.bottom === titleBefore.bottom, 'titlebar moved during scrolling');
			check(footerAfter.top === footerBefore.top && Math.abs(footerAfter.bottom - innerHeight) < 1, 'save bar moved during scrolling');
			check(body.getBoundingClientRect().top === titleAfter.bottom, 'scrollbar reaches into titlebar');
			const scrolling = {scroll_top: body.scrollTop, scroll_height: body.scrollHeight, client_height: body.clientHeight, titlebar_bottom: titleAfter.bottom, footer_top: footerAfter.top};
			return {width: innerWidth, height: innerHeight, categories, content_width: expected.width, content_left: expected.left, yaml_width: raw.width, scrolling};
		})()`)
		if err != nil {
			return nil, fmt.Errorf("%d content layout: %w", width, err)
		}
		picture, err := window.CapturePage()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("content-scroll-%d.png", width)), picture, 0o600); err != nil {
			return nil, err
		}
		results = append(results, value)
	}
	_, err := window.Page().EvalContext(ctx, `(async () => {
		document.querySelector('[data-page="logs"]').click();
		await Promise.race([new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))), new Promise(resolve => setTimeout(resolve, 100))]);
		const body = document.querySelector('#workspace-body'), output = document.querySelector('#desktop-log-text');
		if (body.scrollHeight > body.clientHeight + 1 || body.scrollTop !== 0) throw new Error('logs has a second outer scrollbar');
		if (getComputedStyle(output).overflowY !== 'auto' || output.getBoundingClientRect().bottom > body.getBoundingClientRect().bottom) throw new Error('logs lost its own bounded scroller');
		document.querySelector('[data-page="settings"]').click();
		document.querySelector('[data-section="general"]').click();
		document.querySelector('#workspace-body').scrollTop = 0;
		document.querySelector('[data-page="connection"]').click();
	})()`)
	return results, err
}
