package desktopwindow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
)

func verifyWindow(window *mygo.Window, hasTray bool, directory string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var ready bool
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for !ready {
		value, err := window.Page().EvalContext(ctx, `Boolean(document.querySelector('#loading')?.hidden && document.querySelector('#page-home')?.hidden === false && document.querySelector('#add-exit')?.disabled === false && (document.querySelector('#home-setup')?.hidden === false || document.querySelector('#home-toggle-control')?.hidden === false))`)
		ready = err == nil && value == true
		if !ready {
			select {
			case <-ctx.Done():
				return fmt.Errorf("panel did not render: %w", ctx.Err())
			case <-ticker.C:
			}
		}
	}
	value, err := window.Page().EvalContext(ctx, `(async () => {
		const check = (condition, message) => { if (!condition) throw new Error(message); };
		check(!document.querySelector('#page-home').hidden, 'Home is not the initial page');
		document.querySelector('[data-page="connection"]').click();
		check(!document.querySelector('#desktop-controls').hidden, 'desktop controls unavailable');
		check(!document.querySelector('#add-exit').disabled, 'add-exit control unavailable');
		const root = document.documentElement;
		const mac = window.mygo.platform === 'darwin';
		check(root.dataset.windowEngine === 'mygo', 'MyGo chrome was not selected');
		check(root.dataset.windowChrome === (mac ? 'macos' : 'windows'), 'wrong platform chrome');
		const titlebar = document.querySelector('#titlebar');
		const bar = titlebar.getBoundingClientRect();
		check(bar.top === 0 && bar.height === (mac ? 40 : 44), 'titlebar differs from the existing host');
		check(getComputedStyle(titlebar).getPropertyValue('--app-region').trim() === 'drag', 'titlebar is not draggable');
		check(getComputedStyle(document.querySelector('#refresh-exits')).getPropertyValue('--app-region').trim() === 'no-drag', 'toolbar button is a drag region');
		check(document.querySelector('#window-controls').hidden === mac, 'incorrect system/button arrangement');
		if (mac) check(getComputedStyle(document.querySelector('.brand')).paddingLeft === '88px', 'traffic-light inset changed');
		const mode = document.querySelector('#kill-switch-control [role="combobox"]');
		check(mode && !mode.disabled, 'titlebar mode control unavailable');
		mode.click();
		check(mode.getAttribute('aria-expanded') === 'true', 'titlebar dropdown did not open');
		mode.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
		check(mode.getAttribute('aria-expanded') === 'false', 'titlebar dropdown did not close');
		document.querySelector('[data-page="settings"]').click();
		check(!document.querySelector('#page-settings').hidden, 'settings did not open');
		document.querySelector('[data-page="logs"]').click();
		check(!document.querySelector('#page-logs').hidden, 'logs did not open');
		document.querySelector('[data-page="connection"]').click();
		const brand = document.querySelector('.brand');
		check(brand.tabIndex === -1, 'branding remains in the Tab sequence');
		brand.focus({preventScroll: true});
		check(getComputedStyle(brand).outlineStyle === 'none', 'branding still draws a focus frame');
		await document.fonts.ready;
		await Promise.race([new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))), new Promise(resolve => setTimeout(resolve, 100))]);
		return {title: document.title, desktop: true, settings: true, logs: true,
			chrome: root.dataset.windowChrome, titlebar_height: bar.height,
			drag_region: true, toolbar_click: true, traffic_light_inset: mac ? 88 : 0,
			page: document.querySelector('[aria-current="page"]').dataset.page};
	})()`)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	layouts, err := verifyTitlebarLayouts(ctx, window, directory)
	if err != nil {
		return err
	}
	content, err := verifyContentLayouts(ctx, window, directory)
	if err != nil {
		return err
	}
	focus, err := verifyExitRowFocus(ctx, window, directory)
	if err != nil {
		return err
	}
	if _, err := window.Page().EvalContext(ctx, `window.mygo.window.minimize()`); err != nil {
		return err
	}
	defer window.Restore()
	if err := waitWindowState(ctx, window.IsMinimized); err != nil {
		return fmt.Errorf("the MyGo minimize action did not minimize the window: %w", err)
	}
	window.Restore()
	if err := waitWindowState(ctx, func() bool { return !window.IsMinimized() && window.IsVisible() }); err != nil {
		return fmt.Errorf("could not restore the minimized window: %w", err)
	}
	if hasTray {
		if _, err := window.Page().EvalContext(ctx, `window.mygo.window.close()`); err != nil {
			return err
		}
		if window.IsDestroyed() || window.IsVisible() {
			return errors.New("close-to-tray did not retain a hidden window")
		}
		window.Show()
		window.Focus()
		if !window.IsVisible() {
			return errors.New("could not reopen the retained window")
		}
	}
	picture, err := window.CapturePage()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "native-window.png"), picture, 0o600); err != nil {
		return err
	}
	report, err := json.MarshalIndent(map[string]any{
		"webview": value, "layouts": layouts, "content_layouts": content, "exit_row_focus": focus, "minimize_restore": true, "tray": hasTray, "close_reopen": hasTray,
		"real_login_tested": false, "real_network_connection_tested": false,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "verification.json"), append(report, '\n'), 0o600)
}

func verifyExitRowFocus(ctx context.Context, window *mygo.Window, directory string) ([]any, error) {
	window.Focus()
	var results []any
	for _, position := range []string{"first", "middle", "last"} {
		value, err := window.Page().EvalContext(ctx, fmt.Sprintf(`(async () => {
			const buttons = [...document.querySelectorAll('.exit-trigger')].filter(element => element.getClientRects().length && !element.disabled);
			if (!buttons.length) return {skipped: 'no saved exit rows'};
			const position = %q;
			const index = position === 'first' ? 0 : position === 'last' ? buttons.length - 1 : Math.floor(buttons.length / 2);
			const button = buttons[index];
			button.focus();
			await Promise.race([new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))), new Promise(resolve => setTimeout(resolve, 100))]);
			const row = button.closest('.exit-item'), style = getComputedStyle(row);
			if (button.tabIndex !== 0 || !button.matches(':focus-visible') || style.outlineStyle !== 'solid' || style.outlineWidth !== '2px' || style.borderRadius !== '16px')
				throw new Error('keyboard focus does not outline the full rounded row');
			if (getComputedStyle(button).outlineStyle !== 'none') throw new Error('inner square focus outline is still visible');
			const highlighted = buttons.filter(element => getComputedStyle(element.closest('.exit-item')).outlineStyle === 'solid');
			if (highlighted.length !== 1 || highlighted[0] !== button) throw new Error('neighboring rows are highlighted');
			const bounds = row.getBoundingClientRect(), card = document.querySelector('#exits-card').getBoundingClientRect();
			if (bounds.left < card.left || bounds.right > card.right || bounds.top < card.top || bounds.bottom > card.bottom)
				throw new Error('focused row exceeds the list boundary');
			return {position, index, row_count: buttons.length, focused_rows: highlighted.length, full_row: true,
				radius: style.borderRadius, outline: style.outlineWidth, viewport_width: innerWidth,
				card: {x: card.x, y: card.y, width: card.width, height: card.height}};
		})()`, position))
		if err != nil {
			return nil, fmt.Errorf("%s row focus: %w", position, err)
		}
		picture, err := window.CapturePage()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(directory, "focus-row-"+position+".png"), picture, 0o600); err != nil {
			return nil, err
		}
		if position == "first" {
			if err := os.WriteFile(filepath.Join(directory, "focus-row.png"), picture, 0o600); err != nil {
				return nil, err
			}
		}
		results = append(results, value)
	}
	return results, nil
}

// Native window operations acknowledge the request before the OS finishes its
// animation. Wait for observable state instead of assuming completion on reply.
func waitWindowState(ctx context.Context, matches func() bool) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for !matches() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

func verifyTitlebarLayouts(ctx context.Context, window *mygo.Window, directory string) ([]any, error) {
	originalLanguage, err := window.Page().EvalContext(ctx, `window.TailSplitI18n.language`)
	if err != nil {
		return nil, err
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = window.Page().EvalContext(restoreCtx, fmt.Sprintf(`window.TailSplitI18n.setLanguage(%q)`, originalLanguage))
	}()
	if _, err := window.Page().EvalContext(ctx, `(async () => {
		for (const font of ['500 13px Montserrat', '500 13px Monda', '400 13px Lato', '700 12px Lato']) {
			if (!(await document.fonts.load(font, 'TailSplit')).length) throw new Error('font was not loaded: ' + font);
		}
		await document.fonts.ready;
	})()`); err != nil {
		return nil, err
	}
	var layouts []any
	for _, language := range []string{"en", "zh"} {
		if _, err := window.Page().EvalContext(ctx, fmt.Sprintf(`window.TailSplitI18n.setLanguage(%q)`, language)); err != nil {
			return nil, err
		}
		result, err := verifyLanguageLayouts(ctx, window, directory, language)
		if err != nil {
			return nil, err
		}
		layouts = append(layouts, result...)
	}
	return layouts, nil
}

func verifyLanguageLayouts(ctx context.Context, window *mygo.Window, directory, language string) ([]any, error) {
	originalBounds, originalTheme := window.ContentBounds(), mygo.Theme.Source()
	defer func() {
		window.SetContentBounds(originalBounds)
		mygo.Theme.SetSource(originalTheme)
	}()
	var layouts []any
	for _, theme := range []mygo.ThemeSource{mygo.ThemeLight, mygo.ThemeDark} {
		mygo.Theme.SetSource(theme)
		for _, width := range []int{1120, 760, 1680} {
			window.SetContentSize(width, 780)
			window.Focus()
			var baseline map[string]any
			for _, page := range []string{"connection", "settings", "logs"} {
				layout, err := window.Page().EvalContext(ctx, fmt.Sprintf(`(async () => {
					document.querySelector('[data-page="%s"]').click();
					await Promise.race([new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))), new Promise(resolve => setTimeout(resolve, 100))]);
					const bar = document.querySelector('#titlebar');
					const rectangle = bar.getBoundingClientRect();
					const visible = element => element.getClientRects().length;
					const controls = [...bar.querySelectorAll('button, [role="combobox"]')].filter(visible);
					if (document.documentElement.scrollWidth > innerWidth) throw new Error('window has horizontal overflow');
					for (const control of controls) {
						const bounds = control.getBoundingClientRect();
						if (bounds.left < 0 || bounds.right > innerWidth + 1 || bounds.top < 0 || bounds.bottom > rectangle.bottom + 1)
							throw new Error('titlebar control clipped: ' + control.id);
					}
					const refreshes = [...bar.querySelectorAll('#refresh-exits, #reload, #desktop-log-refresh')].filter(visible);
					if (refreshes.length !== 1) throw new Error('expected one titlebar refresh');
					const refresh = refreshes[0].getBoundingClientRect();
					const toggle = document.querySelector('#desktop-controls').getBoundingClientRect();
					const mode = document.querySelector('#kill-switch-control').getBoundingClientRect();
					if (mode.right > toggle.left || toggle.right > refresh.left) throw new Error('titlebar order differs from Exit Nodes');
					const update = document.querySelector('#update-pill');
					if (visible(update)) {
						const bounds = update.getBoundingClientRect();
						if (bounds.left < toggle.right || bounds.right > refresh.left || controls[controls.indexOf(update) + 1] !== refreshes[0]) throw new Error('update action is not immediately left of Refresh');
						const iconLeft = refreshes[0].querySelector('svg').getBoundingClientRect().left;
						if (Math.abs((bounds.left - toggle.right) - (iconLeft - bounds.right)) > 1) throw new Error('update action has unequal visible gaps');
					}
					if (document.querySelector('#exit-label')) throw new Error('obsolete page-specific exit label remains');
					const body = document.querySelector('#workspace-body'), content = body.querySelector('.app-page:not([hidden])');
					const area = body.getBoundingClientRect(), column = content.getBoundingClientRect();
					const maximum = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--content-max-width'));
					const gutter = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--workspace-gutter'));
					if (Math.abs(column.width - Math.min(maximum, body.clientWidth - 2 * gutter)) > 1) throw new Error('page has a different content width');
					if (Math.abs(column.left - area.left - (body.clientWidth - column.width) / 2) > 1) throw new Error('page content is not centered in workspace');
					if (area.top < rectangle.bottom || scrollY || document.documentElement.scrollHeight > innerHeight) throw new Error('document scroll reaches the titlebar');
					const footer = document.querySelector('.actionbar-content');
					if (visible(footer)) {
						const bounds = footer.getBoundingClientRect();
						if (Math.abs(bounds.left - column.left) > 1 || Math.abs(bounds.width - column.width) > 1) throw new Error('save controls do not align with content: ' + JSON.stringify({column: {left: column.left, width: column.width}, footer: {left: bounds.left, width: bounds.width}, body: {left: area.left, width: area.width, clientWidth: body.clientWidth}, actionbar: document.querySelector('#actionbar').getBoundingClientRect().toJSON()}));
					}
					const family = selector => getComputedStyle(document.querySelector(selector)).fontFamily.split(',')[0].replace(/["']/g, '').trim();
					const fonts = {brand: family('.brand-name'), title: family('#crumb-section'), button: family('#add-exit'),
						select: family('#kill-switch-control .select-trigger'), body: family('body'), note: family('.connection-exit-caption'),
						field_title: family('.field-label label'), dialog_title: family('dialog h2')};
					const expected = {brand: 'Montserrat', title: 'Monda', button: 'Montserrat', select: 'Montserrat', body: 'Lato', note: 'Lato', field_title: 'Monda', dialog_title: 'Montserrat'};
					for (const [role, name] of Object.entries(expected)) if (fonts[role] !== name) throw new Error('incorrect font for ' + role + ': ' + fonts[role]);
					for (const selector of ['#raw-yaml', '#desktop-log-text']) if (!getComputedStyle(document.querySelector(selector)).fontFamily.includes('monospace')) throw new Error('technical text lost monospace');
					return {page: '%s', language: window.TailSplitI18n.language, fonts, width: innerWidth, titlebar_height: rectangle.height, content_left: column.left, content_width: column.width,
						sidebar_width: document.querySelector('.sidebar').getBoundingClientRect().width,
						brand_inset: getComputedStyle(document.querySelector('.brand')).paddingLeft,
						canvas: getComputedStyle(bar).backgroundColor,
						dark: matchMedia('(prefers-color-scheme: dark)').matches, controls_visible: controls.length,
						refresh_id: refreshes[0].id, refresh_right: refresh.right, switch_right: toggle.right, mode_right: mode.right};
				})()`, page, page))
				if err != nil {
					return nil, fmt.Errorf("%s/%d/%s titlebar: %w", theme, width, page, err)
				}
				metrics, ok := layout.(map[string]any)
				if !ok {
					return nil, errors.New("missing titlebar layout metrics")
				}
				if baseline == nil {
					baseline = metrics
				} else {
					for _, key := range []string{"refresh_right", "switch_right", "mode_right", "content_left", "content_width"} {
						if metrics[key] != baseline[key] {
							return nil, fmt.Errorf("%s/%d/%s: %s differs from Exit Nodes (%v != %v)", theme, width, page, key, metrics[key], baseline[key])
						}
					}
				}
				picture, err := window.CapturePage()
				if err != nil {
					return nil, err
				}
				name := fmt.Sprintf("titlebar-%s-%s-%d-%s.png", language, theme, width, page)
				if err := os.WriteFile(filepath.Join(directory, name), picture, 0o600); err != nil {
					return nil, err
				}
				layouts = append(layouts, layout)
			}
		}
	}
	if _, err := window.Page().EvalContext(ctx, `document.querySelector('[data-page="connection"]').click()`); err != nil {
		return nil, err
	}
	return layouts, nil
}
