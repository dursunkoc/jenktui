package widgets

// JobCounts returns how many real rows match the filters and how many there
// are in all. Disabled rows (Branch Filter placeholders) are not counted.
func (l *List) JobCounts() (shown, total int) {
	for _, it := range l.items {
		if !it.Disabled {
			total++
		}
	}
	for _, i := range l.visible() {
		if !l.items[i].Disabled {
			shown++
		}
	}
	return shown, total
}
