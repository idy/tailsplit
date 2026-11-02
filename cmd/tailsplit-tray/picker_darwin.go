//go:build darwin

package main

import (
	"github.com/idy/tailsplit/internal/tray"

	"github.com/getlantern/systray"
)

// Keep one menu item per address so a status refresh cannot accumulate hidden
// copies and click handlers for the same node.
type exitPicker struct {
	parent *systray.MenuItem
	empty  *systray.MenuItem
	items  map[string]*systray.MenuItem
	clicks chan string
	done   chan struct{}
}

func newExitPicker(parent *systray.MenuItem) *exitPicker {
	empty := parent.AddSubMenuItem("登录后刷新出口列表", "")
	empty.Disable()
	return &exitPicker{
		parent: parent, empty: empty, items: make(map[string]*systray.MenuItem),
		clicks: make(chan string, 1), done: make(chan struct{}),
	}
}

func (p *exitPicker) update(snapshot tray.Snapshot, configured string, enabled bool) {
	for _, item := range p.items {
		item.Hide()
	}
	if len(snapshot.ExitNodes) == 0 {
		p.empty.Show()
	} else {
		p.empty.Hide()
	}
	directMatch := false
	for _, node := range snapshot.ExitNodes {
		directMatch = directMatch || node.ID == configured || node.Name == configured
	}
	for _, node := range snapshot.ExitNodes {
		item, exists := p.items[node.ID]
		if !exists {
			item = p.parent.AddSubMenuItemCheckbox(node.Name, "", false)
			p.items[node.ID] = item
			go func(id string, item *systray.MenuItem) {
				for {
					select {
					case <-p.done:
						return
					case <-item.ClickedCh:
						select {
						case p.clicks <- id:
						default:
						}
					}
				}
			}(node.ID, item)
		}
		label := node.Name + " · " + node.ID
		if !node.Online {
			label += "（离线或已过期）"
		}
		item.SetTitle(label)
		item.SetTooltip("选择后保存配置；正在运行的代理会重新连接")
		setChecked(item, node.ID == configured || node.Name == configured || !directMatch && node.Selected)
		setEnabled(item, enabled && node.Online)
		item.Show()
	}
}

func setChecked(item *systray.MenuItem, checked bool) {
	if checked {
		item.Check()
	} else {
		item.Uncheck()
	}
}
