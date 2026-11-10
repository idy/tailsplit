package desktoptray

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/egoist/mygo"
)

// NewMenu registers real MyGo UIDs without starting an application. Inspecting
// the private integer here tests the callback identity, rather than the public
// string ID that the SDK does not use for native click dispatch.
func nativeUID(item *mygo.MenuItem) int64 {
	return reflect.ValueOf(item).Elem().FieldByName("uid").Int()
}

func TestSnapshotsPreserveOrderIdentityAndIndependentState(t *testing.T) {
	s := newMenuState()
	header := s.add(nil, "TailSplit", "status", false, false, false)
	header.Disable()
	s.add(nil, "", "", false, false, true)
	picker := s.add(nil, "Exit", "choose", false, false, false)
	node := picker.AddSubMenuItemCheckbox("Node A", "reconnect", true)
	other := picker.AddSubMenuItemCheckbox("Node B", "offline", false)
	other.Disable()
	other.Hide()

	before := s.takeSnapshot(allChanged)
	installs := 0
	renderer := newMenuRenderer(func(*mygo.Menu) { installs++ })
	renderer.apply(before.items)
	menu, native := renderer.menu, renderer.roots
	if native[0].Label != "TailSplit" || !native[0].Disabled || native[1].Type != mygo.MenuItemSeparator || native[2].Label != "Exit" {
		t.Fatal("root menu order, separator or disabled header changed")
	}
	nativeNode := native[2].Submenu[0]
	uid, oldChannel := nativeUID(nativeNode), node.ClickedCh
	node.SetTitle("节点 A")
	node.SetTooltip("重新连接")
	node.Uncheck()
	other.Enable()
	other.Show()
	after := s.takeSnapshot(0)
	if !renderer.apply(after.items) {
		t.Fatal("scalar changes were not applied")
	}
	if after.changed != menuChanged {
		t.Fatalf("menu mutation dirtied unrelated tray state: %b", after.changed)
	}
	for i := range before.items {
		if before.items[i].id != after.items[i].id || before.items[i].item != after.items[i].item {
			t.Fatalf("root item %d changed identity", i)
		}
	}
	if renderer.menu != menu || installs != 1 {
		t.Fatal("scalar refresh replaced the native menu")
	}
	if renderer.roots[2].Submenu[0] != nativeNode || nativeUID(nativeNode) != uid || uid == 0 || oldChannel != node.ClickedCh {
		t.Fatal("localization replaced the native item, SDK UID or click channel")
	}
	oldNode := before.items[2].children[0]
	if oldNode.title != "Node A" || oldNode.tooltip != "reconnect" || !oldNode.checked {
		t.Fatal("model mutation changed an earlier immutable snapshot")
	}
	if nativeNode.Label != "节点 A" || nativeNode.ToolTip != "重新连接" || nativeNode.Checked || nativeNode.Type != mygo.MenuItemCheckbox {
		t.Fatalf("localized checkbox state was lost: %+v", nativeNode)
	}
	if nativeNode.Subtitle != "" {
		t.Fatal("tooltip must not become visible subtitle text")
	}
	if native[2].Submenu[1].Hidden || native[2].Submenu[1].Disabled {
		t.Fatal("show/enable changes were not applied to the existing item")
	}
	if node.Checked() || node.Title() != "节点 A" || node.Tooltip() != "重新连接" || other.Hidden() || other.Disabled() {
		t.Fatal("accessors differ from the rendered state")
	}
	node.SetTitle(node.Title())
	node.Uncheck()
	if got := s.takeSnapshot(0).changed; got != 0 {
		t.Fatalf("unchanged setters scheduled another refresh: %b", got)
	}
}

func TestScalarAndFinalUnchangedRefreshesPreserveNativeMenu(t *testing.T) {
	s := newMenuState()
	item := s.add(nil, "original", "original tip", true, false, false)
	installs := 0
	renderer := newMenuRenderer(func(*mygo.Menu) { installs++ })
	renderer.apply(s.takeSnapshot(allChanged).items)
	menu, native := renderer.menu, renderer.roots[0]
	uid := nativeUID(native)

	for range 100 {
		item.SetTitle("temporary")
		item.SetTitle("original")
		item.SetTooltip("temporary tip")
		item.SetTooltip("original tip")
		item.Disable()
		item.Enable()
		item.Hide()
		item.Show()
		item.Check()
		item.Uncheck()
	}
	pending := s.takeSnapshot(0)
	if pending.changed != menuChanged || renderer.apply(pending.items) {
		t.Fatal("a final-unchanged batch issued native updates")
	}
	for _, change := range []struct {
		name string
		set  func()
	}{
		{"title", func() { item.SetTitle("localized") }},
		{"tooltip only", func() { item.SetTooltip("localized tip") }},
		{"disable", item.Disable},
		{"hide", item.Hide},
		{"check", item.Check},
		{"enable", item.Enable},
		{"show", item.Show},
		{"uncheck", item.Uncheck},
	} {
		t.Run(change.name, func(t *testing.T) {
			change.set()
			snap := s.takeSnapshot(0)
			if !renderer.apply(snap.items) || renderer.apply(snap.items) {
				t.Fatal("changed scalar was skipped or unchanged scalar was republished")
			}
			if renderer.menu != menu || renderer.roots[0] != native || nativeUID(native) != uid || installs != 1 {
				t.Fatal("a scalar change rebuilt the menu or changed SDK identity")
			}
		})
	}
	if native.Label != "localized" || native.ToolTip != "localized tip" || native.Disabled || native.Hidden || native.Checked {
		t.Fatal("scalar native state differs from the final model state")
	}
}

func TestNativeIdentityAndCallbackLifetimeSurviveRebuildsAndGC(t *testing.T) {
	s := newMenuState()
	parent := s.add(nil, "Exit", "choose", false, false, false)
	item := parent.AddSubMenuItemCheckbox("Node", "connect", false)
	item.ClickedCh = make(chan struct{}, 1)
	renderer := newMenuRenderer(nil)
	renderer.apply(s.takeSnapshot(allChanged).items)
	old := weak.Make(renderer.items[item].native)
	oldParent := weak.Make(renderer.items[parent].native)
	uid, parentUID := nativeUID(old.Value()), nativeUID(oldParent.Value())

	// Repeated tooltip changes must neither replace native nodes nor retain a
	// history of menu trees. A new child exercises a real structural rebuild.
	for i := range 50 {
		item.SetTitle(fmt.Sprintf("节点 %d", i))
		item.SetTooltip(fmt.Sprintf("连接 %d", i))
		renderer.apply(s.takeSnapshot(0).items)
	}
	previousMenu := renderer.menu
	parent.AddSubMenuItem("Refresh", "refresh")
	renderer.apply(s.takeSnapshot(0).items)
	if renderer.menu == previousMenu || len(renderer.items) != 3 || len(renderer.roots[0].Submenu) != 2 {
		t.Fatal("structure was not rebuilt with one cached native per model item")
	}
	previousMenu = nil
	runtime.GC()
	runtime.GC()

	// This weak reference models a Windows popup's old private UID. Nothing
	// in that popup owns the Go callback item strongly.
	native := old.Value()
	if native == nil || oldParent.Value() == nil || nativeUID(native) != uid || nativeUID(oldParent.Value()) != parentUID {
		t.Fatal("rebuild or GC retired an item still used by an open popup")
	}
	if renderer.items[item].native != native || renderer.items[parent].native != oldParent.Value() {
		t.Fatal("rebuild registered replacement objects behind the same public ID")
	}
	native.Checked = true // The SDK toggles before dispatching its callback.
	native.Click(native, nil)
	if native.Checked || item.Checked() {
		t.Fatal("old popup callback did not restore the model's checked state")
	}
	select {
	case <-item.ClickedCh:
	default:
		t.Fatal("old popup callback was lost after refresh and GC")
	}
	runtime.KeepAlive(renderer)
}

func TestStructuralAndTypeChangesReuseEveryExistingNativeItem(t *testing.T) {
	s := newMenuState()
	parent := s.add(nil, "Parent", "", false, false, false)
	sibling := s.add(nil, "Sibling", "", false, false, false)
	sibling.ClickedCh = make(chan struct{}, 1)
	installs := 0
	renderer := newMenuRenderer(func(*mygo.Menu) {
		installs++
		// A synchronous native call may reenter the model. This would
		// deadlock if rendering still held menuState.mu.
		sibling.Title()
	})
	renderer.apply(s.takeSnapshot(allChanged).items)
	parentNative, siblingNative := renderer.roots[0], renderer.roots[1]
	parentUID, siblingUID := nativeUID(parentNative), nativeUID(siblingNative)
	parent.AddSubMenuItem("Child", "child")
	sibling.SetTitle("Sibling localized")
	sibling.SetTooltip("new tip")
	sibling.Disable()
	renderer.apply(s.takeSnapshot(0).items)
	if installs != 2 || parentNative.Type != mygo.MenuItemSubmenu || len(parentNative.Submenu) != 1 {
		t.Fatal("leaf-to-submenu transition was not installed")
	}
	if renderer.roots[0] != parentNative || renderer.roots[1] != siblingNative ||
		nativeUID(parentNative) != parentUID || nativeUID(siblingNative) != siblingUID {
		t.Fatal("submenu creation replaced an existing SDK callback identity")
	}
	if siblingNative.Label != "Sibling localized" || siblingNative.ToolTip != "new tip" || !siblingNative.Disabled {
		t.Fatal("mixed structure/scalar batch did not install its final state")
	}
	for _, checked := range []bool{true, false} {
		if checked {
			sibling.Check()
		} else {
			sibling.Uncheck()
		}
		renderer.apply(s.takeSnapshot(0).items)
		wantType := mygo.MenuItemNormal
		if checked {
			wantType = mygo.MenuItemCheckbox
		}
		if siblingNative.Type != wantType || siblingNative.Checked != checked || nativeUID(siblingNative) != siblingUID {
			t.Fatal("implicit checkbox type transition changed identity or state")
		}
	}
	if installs != 4 || len(renderer.items) != 3 {
		t.Fatal("type rebuilds retained replacement native items")
	}
}

func TestClickUsesCurrentStateAndNeverTogglesBusinessState(t *testing.T) {
	s := newMenuState()
	grandparent := s.add(nil, "Routing", "", false, false, false)
	parent := grandparent.AddSubMenuItem("Exit", "")
	item := parent.AddSubMenuItemCheckbox("Node", "", false)
	// A buffered channel makes delivery observable without requiring a live
	// application event loop. Production constructors use unbuffered channels.
	item.ClickedCh = make(chan struct{}, 1)
	renderer := newMenuRenderer(nil)
	renderer.apply(s.takeSnapshot(allChanged).items)
	native := renderer.items[item].native
	native.Checked = true // MyGo toggles before calling Click.
	native.Click(native, nil)
	if item.Checked() || native.Checked {
		t.Fatal("SDK checkbox toggle escaped into the legacy business state")
	}
	select {
	case <-item.ClickedCh:
	default:
		t.Fatal("enabled item did not deliver its click")
	}

	for _, change := range []struct {
		name string
		set  func()
		undo func()
	}{
		{"disabled item", item.Disable, item.Enable},
		{"hidden item", item.Hide, item.Show},
		{"disabled ancestor", parent.Disable, parent.Enable},
		{"hidden ancestor", parent.Hide, parent.Show},
		{"disabled distant ancestor", grandparent.Disable, grandparent.Enable},
		{"hidden distant ancestor", grandparent.Hide, grandparent.Show},
	} {
		t.Run(change.name, func(t *testing.T) {
			change.set()
			// An old native menu callback must consult the current model,
			// even before the next rendered snapshot is available.
			native.Checked = true
			native.Click(native, nil)
			if native.Checked {
				t.Fatal("suppressed stale click retained the SDK checkbox toggle")
			}
			select {
			case <-item.ClickedCh:
				t.Fatal("stale menu callback delivered an unavailable item")
			default:
			}
			change.undo()
		})
	}

	item.Check()
	native.Checked = false
	native.Click(native, nil)
	if !item.Checked() || !native.Checked {
		t.Fatal("callback did not restore the current checked state")
	}
	// Leave the buffer full: repeated native callbacks still must not block.
	for range 1000 {
		native.Click(native, nil)
	}
	<-item.ClickedCh

	// The default unbuffered channel drops clicks with no ready receiver.
	// Restoration also must permit reentry into the model.
	unbuffered := parent.AddSubMenuItem("Refresh", "")
	unbuffered.clicked(func(bool) { unbuffered.SetTitle("Refresh now") })
	if unbuffered.Title() != "Refresh now" {
		t.Fatal("native restore callback could not reenter the model")
	}

	// A callback from a former leaf, and callbacks after shutdown, are stale.
	item.AddSubMenuItem("New child", "")
	native.Click(native, nil)
	select {
	case <-item.ClickedCh:
		t.Fatal("a former leaf callback delivered a click after becoming a submenu")
	default:
	}
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	unbuffered.ClickedCh = make(chan struct{}, 1)
	unbuffered.clicked(nil)
	select {
	case <-unbuffered.ClickedCh:
		t.Fatal("a stopped model delivered a click")
	default:
	}
}

func TestRenderLoopCoalescesAndAllowsUpdatesDuringNativeDispatch(t *testing.T) {
	s := newMenuState()
	item := s.add(nil, "initial", "", true, false, false)
	for range 100 {
		item.Hide()
		item.Show()
		item.Check()
		item.Uncheck()
	}
	item.SetTitle("batched")
	stop := make(chan struct{})
	done := make(chan struct{})
	rendered := make(chan string, 4)
	go func() {
		defer close(done)
		s.renderLoop(stop, func(snap traySnapshot) {
			rendered <- snap.items[0].title
			if snap.items[0].title == "batched" {
				// Native dispatch may invoke application code on the UI thread.
				// No adapter lock can be held across that dispatch.
				item.SetTitle("during dispatch")
			}
		})
	}()
	var got []string
	for range 2 {
		select {
		case label := <-rendered:
			got = append(got, label)
		case <-time.After(time.Second):
			close(stop)
			t.Fatal("renderer deadlocked or lost a concurrent update")
		}
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("renderer failed to stop")
	}
	if !reflect.DeepEqual(got, []string{"batched", "during dispatch"}) {
		t.Fatalf("unexpected native refresh sequence: %v", got)
	}
	if len(rendered) != 0 {
		t.Fatal("batch produced redundant menu refreshes")
	}
}

func TestConcurrentModelUpdatesAndSnapshotRendering(t *testing.T) {
	s := newMenuState()
	parent := s.add(nil, "Exit", "", false, false, false)
	item := parent.AddSubMenuItemCheckbox("Node", "", false)
	renderer := newMenuRenderer(nil)
	renderer.apply(s.takeSnapshot(allChanged).items)
	uid := nativeUID(renderer.items[item].native)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := range 200 {
			item.SetTitle(fmt.Sprintf("Node %d", i))
			item.SetTooltip(fmt.Sprintf("Connect %d", i))
			item.Check()
			item.Uncheck()
			parent.Disable()
			parent.Enable()
			item.clicked(nil)
		}
	}()
	// The renderer is the sole native owner, while the producer only touches
	// the model. This is the same isolation used by RunOnMain in production.
	for range 200 {
		renderer.apply(s.takeSnapshot(menuChanged).items)
	}
	workers.Wait()
	renderer.apply(s.takeSnapshot(menuChanged).items)
	if nativeUID(renderer.items[item].native) != uid || renderer.items[item].native.Label != "Node 199" ||
		renderer.items[item].native.ToolTip != "Connect 199" || renderer.items[item].native.Checked || renderer.roots[0].Disabled {
		t.Fatal("concurrent model updates lost final state or stable identity")
	}
}

type clickTestTray struct {
	click  func()
	popups int
}

func (t *clickTestTray) OnClick(fn func()) func() {
	t.click = fn
	return func() { t.click = nil }
}

func (t *clickTestTray) PopUpMenu() { t.popups++ }

func TestPrimaryClickOpensMenuOnlyOnWindows(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			tray := &clickTestTray{}
			configurePrimaryClick(goos, tray)
			if goos == "windows" {
				if tray.click == nil {
					t.Fatal("Windows primary click has no menu handler")
				}
				tray.click()
				if tray.popups != 1 {
					t.Fatal("Windows primary click did not open the assigned menu")
				}
			} else if tray.click != nil || tray.popups != 0 {
				t.Fatal("overrode the platform's native click behavior")
			}
		})
	}
}
