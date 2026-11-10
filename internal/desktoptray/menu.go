// Package desktoptray adapts TailSplit's channel-based tray menus to MyGo.
package desktoptray

import (
	"strconv"
	"sync"
	"time"
)

// MenuItem keeps its identity and click channel when the native menu is rebuilt.
// As with systray, a click is delivered only if a receiver is ready; UI callbacks
// never wait for the application's menu event loop.
type MenuItem struct {
	ClickedCh chan struct{}

	owner     *menuState
	id        uint64
	parent    *MenuItem
	title     string
	tooltip   string
	disabled  bool
	hidden    bool
	checked   bool
	checkbox  bool
	separator bool
	children  []*MenuItem
}

type changes uint8

const (
	menuChanged changes = 1 << iota
	iconChanged
	tooltipChanged
	allChanged = menuChanged | iconChanged | tooltipChanged
)

// Only this model is shared with the application's event loop. Native calls
// happen after releasing mu, since MyGo may synchronously dispatch to the UI.
type menuState struct {
	mu       sync.Mutex
	items    []*MenuItem
	nextID   uint64
	icon     []byte
	template bool
	tooltip  string
	dirty    changes
	wake     chan struct{}
	stopped  bool
}

func newMenuState() *menuState { return &menuState{wake: make(chan struct{}, 1)} }

var defaultState = newMenuState()

// AddMenuItem appends an item to the tray menu.
func AddMenuItem(title, tooltip string) *MenuItem {
	return defaultState.add(nil, title, tooltip, false, false, false)
}

// AddMenuItemCheckbox appends an explicitly checked or unchecked item.
func AddMenuItemCheckbox(title, tooltip string, checked bool) *MenuItem {
	return defaultState.add(nil, title, tooltip, true, checked, false)
}

// AddSeparator appends a separator in the existing menu order.
func AddSeparator() { defaultState.add(nil, "", "", false, false, true) }

// AddSubMenuItem appends a child to this item's submenu.
func (m *MenuItem) AddSubMenuItem(title, tooltip string) *MenuItem {
	return m.owner.add(m, title, tooltip, false, false, false)
}

// AddSubMenuItemCheckbox appends a checkbox to this item's submenu.
func (m *MenuItem) AddSubMenuItemCheckbox(title, tooltip string, checked bool) *MenuItem {
	return m.owner.add(m, title, tooltip, true, checked, false)
}

func (s *menuState) add(parent *MenuItem, title, tooltip string, checkbox, checked, separator bool) *MenuItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	m := &MenuItem{
		ClickedCh: make(chan struct{}), owner: s, id: s.nextID, parent: parent,
		title: title, tooltip: tooltip, checkbox: checkbox, checked: checked, separator: separator,
	}
	if parent == nil {
		s.items = append(s.items, m)
	} else {
		parent.children = append(parent.children, m)
	}
	s.changedLocked(menuChanged)
	return m
}

func (s *menuState) changedLocked(c changes) {
	if s.stopped {
		return
	}
	s.dirty |= c
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (m *MenuItem) setString(field *string, value string) {
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	if *field != value {
		*field = value
		m.owner.changedLocked(menuChanged)
	}
}

func (m *MenuItem) setBool(field *bool, value bool) {
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	if *field != value {
		*field = value
		m.owner.changedLocked(menuChanged)
	}
}

// SetTitle changes the label without replacing the item or its click channel.
func (m *MenuItem) SetTitle(title string) { m.setString(&m.title, title) }

// SetTooltip retains the tooltip and passes it to MyGo's ToolTip field. MyGo
// v0.8.3 displays menu tooltips on macOS; its Windows menus do not render them.
func (m *MenuItem) SetTooltip(tooltip string) { m.setString(&m.tooltip, tooltip) }

func (m *MenuItem) Enable()  { m.setBool(&m.disabled, false) }
func (m *MenuItem) Disable() { m.setBool(&m.disabled, true) }
func (m *MenuItem) Show()    { m.setBool(&m.hidden, false) }
func (m *MenuItem) Hide()    { m.setBool(&m.hidden, true) }
func (m *MenuItem) Check()   { m.setBool(&m.checked, true) }
func (m *MenuItem) Uncheck() { m.setBool(&m.checked, false) }

func (m *MenuItem) Disabled() bool {
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	return m.disabled
}

func (m *MenuItem) Hidden() bool {
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	return m.hidden
}

func (m *MenuItem) Checked() bool {
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	return m.checked
}

func (m *MenuItem) Title() string {
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	return m.title
}

func (m *MenuItem) Tooltip() string {
	m.owner.mu.Lock()
	defer m.owner.mu.Unlock()
	return m.tooltip
}

func (m *MenuItem) String() string { return m.Title() }

// clicked deliberately does not toggle the model: existing business handlers
// own check state. MyGo toggles checkboxes before Click, so restore their native
// state before notifying those handlers. Never hold mu while calling restore.
func (m *MenuItem) clicked(restore func(bool)) {
	s := m.owner
	s.mu.Lock()
	checked := m.checked
	var ch chan struct{}
	if !s.stopped && !m.separator && len(m.children) == 0 {
		ch = m.ClickedCh
		for item := m; item != nil; item = item.parent {
			if item.disabled || item.hidden {
				ch = nil
				break
			}
		}
	}
	s.mu.Unlock()
	if restore != nil {
		restore(checked)
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

type itemSnapshot struct {
	id        string
	title     string
	tooltip   string
	disabled  bool
	hidden    bool
	checked   bool
	checkbox  bool
	separator bool
	children  []itemSnapshot
	item      *MenuItem
}

type traySnapshot struct {
	changed  changes
	items    []itemSnapshot
	icon     []byte
	template bool
	tooltip  string
}

func (s *menuState) takeSnapshot(force changes) traySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := traySnapshot{changed: s.dirty | force}
	s.dirty = 0
	if out.changed&menuChanged != 0 {
		out.items = snapshotItems(s.items)
	}
	if out.changed&iconChanged != 0 {
		// Stored icon bytes are immutable and already detached from the caller.
		out.icon, out.template = s.icon, s.template
	}
	if out.changed&tooltipChanged != 0 {
		out.tooltip = s.tooltip
	}
	return out
}

// snapshotItems is called with the owning menuState locked.
func snapshotItems(items []*MenuItem) []itemSnapshot {
	out := make([]itemSnapshot, len(items))
	for i, m := range items {
		out[i] = itemSnapshot{
			id: "tailsplit-menu-" + strconv.FormatUint(m.id, 10), title: m.title, tooltip: m.tooltip,
			disabled: m.disabled, hidden: m.hidden, checked: m.checked, checkbox: m.checkbox,
			separator: m.separator, children: snapshotItems(m.children), item: m,
		}
	}
	return out
}

// Refreshes often change dozens of titles, checks and visibility flags. Batch
// them into a final snapshot; the renderer skips unchanged values and updates
// native items in place. apply is only called by this worker.
func (s *menuState) renderLoop(stop <-chan struct{}, apply func(traySnapshot)) {
	for {
		select {
		case <-stop:
			return
		case <-s.wake:
		}
		timer := time.NewTimer(8 * time.Millisecond)
		select {
		case <-stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		select {
		case <-stop:
			return
		default:
		}
		// Consume the single notification accumulated during the batch. A
		// later mutation queues another wake and cannot be lost.
		select {
		case <-s.wake:
		default:
		}
		if snap := s.takeSnapshot(0); snap.changed != 0 {
			apply(snap)
		}
	}
}
