package desktopwindow

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/idy/tailsplit/internal/tray"
)

// ConfigureApplication retains the installed macOS app's application/edit/window
// menu structure and shortcuts. Business tray menus are owned by desktoptray.
func ConfigureApplication(language string, about func(), uninstallService ...func()) {
	mygo.App.SetLocale(language)
	if runtime.GOOS != "darwin" {
		return
	}
	text := func(chineseLabel string) string { return tray.Translate(language, chineseLabel) }
	item := func(chineseLabel string, role mygo.MenuRole) *mygo.MenuItem {
		return &mygo.MenuItem{Label: text(chineseLabel), Role: role}
	}
	aboutItem := &mygo.MenuItem{Label: text("关于 TailSplit"), Click: func(*mygo.MenuItem, *mygo.Window) {
		if about != nil {
			about()
		}
	}}
	applicationItems := []*mygo.MenuItem{aboutItem}
	if len(uninstallService) > 0 && uninstallService[0] != nil {
		applicationItems = append(applicationItems, &mygo.MenuItem{
			Label: text("卸载后台服务…"),
			Click: func(*mygo.MenuItem, *mygo.Window) { uninstallService[0]() },
		})
	}
	applicationItems = append(applicationItems, mygo.Separator(), item("隐藏 TailSplit", mygo.RoleHide),
		item("隐藏其他", mygo.RoleHideOthers), item("全部显示", mygo.RoleUnhide),
		mygo.Separator(), item("退出 TailSplit", mygo.RoleQuit))
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "TailSplit", Submenu: applicationItems},
		{Label: text("编辑"), Submenu: []*mygo.MenuItem{
			item("撤销", mygo.RoleUndo), item("重做", mygo.RoleRedo), mygo.Separator(),
			item("剪切", mygo.RoleCut), item("拷贝", mygo.RoleCopy), item("粘贴", mygo.RolePaste),
			item("粘贴并匹配样式", mygo.RolePasteAndMatchStyle),
			item("删除", mygo.RoleDelete), item("全选", mygo.RoleSelectAll),
		}},
		{Label: text("窗口"), Submenu: []*mygo.MenuItem{
			item("最小化", mygo.RoleMinimize), item("缩放", mygo.RoleZoom), mygo.Separator(),
			item("关闭", mygo.RoleClose),
		}},
	}))
}
