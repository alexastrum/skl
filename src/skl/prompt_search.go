package skl

import (
	"fmt"
	"regexp"
	"strings"
)

// SearchItem is one row of SearchMultiSelect (upstream SearchItem).
type SearchItem struct {
	// Value identifies the item to callers and is matched by the filter, like
	// String(item.value) upstream. It may be empty.
	Value string
	Label string
	Hint  string
	// Group is an optional heading shown before this item.
	Group string
	// Detail is shown in a fixed pane while the item is highlighted.
	Detail string
}

// LockedSection lists always-selected items above the searchable list.
type LockedSection struct {
	Title       string
	Items       []SearchItem
	HiddenCount int
}

// SearchMultiSelectOptions mirrors upstream SearchMultiselectOptions.
type SearchMultiSelectOptions struct {
	Message string
	Items   []SearchItem
	// MaxVisible defaults to 8.
	MaxVisible int
	// Initial holds indexes into Items that start selected.
	Initial []int
	// Required blocks Enter while nothing is selected (locked items count).
	Required bool
	Locked   *LockedSection
	// NoSearch hides the search input (upstream searchable: false).
	NoSearch bool
	// ShowDetail renders a fixed-height detail pane for the highlighted row.
	ShowDetail bool
	// DetailLines defaults to 2.
	DetailLines int
	// HideSelectedSummary drops the "Selected:" line (showSelectedSummary: false).
	HideSelectedSummary bool
	// SelectGroups makes group headings selectable and collapsible.
	SelectGroups bool
	// SelectAll adds a Select All row above the items.
	SelectAll bool
}

type searchEntry struct {
	group     string
	items     []int // group entries: member indexes
	item      int   // item entries: index into Items
	isGroup   bool
	collapsed bool
}

// buildSearchEntries ports upstream buildSearchEntries over item indexes.
func buildSearchEntries(items []SearchItem, filtered []int, selectGroups bool, collapsed map[string]bool) []searchEntry {
	var entries []searchEntry
	if !selectGroups {
		for _, i := range filtered {
			entries = append(entries, searchEntry{item: i})
		}
		return entries
	}
	for k := 0; k < len(filtered); {
		i := filtered[k]
		g := items[i].Group
		if g == "" {
			entries = append(entries, searchEntry{item: i})
			k++
			continue
		}
		var members []int
		for k < len(filtered) && items[filtered[k]].Group == g {
			members = append(members, filtered[k])
			k++
		}
		entries = append(entries, searchEntry{isGroup: true, group: g, items: members, collapsed: collapsed[g]})
		if !collapsed[g] {
			for _, m := range members {
				entries = append(entries, searchEntry{item: m})
			}
		}
	}
	return entries
}

// orderedSet keeps insertion order, like a JS Set.
type orderedSet struct {
	order []int
	has   map[int]bool
}

func newOrderedSet(init []int) *orderedSet {
	s := &orderedSet{has: map[int]bool{}}
	for _, i := range init {
		s.add(i)
	}
	return s
}

func (s *orderedSet) add(i int) {
	if !s.has[i] {
		s.has[i] = true
		s.order = append(s.order, i)
	}
}

func (s *orderedSet) del(i int) {
	if !s.has[i] {
		return
	}
	delete(s.has, i)
	for k, v := range s.order {
		if v == i {
			s.order = append(s.order[:k], s.order[k+1:]...)
			break
		}
	}
}

func (s *orderedSet) toggle(i int) {
	if s.has[i] {
		s.del(i)
	} else {
		s.add(i)
	}
}

// toggleSelectAll selects every listed item unless all are selected, in which
// case it clears them (upstream toggleSelectAll / toggleAllItems, #1872).
func toggleSelectAll(sel *orderedSet, list []int) {
	all := true
	for _, i := range list {
		if !sel.has[i] {
			all = false
			break
		}
	}
	for _, i := range list {
		if all {
			sel.del(i)
		} else {
			sel.add(i)
		}
	}
}

// selectAllState is "none", "partial" or "all".
func selectAllState(sel *orderedSet, list []int) string {
	n := 0
	for _, i := range list {
		if sel.has[i] {
			n++
		}
	}
	switch {
	case n == 0:
		return "none"
	case n == len(list):
		return "all"
	}
	return "partial"
}

var detailSpaceRe = regexp.MustCompile(`\s+`)

// formatDetailLines ports upstream formatDetailLines: wrap detail into exactly
// maxLines lines of at most width cells, ending a cut text with an ellipsis.
func formatDetailLines(detail string, width, maxLines int, ellipsis string) []string {
	width = max(1, width)
	remaining := strings.TrimSpace(detailSpaceRe.ReplaceAllString(detail, " "))
	var lines []string
	for remaining != "" && len(lines) < maxLines {
		if approxStringWidth(remaining) <= width {
			lines = append(lines, remaining)
			remaining = ""
			break
		}
		candidate := truncateToWidth(remaining, width)
		if at := strings.LastIndex(candidate, " "); at > 0 {
			lines = append(lines, strings.TrimRight(candidate[:at], " "))
			remaining = strings.TrimLeft(remaining[at:], " ")
		} else {
			lines = append(lines, candidate)
			remaining = strings.TrimLeft(remaining[len(candidate):], " ")
		}
	}
	if remaining != "" && len(lines) > 0 {
		last := len(lines) - 1
		lines[last] = strings.TrimRight(truncateToWidth(lines[last], max(0, width-approxStringWidth(ellipsis))), " ") + ellipsis
	}
	for len(lines) < maxLines {
		lines = append(lines, "")
	}
	return lines
}

// SearchMultiSelect is upstream's searchMultiselect: type to filter, arrows to
// move, space to toggle, Ctrl+A to toggle every filtered item, Enter to confirm,
// Ctrl+C or Esc to cancel. It returns the selected item indexes in the order
// they were selected; locked items are display-only and callers add them.
func (t *promptTerm) SearchMultiSelect(o SearchMultiSelectOptions) ([]int, error) {
	end, err := t.begin()
	if err != nil {
		return nil, err
	}
	defer end()
	maxVisible := o.MaxVisible
	if maxVisible <= 0 {
		maxVisible = 8
	}
	detailLines := o.DetailLines
	if detailLines <= 0 {
		detailLines = 2
	}
	searchable := !o.NoSearch
	var lockedItems []SearchItem
	if o.Locked != nil {
		lockedItems = o.Locked.Items
	}
	all := make([]int, len(o.Items))
	for i := range all {
		all[i] = i
	}
	query := []rune{}
	cursor := 0
	sel := newOrderedSet(o.Initial)
	collapsed := map[string]bool{}
	y := t.sym()
	bar := t.paint(y.bar, sgrDim)
	barH := t.paint(y.barH, sgrDim)
	hasSelectAll := o.SelectAll && len(o.Items) > 0
	offset := 0
	if hasSelectAll {
		offset = 1
	}

	filtered := func() []int {
		q := strings.ToLower(string(query))
		if q == "" {
			return all
		}
		var out []int
		for i, it := range o.Items {
			if strings.Contains(strings.ToLower(it.Label), q) || (it.Value != "" && strings.Contains(strings.ToLower(it.Value), q)) {
				out = append(out, i)
			}
		}
		return out
	}
	selectedLabels := func() []string {
		var labels []string
		for _, it := range lockedItems {
			labels = append(labels, it.Label)
		}
		for _, i := range all {
			if sel.has[i] {
				labels = append(labels, o.Items[i].Label)
			}
		}
		return labels
	}

	render := func(state promptState) []string {
		var lines []string
		icon := t.paint(y.stepActive, sgrGreen)
		switch state {
		case promptSubmit:
			icon = t.paint(y.stepSubmit, sgrGreen)
		case promptCancel:
			icon = t.paint(y.stepCancel, sgrRed)
		}
		lines = append(lines, icon+"  "+t.paint(o.Message, sgrBold))
		switch state {
		case promptSubmit:
			return append(lines, bar+"  "+t.paint(strings.Join(selectedLabels(), ", "), sgrDim))
		case promptCancel:
			return append(lines, bar+"  "+t.paint("Cancelled", sgrStrike, sgrDim))
		}
		list := filtered()
		entries := buildSearchEntries(o.Items, list, o.SelectGroups, collapsed)
		entryCursor := cursor - offset

		if len(lockedItems) > 0 {
			lines = append(lines, bar)
			title := t.paint(o.Locked.Title, sgrBold) + " " + t.paint(strings.Repeat(y.barH, 2)+" always included", sgrDim)
			lines = append(lines, bar+"  "+barH+barH+" "+title+" "+strings.Repeat(barH, 12))
			for _, it := range lockedItems {
				lines = append(lines, bar+"    "+t.paint(y.bullet, sgrGreen)+" "+t.paint(it.Label, sgrBold))
			}
			if o.Locked.HiddenCount > 0 {
				lines = append(lines, bar+"    "+t.paint(fmt.Sprintf("%sand %d more", y.ellipsis, o.Locked.HiddenCount), sgrDim))
			}
			lines = append(lines, bar)
			lines = append(lines, bar+"  "+barH+barH+" "+t.paint("Additional agents", sgrBold)+" "+strings.Repeat(barH, 29))
		}
		if searchable {
			lines = append(lines, bar+"  "+t.paint("Search:", sgrDim)+" "+string(query)+t.paint(" ", sgrInverse))
			lines = append(lines, bar+"  "+t.paint(y.upDown+" move, space select, ctrl+a select all, enter confirm", sgrDim))
			lines = append(lines, bar)
		}
		if hasSelectAll {
			n := 0
			for _, i := range all {
				if sel.has[i] {
					n++
				}
			}
			radio := t.paint(y.searchOff, sgrDim)
			switch selectAllState(sel, all) {
			case "all":
				radio = t.paint(y.searchOn, sgrGreen)
			case "partial":
				radio = t.paint(y.searchPartial, sgrYellow)
			}
			prefix, label := " ", t.paint("Select All", sgrBold)
			if cursor == 0 {
				prefix, label = t.paint(y.pointer, sgrCyan), t.paint("Select All", sgrUnderline, sgrBold)
			}
			lines = append(lines, fmt.Sprintf("%s %s %s %s %s", bar, prefix, radio, label, t.paint(fmt.Sprintf("(%d/%d)", n, len(o.Items)), sgrDim)))
			lines = append(lines, bar+"   "+strings.Repeat(barH, 36))
		}

		cols, rows := t.size()
		buildFooter := func(detail, summary bool) []string {
			var footer []string
			if detail {
				var text string
				switch {
				case hasSelectAll && cursor == 0:
					text = fmt.Sprintf("Select or clear all %d skills.", len(o.Items))
				case entryCursor >= 0 && entryCursor < len(entries) && entries[entryCursor].isGroup:
					e := entries[entryCursor]
					text = fmt.Sprintf("Select all %d skills in %s.", len(e.items), e.group)
				case entryCursor >= 0 && entryCursor < len(entries):
					text = o.Items[entries[entryCursor].item].Detail
				}
				footer = append(footer, bar, bar+"  "+t.paint("Description", sgrDim))
				for _, l := range formatDetailLines(text, max(1, cols-5), detailLines, y.ellipsis) {
					footer = append(footer, bar+"  "+t.paint(l, sgrDim))
				}
			}
			if summary {
				footer = append(footer, bar)
				labels := selectedLabels()
				if len(labels) == 0 {
					footer = append(footer, bar+"  "+t.paint("Selected: (none)", sgrDim))
				} else {
					s := strings.Join(labels, ", ")
					if len(labels) > 3 {
						s = fmt.Sprintf("%s +%d more", strings.Join(labels[:3], ", "), len(labels)-3)
					}
					footer = append(footer, bar+"  "+t.paint("Selected:", sgrGreen)+" "+s)
				}
			}
			if !searchable {
				footer = append(footer, bar, bar+"  "+t.paint(y.upDown+" move, "+y.leftRight+" collapse/expand, space select, enter confirm", sgrDim))
			}
			return append(footer, t.paint(y.barEnd, sgrDim))
		}
		buildItems := func(limit int) []string {
			if len(list) == 0 {
				return []string{bar + "  " + t.paint("No matches found", sgrDim)}
			}
			start := max(0, min(entryCursor-limit/2, len(entries)-limit))
			stop := min(len(entries), start+limit)
			var out []string
			for k := start; k < stop; k++ {
				e := entries[k]
				isCursor := k == entryCursor
				prefix := " "
				if isCursor {
					prefix = t.paint(y.pointer, sgrCyan)
				}
				if e.isGroup {
					n := 0
					for _, i := range e.items {
						if sel.has[i] {
							n++
						}
					}
					radio := t.paint(y.searchOff, sgrDim)
					if n == len(e.items) {
						radio = t.paint(y.searchOn, sgrGreen)
					} else if n > 0 {
						radio = t.paint(y.searchPartial, sgrYellow)
					}
					label := t.paint(e.group, sgrBold)
					if isCursor {
						label = t.paint(e.group, sgrUnderline, sgrBold)
					}
					disc := y.expanded
					if e.collapsed {
						disc = y.collapsed
					}
					out = append(out, fmt.Sprintf("%s %s %s %s %s", bar, prefix, t.paint(disc, sgrDim), radio, label))
					continue
				}
				it := o.Items[e.item]
				radio := t.paint(y.searchOff, sgrDim)
				if sel.has[e.item] {
					radio = t.paint(y.searchOn, sgrGreen)
				}
				label := it.Label
				if isCursor {
					label = t.paint(it.Label, sgrUnderline)
				}
				hint := ""
				if it.Hint != "" {
					hint = t.paint(" ("+it.Hint+")", sgrDim)
				}
				tree := ""
				if o.SelectGroups && it.Group != "" {
					last := -1
					for _, i := range list {
						if o.Items[i].Group == it.Group {
							last = i
						}
					}
					glyph := y.treeMid
					if last == e.item {
						glyph = y.treeEnd
					}
					tree = t.paint(glyph, sgrDim) + " "
				}
				out = append(out, fmt.Sprintf("%s %s %s%s %s%s", bar, prefix, tree, radio, label, hint))
			}
			var parts []string
			if start > 0 {
				parts = append(parts, fmt.Sprintf("%s %d more", y.up, start))
			}
			if hidden := len(entries) - stop; hidden > 0 {
				parts = append(parts, fmt.Sprintf("%s %d more", y.down, hidden))
			}
			if len(parts) > 0 {
				out = append(out, bar+"  "+t.paint(strings.Join(parts, "  "), sgrDim))
			}
			return out
		}
		maxRows := 0
		if rows > 0 {
			maxRows = max(1, rows-1)
		}
		fit := func(detail, summary bool) ([]string, []string, int) {
			footer := buildFooter(detail, summary)
			limit := max(1, maxVisible)
			items := buildItems(limit)
			n := len(lines) + len(items) + len(footer)
			for maxRows > 0 && n > maxRows && limit > 1 {
				limit--
				items = buildItems(limit)
				n = len(lines) + len(items) + len(footer)
			}
			return items, footer, n
		}
		detail, summary := o.ShowDetail, !o.HideSelectedSummary
		items, footer, n := fit(detail, summary)
		// On short terminals keep controls and rows before the optional panes.
		if maxRows > 0 && n > maxRows && detail {
			detail = false
			items, footer, n = fit(detail, summary)
		}
		if maxRows > 0 && n > maxRows && summary {
			summary = false
			items, footer, _ = fit(detail, summary)
		}
		lines = append(lines, items...)
		return append(lines, footer...)
	}

	f := &promptFrame{t: t}
	f.draw(render(promptActive))
	for {
		k := t.keys().next()
		list := filtered()
		entries := buildSearchEntries(o.Items, list, o.SelectGroups, collapsed)
		var entry *searchEntry
		if i := cursor - offset; i >= 0 && i < len(entries) {
			entry = &entries[i]
		}
		switch {
		case k.kind == keyEnter:
			if o.Required && len(sel.order) == 0 && len(lockedItems) == 0 {
				continue
			}
			f.draw(render(promptSubmit))
			out := make([]int, len(sel.order))
			copy(out, sel.order)
			return out, nil
		case k.isCancel():
			f.draw(render(promptCancel))
			return nil, ErrPromptCancel
		case k.kind == keyUp:
			cursor = max(0, cursor-1)
		case k.kind == keyDown:
			cursor = min(len(entries)+offset-1, cursor+1)
		case k.kind == keyRight:
			if !o.SelectGroups {
				continue
			}
			if entry != nil && entry.isGroup && entry.collapsed {
				delete(collapsed, entry.group)
			} else {
				continue
			}
		case k.kind == keyLeft:
			if !o.SelectGroups || entry == nil {
				continue
			}
			g := entry.group
			if !entry.isGroup {
				g = o.Items[entry.item].Group
			}
			if g == "" {
				continue
			}
			collapsed[g] = true
			cursor = offset - 1
			for i, e := range buildSearchEntries(o.Items, list, o.SelectGroups, collapsed) {
				if e.isGroup && e.group == g {
					cursor = i + offset
					break
				}
			}
		case k.kind == keySpace:
			switch {
			case hasSelectAll && cursor == 0:
				toggleSelectAll(sel, all)
			case entry != nil && entry.isGroup:
				toggleSelectAll(sel, entry.items)
			case entry != nil:
				sel.toggle(entry.item)
			}
		case k.kind == keyCtrlA:
			toggleSelectAll(sel, list)
		case k.kind == keyBackspace:
			if len(query) > 0 {
				query = query[:len(query)-1]
			}
			cursor = 0
		case k.kind == keyRune && searchable:
			query = append(query, k.r)
			cursor = 0
		default:
			continue
		}
		f.draw(render(promptActive))
	}
}
