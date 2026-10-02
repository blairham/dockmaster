package views

import "testing"

// TestToggleWideRefits: the wide columns are fitted to the last width
// straight away — not left at their declared widths until something else
// happens to resize the view. (In the app the flash that ctrl+w raises
// resizes it and would hide the difference.)
func TestToggleWideRefits(t *testing.T) {
	v := NewContainersView(nil, false, false)
	v.Resize(160, 20)
	v.ToggleWide()
	total := 0
	for _, c := range v.Table().Columns() {
		total += c.Width + cellPadding
	}
	if total > 160 || len(v.Table().Columns()) != 12 {
		t.Errorf("%d wide columns fill %d cells, over the 160 they were sized to", len(v.Table().Columns()), total)
	}
}
