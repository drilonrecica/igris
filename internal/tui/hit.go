package tui

// The hit-region layer (SPEC §15.5): while the view is rendered, every
// clickable element records the screen rectangle it was drawn in, and a
// mouse event is mapped back to the element under the pointer.

// rect is a screen rectangle in cells; x and y are zero-based.
type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

// region names an area that scrolls with the wheel.
type region int

const (
	regionNone region = iota
	regionLog
	regionTasks
	regionPage // the body of an open page
)

// target is what a zone stands for: an action (a button), a dialog option,
// or a scrollable region.
type target struct {
	act    action
	option int // dialog option index, when act is actOption
	region region
}

type zone struct {
	r rect
	t target
}

// zones is the hit map of one rendered frame.
type zones struct{ list []zone }

// add records t at r. Zones added later are on top.
func (z *zones) add(r rect, t target) {
	if r.w > 0 && r.h > 0 {
		z.list = append(z.list, zone{r, t})
	}
}

// merge adds the zones of a block drawn at (dx, dy).
func (z *zones) merge(o zones, dx, dy int) {
	for _, zn := range o.list {
		zn.r.x += dx
		zn.r.y += dy
		z.list = append(z.list, zn)
	}
}

// at returns the topmost target at (x, y).
func (z *zones) at(x, y int) (target, bool) {
	for i := len(z.list) - 1; i >= 0; i-- {
		if z.list[i].r.contains(x, y) {
			return z.list[i].t, true
		}
	}
	return target{}, false
}

func (z *zones) reset() { z.list = z.list[:0] }
