package tray

type LoginItemStatus int

const (
	LoginItemDisabled LoginItemStatus = iota
	LoginItemEnabled
	LoginItemNeedsApproval
	LoginItemUnavailable
)
